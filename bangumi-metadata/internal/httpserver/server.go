package httpserver

import (
	"embed"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/user/bangumi-metadata/internal/bangumi"
	"github.com/user/bangumi-metadata/internal/database"
	"github.com/user/bangumi-metadata/internal/scraper"
)

//go:embed admin.html
var adminFS embed.FS

type Server struct {
	db         *database.DB
	bgClient   *bangumi.Client
	scraper    *scraper.Scraper
	accessToken string
	mu         sync.Mutex
	running    bool
	lastRun    *scraper.ScrapeResult
	runErr     string
}

func New(db *database.DB, bgClient *bangumi.Client, accessToken string) *Server {
	return &Server{
		db:          db,
		bgClient:    bgClient,
		scraper:     scraper.New(bgClient, db, accessToken, false, ""),
		accessToken: accessToken,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/confirm", s.handleConfirm)
	mux.HandleFunc("/api/scrape", s.handleScrape)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/update", s.handleUpdate)
	mux.HandleFunc("/api/cover/delete", s.handleCoverDelete)
	mux.HandleFunc("/api/cover/generate", s.handleCoverGenerate)
	mux.HandleFunc("/api/cover/status", s.handleCoverStatus)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/series/", s.handleSeriesMeta)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/", s.handleAdmin)
	return mux
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	seriesID := r.URL.Query().Get("series")
	name := r.URL.Query().Get("name")
	searchTerm := r.URL.Query().Get("q")
	source := r.URL.Query().Get("source")
	if seriesID == "" {
		writeJSON(w, 400, map[string]any{"error": "series required"})
		return
	}
	if searchTerm == "" {
		searchTerm = name
	}
	if searchTerm == "" {
		writeJSON(w, 400, map[string]any{"error": "name or q required"})
		return
	}

	cleanName := bangumi.CleanFolderName(searchTerm)
	var matches []map[string]any

	switch source {
	case "mangadex":
		mdResults, err := s.bgClient.SearchMangaDex(cleanName, 8)
		if err == nil {
			for _, md := range mdResults {
				matches = append(matches, map[string]any{
					"id":       md.ID,
					"name":     md.Name,
					"nameCn":   md.NameCN,
					"image":    md.Image,
					"platform": "MangaDex",
					"source":   "mangadex",
				})
			}
		} else {
			log.Printf("MangaDex search error: %v", err)
		}
	case "bof":
		bofResults, err := s.bgClient.SearchBookOfMoe(cleanName, 8)
		if err == nil {
			for _, bof := range bofResults {
				matches = append(matches, map[string]any{
					"id":       bof.ID,
					"name":     bof.Title,
					"nameCn":   bof.Title,
					"image":    "",
					"platform": "BookOfMoe",
					"source":   "bof",
					"author":   bof.Author,
				})
			}
		} else {
			log.Printf("BookOfMoe search error: %v", err)
		}
	default: // bangumi
		results, err := s.bgClient.SearchSubjects(cleanName, bangumi.SubjectBook, 8)
		if err != nil {
			writeJSON(w, 502, map[string]any{"error": err.Error()})
			return
		}
		for _, r := range results {
			if r.Platform == "漫画" || r.Platform == "" {
				matches = append(matches, map[string]any{
					"id":       r.ID,
					"name":     r.Name,
					"nameCn":   r.NameCN,
					"image":    r.Image,
					"platform": r.Platform,
					"source":   "bangumi",
				})
			}
		}
	}

	writeJSON(w, 200, map[string]any{
		"seriesId":   seriesID,
		"seriesName": name,
		"searchName": cleanName,
		"results":    matches,
	})
}

