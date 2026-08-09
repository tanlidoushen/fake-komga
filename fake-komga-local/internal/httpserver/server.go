package httpserver

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"fake-komga-local/internal/archive"
	"fake-komga-local/internal/database"
	"fake-komga-local/internal/scanner"
	"fake-komga-local/internal/thumbnail"
)

//go:embed admin.html
var adminHTML embed.FS

type Server struct {
	db        *database.DB
	scan      *scanner.Scanner
	thumb     *thumbnail.Service
	comicsDir string
	log       *slog.Logger
	router    *chi.Mux
	startedAt time.Time
}

func New(db *database.DB, scan *scanner.Scanner, thumb *thumbnail.Service, comicsDir string, log *slog.Logger) *Server {
	s := &Server{db: db, scan: scan, thumb: thumb, comicsDir: comicsDir, log: log, startedAt: time.Now()}
	s.router = chi.NewRouter()
	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.Timeout(60 * time.Second))
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) routes() {
	s.router.Get("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin", 307) })
	s.router.Get("/admin", s.adminPage)
s.router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	})
	s.router.Post("/api/scan", s.handleScan)
	s.router.Post("/api/covers/generate", s.handleCovers)
	s.router.Post("/api/covers/delete", s.handleCoverDelete)
	s.router.Get("/api/settings", s.getSettings)
	s.router.Post("/api/settings", s.saveSettings)
	s.router.Get("/api/scan/runs", s.handleScanRuns)
	s.router.Get("/api/scan/status", s.handleScanStatus)

	s.router.Route("/api/v1", func(r chi.Router) {
		r.Get("/libraries", s.listLibraries)
		r.Get("/libraries/{id}/series", s.librarySeries)
		r.Get("/series", s.listSeries)
		r.Get("/series/{id}", s.getSeries)
		r.Get("/series/{id}/thumbnail", s.seriesThumbnail)
		r.Get("/genres", s.emptyList)
		r.Get("/tags", s.emptyList)
		r.Get("/publishers", s.emptyList)
		r.Get("/authors", s.emptyList)
		r.Get("/collections", s.emptyList)
		r.Get("/readlists", s.emptyList)
		r.Get("/series/{id}/books", s.seriesBooks)
		r.Get("/books", s.listBooks)
		r.Get("/books/{id}", s.getBook)
		r.Get("/books/{id}/pages", s.bookPages)
		r.Get("/books/{id}/pages/{pageNumber}", s.bookPage)
		r.Get("/books/{id}/thumbnail", s.bookThumbnail)
		r.Get("/books/{id}/file", s.bookFile)
		r.Get("/books/{id}/read-progress", s.komgaBookReadProgress)
		r.Patch("/books/{id}/read-progress", s.patchKomgaBookReadProgress)
		r.Delete("/books/{id}/read-progress", s.deleteKomgaBookReadProgress)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) emptyList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, []any{})
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	// API clients: return JSON, browsers: return HTML
	if strings.Contains(r.Header.Get("Accept"), "text/html") || r.URL.Query().Has("html") {
		data, err := adminHTML.ReadFile("admin.html")
		if err != nil {
			http.Error(w, "admin page not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
		return
	}
	writeJSON(w, 200, map[string]any{
		"name":        "fake-komga-local",
		"version":     "1.2",
		"apiVersion":  "v1",
		"url":         "/api/v1",
		"seriesCount": 0,
	})
}

func (s *Server) handleScanRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.db.GetScanRuns(r.Context(), 10)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, runs)
}

func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	run, err := s.db.GetLastScanRun(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if run == nil {
		writeJSON(w, 200, map[string]any{"status": "never_scanned"})
		return
	}
	writeJSON(w, 200, run)
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	go s.scan.Scan(r.Context())
	writeJSON(w, 202, map[string]any{"status": "started"})
}

func (s *Server) handleCovers(w http.ResponseWriter, r *http.Request) {
	go func() {
		ctx := context.Background()
		rows, err := s.db.DB().Query(
			`SELECT s.id, b.path FROM series s
			 LEFT JOIN books b ON b.id = (SELECT id FROM books WHERE series_id = s.id LIMIT 1)
			 WHERE s.id NOT IN (SELECT series_id FROM series_thumbnails)`)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var sid, path string
			rows.Scan(&sid, &path)
			if path != "" {
				s.thumb.Generate(ctx, path, sid)
			}
		}
	}()
	writeJSON(w, 202, map[string]any{"status": "started"})
}