func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req struct {
		SeriesID  string `json:"seriesId"`
		BangumiID int    `json:"bangumiId"`
		Source    string `json:"source"`
		SkipCover *bool  `json:"skipCover"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid request"})
		return
	}

	if req.SeriesID == "" || req.BangumiID == 0 {
		writeJSON(w, 400, map[string]any{"error": "seriesId and bangumiId required"})
		return
	}

	skipCover := true // 默认不写 Bangumi 封面，使用 fake-komga 自生成封面
	if req.SkipCover != nil {
		skipCover = *req.SkipCover
	} else if v, _ := s.db.GetSetting(context.Background(), "skip_cover"); v != "" {
		skipCover = v == "true"
	}
	s.scraper.SetSkipCover(skipCover)
	// 每次刮削前从设置读取最新 access_token
	if token, _ := s.db.GetSetting(context.Background(), "access_token"); token != "" {
		s.bgClient.SetAccessToken(token)
	}
	result, err := s.scraper.ScrapeSingle(req.SeriesID, req.BangumiID)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}

	s.db.ClearFailed(req.SeriesID)
	writeJSON(w, 200, result)
}

func (s *Server) handleScrape(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	forceRefresh := r.URL.Query().Get("force") == "true"

	// 可选 body: {"seriesIds": ["..."]} 指定系列批量刮削；缺省=全部
	var req struct {
		SeriesIDs []string `json:"seriesIds"`
	}
	var seriesIDs []string
	if r.Body != nil {
		bodyBytes, _ := io.ReadAll(r.Body)
		if len(bodyBytes) > 0 {
			json.Unmarshal(bodyBytes, &req)
			seriesIDs = req.SeriesIDs
		}
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		writeJSON(w, 409, map[string]any{"error": "scrape already running"})
		return
	}
	s.running = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()
		// 每次刮削前从设置读取最新正则和封面策略，无需重启
		if re, _ := s.db.GetSetting(context.Background(), "batch_keyword_regex"); re != "" {
			s.scraper.SetBatchRegex(re)
		}
		if token, _ := s.db.GetSetting(context.Background(), "access_token"); token != "" {
			s.bgClient.SetAccessToken(token)
		}
		if sc, _ := s.db.GetSetting(context.Background(), "skip_cover"); sc == "" {
			s.scraper.SetSkipCover(true)
		} else {
			s.scraper.SetSkipCover(sc == "true")
		}
		var result *scraper.ScrapeResult
		var err error
		if len(seriesIDs) > 0 {
			log.Printf("Starting scrape selected (%d series)", len(seriesIDs))
			result, err = s.scraper.ScrapeSelected(seriesIDs, forceRefresh)
		} else {
			log.Printf("Starting scrape all (force=%v)", forceRefresh)
			result, err = s.scraper.ScrapeAll(forceRefresh)
		}
		s.mu.Lock()
		s.lastRun = result
		if err != nil {
			s.runErr = err.Error()
		} else {
			s.runErr = ""
		}
		s.mu.Unlock()
		log.Printf("Scrape completed: %+v", result)
	}()

	writeJSON(w, 202, map[string]any{"status": "started", "force": forceRefresh, "selected": len(seriesIDs)})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	running := s.running
	lastRun := s.lastRun
	runErr := s.runErr
	s.mu.Unlock()
	resp := map[string]any{"running": running}
	if lastRun != nil {
		resp["lastRun"] = lastRun
	}
	if runErr != "" {
		resp["error"] = runErr
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.GetStats()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	allSeries, _ := s.db.GetAllSeries()
	allList := make([]map[string]any, 0, len(allSeries))
	for _, ser := range allSeries {
		item := map[string]any{
			"id":           ser.ID,
			"name":         ser.Name,
			"libraryName":  ser.LibraryName,
			"fk115CoverUrl": s.fk115URL() + "/api/v1/series/" + ser.ID + "/thumbnail",
		}
		meta, _ := s.db.GetSeriesMeta(ser.ID)
		if meta != nil {
			item["matched"] = true
			item["bangumiTitle"] = meta.TitleCN
			item["coverUrl"] = meta.CoverURL
			item["tags"] = meta.Tags
			item["rating"] = meta.Rating
			item["publisher"] = meta.Publisher
			item["status"] = meta.Status
		} else {
			item["matched"] = false
		}
		allList = append(allList, item)
	}
	stats["allSeries"] = allList
	stats["pendingList"] = allList
	failedSeries, _ := s.db.GetFailedSeries()
	stats["failedSeries"] = len(failedSeries)
	failedList := make([]map[string]any, 0)
	for _, fs := range failedSeries {
		failedList = append(failedList, map[string]any{
			"id":          fs.ID,
			"name":        fs.Name,
			"libraryName": fs.LibraryName,
		})
	}
	stats["failedList"] = failedList
	writeJSON(w, 200, stats)
}

func (s *Server) handleSeriesMeta(w http.ResponseWriter, r *http.Request) {
	seriesID := strings.TrimPrefix(r.URL.Path, "/api/series/")
	if seriesID == "" {
		http.Error(w, "series ID required", 400)
		return
	}
	meta, err := s.db.GetSeriesMeta(seriesID)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if meta == nil {
		// 未刮削的系列也返回详情(空元数据+封面),让前端可打开详情面板
		empty := &database.SeriesMeta{SeriesID: seriesID}
		writeJSON(w, 200, s.addFk115CoverURL(seriesID, empty))
		return
	}
	writeJSON(w, 200, s.addFk115CoverURL(seriesID, meta))
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		matchType, _ := s.db.GetSetting(context.Background(), "match_type")
		accessToken, _ := s.db.GetSetting(context.Background(), "access_token")
		fetchVol, _ := s.db.GetSetting(context.Background(), "fetch_volume_data")
		forceRef, _ := s.db.GetSetting(context.Background(), "force_refresh")
		if matchType == "" { matchType = "漫画" }
		skipCover, _ := s.db.GetSetting(context.Background(), "skip_cover")
	fk115Url, _ := s.db.GetSetting(context.Background(), "fk115_url")
	batchKeywordRegex, _ := s.db.GetSetting(context.Background(), "batch_keyword_regex")
	if skipCover == "" {
		skipCover = "true"
	}
		writeJSON(w, 200, map[string]any{
			"matchType":      matchType,
			"accessToken":    accessToken,
			"fetchVolumeData": fetchVol == "true",
			"forceRefresh":   forceRef == "true",
			"skipCover":      skipCover == "true",
			"fk115Url":      fk115Url,
			"batchKeywordRegex": batchKeywordRegex,
		})
		return
	}
	if r.Method == "POST" {
		var req struct {
			MatchType      string `json:"matchType"`
			AccessToken    string `json:"accessToken"`
			FetchVolumeData bool   `json:"fetchVolumeData"`
			ForceRefresh    bool   `json:"forceRefresh"`
			SkipCover       bool   `json:"skipCover"`
			Fk115Url        string `json:"fk115Url"`
			BatchKeywordRegex string `json:"batchKeywordRegex"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid request"})
			return
		}
		ctx := r.Context()
		s.db.SetSetting(ctx, "match_type", req.MatchType)
		s.db.SetSetting(ctx, "access_token", req.AccessToken)
		s.db.SetSetting(ctx, "fetch_volume_data", map[bool]string{true: "true", false: "false"}[req.FetchVolumeData])
		s.db.SetSetting(ctx, "force_refresh", map[bool]string{true: "true", false: "false"}[req.ForceRefresh])
		s.db.SetSetting(ctx, "skip_cover", map[bool]string{true: "true", false: "false"}[req.SkipCover])
		s.db.SetSetting(ctx, "fk115_url", req.Fk115Url)
		s.db.SetSetting(ctx, "batch_keyword_regex", req.BatchKeywordRegex)
		writeJSON(w, 200, map[string]any{"status": "ok"})
		return
	}
	http.Error(w, "method not allowed", 405)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		SeriesID     string           `json:"seriesId"`
		TitleCN      string           `json:"titleCn"`
		TitleJP      string           `json:"titleJp"`
		Publisher    string           `json:"publisher"`
		Platform     string           `json:"platform"`
		Status       string           `json:"status"`
		Summary      string           `json:"summary"`
		CoverURL     string           `json:"coverUrl"`
		Rating       float64          `json:"rating"`
		TotalVolumes int              `json:"totalVolumes"`
		Tags         []string         `json:"tags"`
		Authors      []database.Author `json:"authors"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid request"})
		return
	}
	if req.SeriesID == "" {
		writeJSON(w, 400, map[string]any{"error": "seriesId required"})
		return
	}

	// Get existing meta
	meta, err := s.db.GetSeriesMeta(req.SeriesID)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}

	if meta == nil {
		// Create new meta
		meta = &database.SeriesMeta{SeriesID: req.SeriesID}
	}

	// Always update all fields from request
	meta.TitleCN = req.TitleCN
	meta.TitleJP = req.TitleJP
	meta.Publisher = req.Publisher
	meta.Platform = req.Platform
	meta.Status = req.Status
	meta.Summary = req.Summary
	meta.CoverURL = req.CoverURL
	meta.Rating = req.Rating
	meta.TotalVolumes = req.TotalVolumes
	meta.Tags = req.Tags
	meta.Authors = req.Authors

	meta.UpdatedAt = time.Now()

	if err := s.db.SaveSeriesMeta(meta); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, 200, map[string]any{"status": "ok"})
}

func (s *Server) handleCoverDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		SeriesID string `json:"seriesId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SeriesID == "" {
		writeJSON(w, 400, map[string]any{"error": "seriesId required"})
		return
	}
	resp, err := http.Post(
		s.fk115URL()+"/api/covers/delete",
		"application/json",
		strings.NewReader(fmt.Sprintf(`{"seriesId":"%s"}`, req.SeriesID)),
	)
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": fmt.Sprintf("cannot connect to fake-komga: %v", err)})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		writeJSON(w, 502, map[string]any{"error": fmt.Sprintf("fake-komga returned %d", resp.StatusCode)})
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "message": "封面已删除（下次访问时重新生成）"})
}