func (s *Server) handleCoverDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SeriesID string `json:"seriesId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SeriesID == "" {
		writeJSON(w, 400, map[string]any{"error": "seriesId required"})
		return
	}
	s.thumb.Delete(r.Context(), req.SeriesID)
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

// --- Libraries ---

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.DB().Query("SELECT id, name, root FROM libraries")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	list := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, root string
		rows.Scan(&id, &name, &root)
		list = append(list, map[string]any{
			"id": id, "name": name, "root": root, "seriesCount": 0,
		})
	}
	writeJSON(w, 200, list)
}

func (s *Server) librarySeries(w http.ResponseWriter, r *http.Request) {
	libID := chi.URLParam(r, "id")
	rows, err := s.db.DB().Query("SELECT id, name FROM series WHERE library_id=?", libID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	list := make([]map[string]any, 0)
	for rows.Next() {
		var id, name string
		rows.Scan(&id, &name)
		list = append(list, s.seriesDTO(id, name))
	}
	writeJSON(w, 200, map[string]any{
		"content": list, "totalElements": len(list),
		"empty": len(list) == 0, "first": true, "last": true,
		"number": 0, "numberOfElements": len(list),
		"size": len(list), "totalPages": 1,
	})
}

// --- Search / 分页 辅助函数 (仿 fake-komga-115) ---

func intQuery(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func listQuery(r *http.Request, key string) []string {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

func boolQuery(r *http.Request, key string) *bool {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil
	}
	switch v {
	case "true", "1":
		b := true
		return &b
	default:
		b := false
		return &b
	}
}

func sortDir(sortValue string) string {
	if strings.Contains(sortValue, ",desc") {
		return "DESC"
	}
	return "ASC"
}

func makePage[T any](content []T, page, size int, total int64, unpaged bool) map[string]any {
	totalPages := 0
	if size > 0 {
		totalPages = int((total + int64(size) - 1) / int64(size))
	}
	return map[string]any{
		"content":          content,
		"totalElements":    total,
		"totalPages":       totalPages,
		"size":             size,
		"number":           page,
		"numberOfElements": len(content),
		"first":            page == 0,
		"last":             page >= totalPages-1 || totalPages <= 1,
		"empty":            len(content) == 0,
	}
}

// --- Series ---

func (s *Server) listSeries(w http.ResponseWriter, r *http.Request) {
	// 完整复刻 fake-komga-115 搜索处理逻辑
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	libraryIDs := listQuery(r, "library_id")
	page := intQuery(r, "page", 0)
	size := intQuery(r, "size", 20)
	sort := r.URL.Query().Get("sort")
	readStatuses := listQuery(r, "read_status")

	// deleted=true → 空结果 (仿 fake-komga-115)
	if r.URL.Query().Get("deleted") == "true" {
		writeJSON(w, 200, makePage([]map[string]any{}, page, size, 0, false))
		return
	}

	// Build WHERE (仿 fake-komga-115 buildFilters)
	var clauses []string
	var args []any

	// Search: s.name LIKE ? ESCAPE '\' COLLATE NOCASE
	if search != "" {
		clauses = append(clauses, `s.name LIKE ? ESCAPE '\' COLLATE NOCASE`)
		args = append(args, "%"+escapeLike(search)+"%")
	}

	// Library filter: s.library_id IN (...)
	if len(libraryIDs) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(libraryIDs)), ",")
		clauses = append(clauses, "s.library_id IN ("+placeholders+")")
		for _, lid := range libraryIDs {
			args = append(args, lid)
		}
	}

	// Read status filter (仿 fake-komga-115 addReadStatusFilter)
	if len(readStatuses) > 0 {
		var statusClauses []string
		for _, rs := range readStatuses {
			switch strings.ToLower(strings.TrimSpace(rs)) {
			case "read":
				statusClauses = append(statusClauses, `EXISTS (SELECT 1 FROM book_read_progress brp WHERE brp.series_id=s.id AND brp.completed=1)`)
			case "unread":
				statusClauses = append(statusClauses, `NOT EXISTS (SELECT 1 FROM book_read_progress brp WHERE brp.series_id=s.id)`)
			case "in_progress":
				statusClauses = append(statusClauses, `EXISTS (SELECT 1 FROM book_read_progress brp WHERE brp.series_id=s.id AND brp.completed=0)`)
			}
		}
		if len(statusClauses) > 0 {
			clauses = append(clauses, "("+strings.Join(statusClauses, " OR ")+")")
		}
	}

	// Tag/Genre/Publisher/Author 过滤 (使用 INSTR 避免 LIKE 参数拼接问题)
	if tags := listQuery(r, "tag"); len(tags) > 0 {
		var tagClauses []string
		for _, t := range tags {
			tagClauses = append(tagClauses, `EXISTS (SELECT 1 FROM bangumi_series_meta bm WHERE bm.series_id=s.id AND instr(bm.tags_json,?)>0)`)
			args = append(args, t)
		}
		clauses = append(clauses, "("+strings.Join(tagClauses, " AND ")+")")
	}
	if genres := listQuery(r, "genre"); len(genres) > 0 {
		var genreClauses []string
		for _, g := range genres {
			genreClauses = append(genreClauses, `EXISTS (SELECT 1 FROM bangumi_series_meta bm WHERE bm.series_id=s.id AND instr(bm.tags_json,?)>0)`)
			args = append(args, g)
		}
		clauses = append(clauses, "("+strings.Join(genreClauses, " AND ")+")")
	}
	if publishers := listQuery(r, "publisher"); len(publishers) > 0 {
		var pubClauses []string
		for _, p := range publishers {
			pubClauses = append(pubClauses, `EXISTS (SELECT 1 FROM bangumi_series_meta bm WHERE bm.series_id=s.id AND instr(bm.publisher,?)>0)`)
			args = append(args, p)
		}
		clauses = append(clauses, "("+strings.Join(pubClauses, " AND ")+")")
	}
	if authors := listQuery(r, "author"); len(authors) > 0 {
		var authClauses []string
		for _, a := range authors {
			authClauses = append(authClauses, `EXISTS (SELECT 1 FROM bangumi_series_meta bm WHERE bm.series_id=s.id AND instr(bm.authors_json,?)>0)`)
			args = append(args, a)
		}
		clauses = append(clauses, "("+strings.Join(authClauses, " AND ")+")")
	}

	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}

	// Count total
	var total int64
	s.db.DB().QueryRow("SELECT COUNT(*) FROM series s"+where, args...).Scan(&total)

	// Sort (仿 fake-komga-115)
	order := "s.name COLLATE NOCASE ASC"
	sortValue := strings.ToLower(strings.TrimSpace(sort))
	switch {
	case strings.Contains(sortValue, "random"):
		order = "RANDOM()"
	case strings.Contains(sortValue, "createddate"):
		order = "s.created_at " + sortDir(sortValue) + ",s.name COLLATE NOCASE ASC"
	case strings.Contains(sortValue, "lastmodifieddate"):
		order = "s.updated_at " + sortDir(sortValue) + ",s.name COLLATE NOCASE ASC"
	case strings.Contains(sortValue, ",desc"):
		order = "s.name COLLATE NOCASE DESC"
	}

	// 分页查询
	query := "SELECT s.id, s.name FROM series s" + where + " ORDER BY " + order + " LIMIT ? OFFSET ?"
	queryArgs := append(args, size, page*size)
	rows, err := s.db.DB().Query(query, queryArgs...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	list := make([]map[string]any, 0)
	for rows.Next() {
		var id, name string
		rows.Scan(&id, &name)
		list = append(list, s.seriesDTO(id, name))
	}

	writeJSON(w, 200, makePage(list, page, size, total, false))
}