func (s *Server) handleCoverGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		SeriesID string `json:"seriesId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SeriesID == "" {
		writeJSON(w, 400, map[string]any{"error": "seriesId required"})
		return
	}
	// fake-komga-local 懒生成: 访问 thumbnail 端点即触发生成
	resp, err := http.Get(s.fk115URL() + "/api/v1/series/" + req.SeriesID + "/thumbnail")
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": fmt.Sprintf("cannot connect to fake-komga: %v", err)})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		writeJSON(w, 502, map[string]any{"error": fmt.Sprintf("fake-komga returned %d", resp.StatusCode)})
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "message": "封面生成任务已提交"})
}

func (s *Server) handleCoverStatus(w http.ResponseWriter, r *http.Request) {
	seriesID := r.URL.Query().Get("series")
	if seriesID == "" {
		writeJSON(w, 400, map[string]any{"error": "series required"})
		return
	}
	// Query fake-komga-115's thumbnail status
	resp, err := http.Get(s.fk115URL() + "/admin/api/maintenance-jobs?targetType=series&targetId=" + seriesID)
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": fmt.Sprintf("cannot connect to fake-komga-115: %v", err)})
		return
	}
	defer resp.Body.Close()
	var result any
	json.NewDecoder(resp.Body).Decode(&result)
	writeJSON(w, 200, result)
}

func (s *Server) fk115URL() string {
	url, _ := s.db.GetSetting(context.Background(), "fk115_url")
	if url == "" {
		url = "http://fake-komga-115-dev:25600"
	}
	return url
}

func (s *Server) addFk115CoverURL(seriesID string, meta *database.SeriesMeta) map[string]any {
	m := map[string]any{
		"seriesId": meta.SeriesID,
		"bangumiId": meta.BangumiID,
		"titleCn": meta.TitleCN,
		"titleJp": meta.TitleJP,
		"summary": meta.Summary,
		"publisher": meta.Publisher,
		"status": meta.Status,
		"totalVolumes": meta.TotalVolumes,
		"rating": meta.Rating,
		"ratingCount": meta.RatingCount,
		"tags": meta.Tags,
		"authors": meta.Authors,
		"coverUrl": meta.CoverURL,
		"platform": meta.Platform,
		"updatedAt": meta.UpdatedAt,
		"fk115CoverUrl": s.fk115URL() + "/api/v1/series/" + seriesID + "/thumbnail",
	}
	return m
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := adminFS.ReadFile("admin.html")
	if err != nil {
		http.Error(w, "admin page not found", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) ListenAndServe(addr string) error {
	fmt.Printf("Starting HTTP server on %s\n", addr)
	fmt.Printf("  GET  /api/search?series=&name= - search Bangumi\n")
	fmt.Printf("  POST /api/confirm             - confirm match & scrape\n")
	fmt.Printf("  POST /api/scrape              - auto scrape all\n")
	fmt.Printf("  GET  /api/stats               - stats + pending list\n")
	fmt.Printf("  GET  /                        - admin page\n")
	return http.ListenAndServe(addr, s.Handler())
}