func (s *Server) getSeries(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row := s.db.DB().QueryRow("SELECT id, name, library_id, path FROM series WHERE id=?", id)
	var sid, name, libID, path string
	if err := row.Scan(&sid, &name, &libID, &path); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	dto := s.seriesDTO(sid, name)
	dto["libraryId"] = libID
	dto["path"] = path
	writeJSON(w, 200, dto)
}

func (s *Server) seriesDTO(id, name string) map[string]any {
	var bookCount int
	s.db.DB().QueryRow("SELECT COUNT(*) FROM books WHERE series_id=?", id).Scan(&bookCount)
	// 阅读进度聚合（仿 fake-komga-115）
	progress, _ := s.db.SeriesReadProgress(context.Background(), id)
	now := time.Now().UTC().Format(time.RFC3339)
	dto := map[string]any{
		"id":                   id,
		"name":                 name,
		"libraryId":            "",
		"url":                  "/api/v1/series/" + id,
		"booksCount":           progress.BooksCount,
		"booksReadCount":       progress.BooksReadCount,
		"booksInProgressCount": progress.BooksInProgressCount,
		"booksUnreadCount":     progress.BooksUnreadCount,
		"booksMetadata": map[string]any{
			"authors":        []any{},
			"releaseDate":    nil,
			"summary":        "",
			"summaryNumber":  "",
			"created":        now,
			"lastModified":   now,
			"tags":           []string{},
		},
		"deleted":              false,
		"fileLastModified":     now,
		"oneshot":              false,
		"created":              now,
		"lastModified":         now,
		"fk115CoverUrl":        "/api/v1/series/" + id + "/thumbnail",
		"metadata": map[string]any{
			"title":           name,
			"titleLock":       false,
			"summary":         "",
			"summaryLock":     false,
			"publisher":       "",
			"publisherLock":   false,
			"ageRating":       nil,
			"ageRatingLock":   false,
			"language":        "zh",
			"languageLock":    false,
			"genres":          []string{},
			"genresLock":      false,
			"status":          "ONGOING",
			"titleSort":      name,
			"readingDirection": "LEFT_TO_RIGHT",
			"readingDirectionLock": false,
			"statusLock":      false,
			"tags":            []string{},
			"tagsLock":        false,
			"authors":         []any{},
			"authorsLock":     false,
			"links":           []any{},
			"linksLock":       false,
			"alternateTitles": []any{},
			"alternateTitlesLock": false,
			"totalBookCount":  nil,
			"created":         time.Now().UTC().Format(time.RFC3339),
			"lastModified":    time.Now().UTC().Format(time.RFC3339),
		},
	}
	meta, err := s.db.GetSeriesMeta(id)
	if err == nil && meta != nil {
		// booksMetadata.authors 填作者（Komikku 从这读，series.metadata.authors 官方为 null）
		if bm, ok := dto["booksMetadata"].(map[string]any); ok && len(meta.Authors) > 0 {
			bm["authors"] = meta.Authors
		}
		// status 空时默认 ONGOING（官方 Komga 行为：连载中）
		metaStatus := meta.Status
		if metaStatus == "" {
			metaStatus = "ONGOING"
		}
		dto["metadata"] = map[string]any{
			"title":           meta.TitleCN,
			"titleLock":       true,
			"summary":         meta.Summary,
			"summaryLock":     false,
			"publisher":       meta.Publisher,
			"publisherLock":   false,
			"ageRating":       nil,
			"ageRatingLock":   false,
			"language":        "zh",
			"languageLock":    false,
			"genres":          []string{},
			"genresLock":      false,
			"status":          metaStatus,
			"titleSort":      meta.TitleCN,
			"readingDirection": "LEFT_TO_RIGHT",
			"readingDirectionLock": false,
			"statusLock":      false,
			"tags":            meta.Tags,
			"tagsLock":        false,
			"totalBookCount":  meta.TotalVolumes,
			"rating":          meta.Rating,
			"authors":         meta.Authors,
			"authorsLock":     false,
			"links":           []map[string]any{{"label": "bangumi", "url": fmt.Sprintf("https://bangumi.tv/subject/%d", meta.BangumiID)}},
			"linksLock":       false,
			"alternateTitles": []any{},
			"alternateTitlesLock": false,
			"created":         time.Now().UTC().Format(time.RFC3339),
			"lastModified":    time.Now().UTC().Format(time.RFC3339),
		}
		dto["coverUrl"] = meta.CoverURL
	}
	return dto
}

func (s *Server) seriesThumbnail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	path := s.thumb.ThumbnailPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		row := s.db.DB().QueryRow("SELECT path FROM books WHERE series_id=? LIMIT 1", id)
		var bookPath string
		if err := row.Scan(&bookPath); err != nil {
			http.Error(w, "no thumbnail", 404)
			return
		}
		if err := s.thumb.Generate(r.Context(), bookPath, id); err != nil {
			http.Error(w, "generate failed: "+err.Error(), 500)
			return
		}
		data, _ = os.ReadFile(path)
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

// volumeRe 匹配文件名中的 Vol.NN（如 "刃牙道 Vol.11.cbz" -> 11）
var volumeRe = regexp.MustCompile(`(?i)\bvol\.?\s*(\d+)`)

// bookNumber 从文件名解析卷号；解析失败时回退到文件顺序 index+1
func bookNumber(name string, index int) (string, float64) {
	m := volumeRe.FindStringSubmatch(name)
	if len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return strconv.Itoa(n), float64(n)
		}
	}
	n := index + 1
	return strconv.Itoa(n), float64(n)
}

// formatBytes 把字节数格式化为可读大小（如 129360672 -> "123.3 MB"）
func formatBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	size := float64(value)
	for _, unit := range units {
		size /= 1024
		if size < 1024 || unit == "TB" {
			return fmt.Sprintf("%.1f %s", math.Round(size*10)/10, unit)
		}
	}
	return fmt.Sprintf("%d B", value)
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

// --- Books ---

// listBooks 支持 Komikku 书籍搜索: /api/v1/books?search=<query>
func (s *Server) listBooks(w http.ResponseWriter, r *http.Request) {
	// 完整复刻 fake-komga-115 书籍搜索处理逻辑
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	libraryIDs := listQuery(r, "library_id")
	page := intQuery(r, "page", 0)
	size := intQuery(r, "size", 20)
	sort := r.URL.Query().Get("sort")
	readStatuses := listQuery(r, "read_status")

	// deleted=true → 空结果 (仿 fake-komga-115)
	if r.URL.Query().Get("deleted") == "true" {
		writeJSON(w, 200, makePage([]map[string]any{}, page, size, 0, false))
		return
	}

	// Build WHERE (仿 fake-komga-115 buildFilters)
	var clauses []string
	var args []any

	// Search
	if search != "" {
		clauses = append(clauses, `(b.name LIKE ? ESCAPE '\' COLLATE NOCASE OR s.name LIKE ? ESCAPE '\' COLLATE NOCASE)`)
		like := "%" + escapeLike(search) + "%"
		args = append(args, like, like)
	}

	// Library filter
	if len(libraryIDs) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(libraryIDs)), ",")
		clauses = append(clauses, "s.library_id IN ("+placeholders+")")
		for _, lid := range libraryIDs {
			args = append(args, lid)
		}
	}

	// Read status filter
	if len(readStatuses) > 0 {
		var statusClauses []string
		for _, rs := range readStatuses {
			switch strings.ToLower(strings.TrimSpace(rs)) {
			case "read":
				statusClauses = append(statusClauses, `EXISTS (SELECT 1 FROM book_read_progress brp WHERE brp.book_id=b.id AND brp.completed=1)`)
			case "unread":
				statusClauses = append(statusClauses, `NOT EXISTS (SELECT 1 FROM book_read_progress brp WHERE brp.book_id=b.id)`)
			case "in_progress":
				statusClauses = append(statusClauses, `EXISTS (SELECT 1 FROM book_read_progress brp WHERE brp.book_id=b.id AND brp.completed=0)`)
			}
		}
		if len(statusClauses) > 0 {
			clauses = append(clauses, "("+strings.Join(statusClauses, " OR ")+")")
		}
	}

	// Tag/Genre/Publisher/Author 过滤 (使用 INSTR 避免 LIKE 参数拼接问题)
	if tags := listQuery(r, "tag"); len(tags) > 0 {
		var tagClauses []string
		for _, t := range tags {
			tagClauses = append(tagClauses, `EXISTS (SELECT 1 FROM bangumi_series_meta bm WHERE bm.series_id=s.id AND instr(bm.tags_json,?)>0)`)
			args = append(args, t)
		}
		clauses = append(clauses, "("+strings.Join(tagClauses, " AND ")+")")
	}
	if genres := listQuery(r, "genre"); len(genres) > 0 {
		var genreClauses []string
		for _, g := range genres {
			genreClauses = append(genreClauses, `EXISTS (SELECT 1 FROM bangumi_series_meta bm WHERE bm.series_id=s.id AND instr(bm.tags_json,?)>0)`)
			args = append(args, g)
		}
		clauses = append(clauses, "("+strings.Join(genreClauses, " AND ")+")")
	}
	if publishers := listQuery(r, "publisher"); len(publishers) > 0 {
		var pubClauses []string
		for _, p := range publishers {
			pubClauses = append(pubClauses, `EXISTS (SELECT 1 FROM bangumi_series_meta bm WHERE bm.series_id=s.id AND instr(bm.publisher,?)>0)`)
			args = append(args, p)
		}
		clauses = append(clauses, "("+strings.Join(pubClauses, " AND ")+")")
	}
	if authors := listQuery(r, "author"); len(authors) > 0 {
		var authClauses []string
		for _, a := range authors {
			authClauses = append(authClauses, `EXISTS (SELECT 1 FROM bangumi_series_meta bm WHERE bm.series_id=s.id AND instr(bm.authors_json,?)>0)`)
			args = append(args, a)
		}
		clauses = append(clauses, "("+strings.Join(authClauses, " AND ")+")")
	}

	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}

	// Count total
	var total int64
	s.db.DB().QueryRow("SELECT COUNT(*) FROM books b JOIN series s ON b.series_id=s.id"+where, args...).Scan(&total)

	// Sort
	order := "b.name COLLATE NOCASE ASC"
	sortValue := strings.ToLower(strings.TrimSpace(sort))
	switch {
	case strings.Contains(sortValue, "random"):
		order = "RANDOM()"
	case strings.Contains(sortValue, "createddate"):
		order = "b.created_at " + sortDir(sortValue) + ",b.name COLLATE NOCASE ASC"
	case strings.Contains(sortValue, "lastmodifieddate"):
		order = "b.updated_at " + sortDir(sortValue) + ",b.name COLLATE NOCASE ASC"
	case strings.Contains(sortValue, "metadata.titleSort"):
		order = "b.name " + sortDir(sortValue) + ",b.name COLLATE NOCASE ASC"
	case strings.Contains(sortValue, ",desc"):
		order = "b.name COLLATE NOCASE DESC"
	}

	// 分页查询
	q := `SELECT b.id, b.name, b.path, b.size, b.series_id, s.name
		FROM books b JOIN series s ON b.series_id = s.id` + where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	queryArgs := append(args, size, page*size)
	rows, err := s.db.DB().Query(q, queryArgs...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	type bookRow struct {
		id         string
		name       string
		path       string
		size       int64
		seriesID   string
		seriesName string
	}
	var books []bookRow
	for rows.Next() {
		var b bookRow
		rows.Scan(&b.id, &b.name, &b.path, &b.size, &b.seriesID, &b.seriesName)
		books = append(books, b)
	}
	ids := make([]string, len(books))
	for i, b := range books {
		ids[i] = b.id
	}
	progresses, _ := s.db.BookReadProgresses(r.Context(), ids)
	list := make([]map[string]any, 0)
	for index, b := range books {
		now := time.Now().UTC().Format(time.RFC3339)
		numStr, numSort := bookNumber(b.name, index)
		var rp any
		if p, ok := progresses[b.id]; ok {
			rp = bookReadProgressDTO(p)
		}
		list = append(list, map[string]any{
			"id": b.id, "name": b.name, "seriesId": b.seriesID,
			"seriesTitle": b.seriesName, "number": numSort, "libraryId": "",
			"size": formatBytes(b.size), "sizeBytes": b.size,
			"media": map[string]any{
				"status": "READY",
				"mediaType": "application/zip",
				"pagesCount": 0,
				"mediaProfile": "DIVINA",
				"epubDivinaCompatible": false,
			},
			"fileHash": "", "fileLastModified": now,
			"deleted": false, "oneshot": false,
			"created": now, "lastModified": now,
			"url": "/api/v1/books/" + b.id,
			"readProgress": rp,
			"metadata": map[string]any{
				"title": b.name, "titleLock": false,
				"summary": "", "summaryLock": false,
				"number": numStr, "numberLock": false, "numberSort": numSort, "numberSortLock": false,
				"releaseDate": nil, "releaseDateLock": false,
				"authors": []any{}, "authorsLock": false,
				"tags": []string{}, "tagsLock": false,
				"isbn": "", "isbnLock": false,
				"links": []any{}, "linksLock": false,
				"created": now, "lastModified": now,
			},
		})
	}
	writeJSON(w, 200, makePage(list, page, size, total, false))
}

func (s *Server) seriesBooks(w http.ResponseWriter, r *http.Request) {
	seriesID := chi.URLParam(r, "id")
	rows, err := s.db.DB().Query("SELECT id, name, path, size FROM books WHERE series_id=? ORDER BY name", seriesID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	type bookRow struct {
		id   string
		name string
		path string
		size int64
	}
	var books []bookRow
	for rows.Next() {
		var b bookRow
		rows.Scan(&b.id, &b.name, &b.path, &b.size)
		books = append(books, b)
	}
	// 批量查进度
	ids := make([]string, len(books))
	for i, b := range books {
		ids[i] = b.id
	}
	progresses, _ := s.db.BookReadProgresses(r.Context(), ids)

	list := make([]map[string]any, 0)
	for index, b := range books {
		now := time.Now().UTC().Format(time.RFC3339)
		numStr, numSort := bookNumber(b.name, index)
		var rp any
		if p, ok := progresses[b.id]; ok {
			rp = bookReadProgressDTO(p)
		}
		list = append(list, map[string]any{
			"id": b.id, "name": b.name, "seriesId": seriesID,
			"seriesTitle": "", "number": numSort, "libraryId": "",
			"size": formatBytes(b.size), "sizeBytes": b.size,
			"media": map[string]any{
			"status": "READY",
			"mediaType": "application/zip",
			"pagesCount": 0,
			"mediaProfile": "DIVINA",
			"epubDivinaCompatible": false,
		},
			"fileHash": "", "fileLastModified": now,
			"deleted": false, "oneshot": false,
			"created": now, "lastModified": now,
			"url": "/api/v1/books/" + b.id,
			"readProgress": rp,
			"metadata": map[string]any{
				"title": b.name, "titleLock": false,
				"summary": "", "summaryLock": false,
				"number": numStr, "numberLock": false, "numberSort": numSort, "numberSortLock": false,
				"releaseDate": nil, "releaseDateLock": false,
				"authors": []any{}, "authorsLock": false,
				"tags": []string{}, "tagsLock": false,
				"isbn": "", "isbnLock": false,
				"links": []any{}, "linksLock": false,
				"created": now, "lastModified": now,
			},
		})
	}
	writeJSON(w, 200, map[string]any{
		"content": list, "totalElements": len(list),
		"empty": len(list) == 0, "first": true, "last": true,
		"number": 0, "numberOfElements": len(list),
		"size": len(list), "totalPages": 1,
	})
}

func (s *Server) getBook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row := s.db.DB().QueryRow("SELECT id, series_id, name, path, size FROM books WHERE id=?", id)
	var bid, seriesID, name, path string
	var size int64
	if err := row.Scan(&bid, &seriesID, &name, &path, &size); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	numStr, numSort := bookNumber(name, 0)
	var rp any
	if p, ok, err := s.db.BookReadProgress(r.Context(), bid); err == nil && ok {
		rp = bookReadProgressDTO(p)
	}
	writeJSON(w, 200, map[string]any{
		"id": bid, "seriesId": seriesID, "name": name,
		"seriesTitle": "", "number": numSort, "libraryId": "",
		"size": formatBytes(size), "sizeBytes": size,
		"media": map[string]any{
			"status": "READY",
			"mediaType": "application/zip",
			"pagesCount": 0,
			"mediaProfile": "DIVINA",
			"epubDivinaCompatible": false,
		},
		"fileHash": "", "fileLastModified": now,
		"deleted": false, "oneshot": false,
		"created": now, "lastModified": now,
		"url": "/api/v1/books/" + bid,
		"readProgress": rp,
		"metadata": map[string]any{
				"title": name, "titleLock": false,
				"summary": "", "summaryLock": false,
				"number": numStr, "numberLock": false, "numberSort": numSort, "numberSortLock": false,
				"releaseDate": nil, "releaseDateLock": false,
				"authors": []any{}, "authorsLock": false,
				"tags": []string{}, "tagsLock": false,
				"isbn": "", "isbnLock": false,
				"links": []any{}, "linksLock": false,
				"created": now, "lastModified": now,
			},
	})
}

func (s *Server) bookPages(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row := s.db.DB().QueryRow("SELECT path FROM books WHERE id=?", id)
	var path string
	if err := row.Scan(&path); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	ar, err := archive.Open(path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer ar.Close()
	count, err := ar.PageCount(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Komga returns plain list, not wrapped in content/totalElements
	var pages []map[string]any
	for i := 0; i < count; i++ {
		pages = append(pages, map[string]any{
			"number":     i + 1,
			"fileName":   fmt.Sprintf("%03d.jpg", i+1),
			"mediaType":  "image/jpeg",
			"size":       "0 MB",
			"sizeBytes":  0,
			"width":      nil,
			"height":     nil,
		})
	}
	writeJSON(w, 200, pages)
}

func (s *Server) bookPage(w http.ResponseWriter, r *http.Request) {
	bookID := chi.URLParam(r, "id")
	pageNum, _ := strconv.Atoi(chi.URLParam(r, "pageNumber"))
	row := s.db.DB().QueryRow("SELECT path FROM books WHERE id=?", bookID)
	var path string
	if err := row.Scan(&path); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	ar, err := archive.Open(path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer ar.Close()
	data, err := ar.Page(r.Context(), pageNum-1)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	contentType := "image/jpeg"
	if strings.HasSuffix(path, ".png") {
		contentType = "image/png"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

func (s *Server) bookFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row := s.db.DB().QueryRow("SELECT path FROM books WHERE id=?", id)
	var path string
	if err := row.Scan(&path); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	http.ServeFile(w, r, path)
}

func (s *Server) bookThumbnail(w http.ResponseWriter, r *http.Request) {
	bookID := chi.URLParam(r, "id")
	row := s.db.DB().QueryRow("SELECT path FROM books WHERE id=?", bookID)
	var path string
	if err := row.Scan(&path); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	ar, err := archive.Open(path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer ar.Close()
	data, err := ar.Page(r.Context(), 0)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

// --- 阅读进度 (仿 fake-komga-115 / 官方 Komga) ---

func (s *Server) komgaBookReadProgress(w http.ResponseWriter, r *http.Request) {
	bookID := chi.URLParam(r, "id")
	var seriesID string
	if err := s.db.DB().QueryRow("SELECT series_id FROM books WHERE id=?", bookID).Scan(&seriesID); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	progress, ok, err := s.db.BookReadProgress(r.Context(), bookID)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, 200, nil)
		return
	}
	writeJSON(w, 200, bookReadProgressDTO(progress))
}

func (s *Server) deleteKomgaBookReadProgress(w http.ResponseWriter, r *http.Request) {
	bookID := chi.URLParam(r, "id")
	var seriesID string
	if err := s.db.DB().QueryRow("SELECT series_id FROM books WHERE id=?", bookID).Scan(&seriesID); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if err := s.db.DeleteBookReadProgress(r.Context(), bookID); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) patchKomgaBookReadProgress(w http.ResponseWriter, r *http.Request) {
	bookID := chi.URLParam(r, "id")
	var seriesID string
	if err := s.db.DB().QueryRow("SELECT series_id FROM books WHERE id=?", bookID).Scan(&seriesID); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	var request struct {
		Completed *bool `json:"completed"`
		Page      *int  `json:"page"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid request"})
		return
	}
	if request.Completed == nil && request.Page == nil {
		writeJSON(w, 400, map[string]any{"error": "completed or page is required."})
		return
	}
	if request.Page != nil && *request.Page < 1 {
		writeJSON(w, 400, map[string]any{"error": "page must be greater than zero."})
		return
	}
	if err := s.db.UpdateBookReadProgress(r.Context(), bookID, seriesID, request.Completed, request.Page); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func bookReadProgressDTO(progress database.BookReadProgress) map[string]any {
	var page any
	if progress.Page != nil {
		page = *progress.Page
	}
	var readDate any
	if progress.ReadDate != nil {
		readDate = progress.ReadDate.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"completed": progress.Completed,
		"page":      page,
		"readDate":  readDate,
	}
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	var dirs []string
	raw, _ := s.db.GetSetting("comics_dirs")
	if raw == "" {
		// First run - use CLI default
		dirs = []string{s.comicsDir}
	} else {
		json.Unmarshal([]byte(raw), &dirs)
	}
	writeJSON(w, 200, map[string]any{"comicsDirs": dirs})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ComicsDirs []string `json:"comicsDirs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid"})
		return
	}
	data, _ := json.Marshal(req.ComicsDirs)
	s.db.SetSetting("comics_dirs", string(data))
	s.scan.SetDirs(req.ComicsDirs)
	writeJSON(w, 200, map[string]any{"status": "ok"})
}
