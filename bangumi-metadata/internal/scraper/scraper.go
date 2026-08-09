package scraper

import (
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/user/bangumi-metadata/internal/bangumi"
	"github.com/user/bangumi-metadata/internal/database"
)

// Scraper handles the metadata scraping workflow.
type Scraper struct {
	bangumi    *bangumi.Client
	db         *database.DB
	accessToken string
	batchKeywordRegex string
	re *regexp.Regexp
	skipCover  bool
}

// New creates a new Scraper.
// ScrapeAll scrapes metadata for all series that don't have it yet.
func New(bangumiClient *bangumi.Client, db *database.DB, accessToken string, skipCover bool, batchKeywordRegex string) *Scraper {
	s := &Scraper{
		bangumi:     bangumiClient,
		db:          db,
		accessToken: accessToken,
		skipCover:   skipCover,
	}
	if batchKeywordRegex != "" {
		s.re, _ = regexp.Compile(batchKeywordRegex)
	}
	return s
}
func (s *Scraper) SetSkipCover(skip bool) {
	s.skipCover = skip
}

// SetBatchRegex 动态更新批量匹配关键词提取正则（无需重启）
func (s *Scraper) SetBatchRegex(re string) {
	s.batchKeywordRegex = re
	if re == "" {
		s.re = nil
		return
	}
	s.re, _ = regexp.Compile(re)
}

// searchNameFor 根据当前正则从系列名提取搜索关键词
func (s *Scraper) searchNameFor(ser database.SeriesInfo) string {
	searchName := bangumi.CleanFolderName(ser.Name)
	if s.re != nil {
		m := s.re.FindStringSubmatch(ser.Name)
		if len(m) > 1 {
			searchName = m[1]
		} else if len(m) == 1 {
			searchName = m[0]
		}
	}
	return searchName
}

// scrapeOne 刮削单个系列：搜索→取第一个漫画匹配→保存元数据
func (s *Scraper) scrapeOne(ser database.SeriesInfo) (bool, error) {
	searchName := s.searchNameFor(ser)
	searchResults, err := s.bangumi.SearchSubjects(searchName, bangumi.SubjectBook, 8)
	if err != nil {
		return false, fmt.Errorf("search failed: %w", err)
	}

	var matched []bangumi.SearchResult
	for _, sr := range searchResults {
		if sr.Platform == "漫画" || sr.Platform == "" {
			matched = append(matched, sr)
		}
	}
	if len(matched) == 0 {
		return false, nil
	}

	bestMatch := matched[0]
	subject, err := s.bangumi.GetSubject(bestMatch.ID)
	if err != nil {
		return false, fmt.Errorf("get subject %d failed: %w", bestMatch.ID, err)
	}

	meta := s.buildSeriesMeta(ser, subject, bestMatch)
	if err := s.db.SaveSeriesMeta(meta); err != nil {
		return false, fmt.Errorf("save metadata failed: %w", err)
	}

	volumes, err := s.scrapeVolumeMeta(ser.ID, subject)
	if err != nil {
		log.Printf("  Warning: volume scrape failed: %v", err)
	}
	_ = volumes
	return true, nil
}

// ScrapeSelected 批量刮削指定系列（已选择的）
func (s *Scraper) ScrapeSelected(seriesIDs []string, forceRefresh bool) (*ScrapeResult, error) {
	result := &ScrapeResult{
		Total:   len(seriesIDs),
		Matched: 0,
		Failed:  0,
		Skipped: 0,
		Errors:  make([]string, 0),
	}
	for i, id := range seriesIDs {
		ser, err := s.db.GetSeriesByID(id)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			result.Failed++
			continue
		}
		log.Printf("[%d/%d] Scraping selected: %s", i+1, len(seriesIDs), ser.Name)
		ok, err := s.scrapeOne(*ser)
		if err != nil {
			errMsg := fmt.Sprintf("%s: %v", ser.Name, err)
			log.Printf("  ERROR: %s", errMsg)
			result.Errors = append(result.Errors, errMsg)
			result.Failed++
		} else if !ok {
			log.Printf("  No match found for: %s", ser.Name)
			s.db.MarkFailed(ser.ID, "未找到匹配")
			result.Skipped++
		} else {
			result.Matched++
		}
		if i < len(seriesIDs)-1 {
			time.Sleep(1 * time.Second)
		}
	}
	return result, nil
}

func (s *Scraper) ScrapeAll(forceRefresh bool) (*ScrapeResult, error) {
	series, err := s.db.GetSeriesList(forceRefresh)
	if err != nil {
		return nil, fmt.Errorf("get series list: %w", err)
	}

	result := &ScrapeResult{
		Total:   len(series),
		Matched: 0,
		Failed:  0,
		Skipped: 0,
		Errors:  make([]string, 0),
	}

	for i, ser := range series {
		log.Printf("[%d/%d] Scraping: %s (%s)", i+1, len(series), ser.Name, ser.LibraryName)
		ok, err := s.scrapeOne(ser)
		if err != nil {
			errMsg := fmt.Sprintf("%s: %v", ser.Name, err)
			log.Printf("  ERROR: %s", errMsg)
			result.Errors = append(result.Errors, errMsg)
			result.Failed++
		} else if !ok {
			log.Printf("  No match found for: %s", ser.Name)
			s.db.MarkFailed(ser.ID, "未找到匹配")
			result.Skipped++
		} else {
			result.Matched++
		}
		if i < len(series)-1 {
			time.Sleep(1 * time.Second)
		}
	}

	return result, nil
}

func (s *Scraper) buildSeriesMeta(ser database.SeriesInfo, subject *bangumi.Subject, searchResult bangumi.SearchResult) *database.SeriesMeta {
	meta := &database.SeriesMeta{
		SeriesID:   ser.ID,
		BangumiID:  subject.ID,
		TitleCN:    subject.NameCN,
		TitleJP:    subject.Name,
		Summary:    subject.Summary,
		Platform:   subject.Platform,
		Rating:     0,
		RatingCount: 0,
		Tags:       make([]string, 0),
		Authors:    make([]database.Author, 0),
		UpdatedAt:  time.Now(),
	}

	// Clean summary
	meta.Summary = strings.TrimSpace(meta.Summary)
	meta.Summary = strings.ReplaceAll(meta.Summary, "\r\n", "\n")
	meta.Summary = strings.ReplaceAll(meta.Summary, "\r", "\n")

	// Rating
	if subject.Rating != nil {
		meta.Rating = math.Round(subject.Rating.Score*10) / 10
		meta.RatingCount = bangumi.GetRatingCount(subject.Rating)
	}

	// Total volumes
	if subject.Volumes > 0 {
		meta.TotalVolumes = subject.Volumes
	} else if subject.Eps > 0 {
		meta.TotalVolumes = subject.Eps
	}

	// Cover URL - skip if configured (use fake-komga-115 auto-generated thumbnails)
	if !s.skipCover {
		if subject.Images != nil && subject.Images.Large != "" {
			meta.CoverURL = subject.Images.Large
		} else if subject.Image != "" {
			meta.CoverURL = subject.Image
		} else if subject.Images != nil && subject.Images.Medium != "" {
			meta.CoverURL = subject.Images.Medium
		}
	}

	// Parse infobox for publisher, status, authors
	meta.Publisher = bangumi.ParseInfoboxValue(subject.Infobox, "出版社")
	if meta.Publisher == "" {
		meta.Publisher = bangumi.ParseInfoboxValue(subject.Infobox, "连载杂志")
	}
	if meta.Publisher == "" {
		meta.Publisher = bangumi.ParseInfoboxValue(subject.Infobox, "制作")
	}

	// Status
	statusVal := bangumi.ParseInfoboxValue(subject.Infobox, "状态")
	if statusVal == "" {
		statusVal = bangumi.ParseInfoboxValue(subject.Infobox, "连载状态")
	}
	if statusVal == "" {
		statusVal = bangumi.ParseInfoboxValue(subject.Infobox, "刊行状态")
	}
	meta.Status = mapStatus(statusVal)

	// If totalVolumes is set, mark as ENDED
	if meta.TotalVolumes > 0 && meta.Status == "" {
		meta.Status = "ENDED"
	}

	// Authors
	authorRoles := map[string]string{
		"作者": "writer", "原作": "writer", "漫画家": "writer", "脚本": "writer",
		"作画": "penciller", "插图": "illustrator", "人物原案": "conceptor",
		"原案": "story", "人物设定": "designer",
	}
	for key, role := range authorRoles {
		val := bangumi.ParseInfoboxValue(subject.Infobox, key)
		if val != "" {
			// Split by common separators
			names := splitAuthorNames(val)
			for _, name := range names {
				name = strings.TrimSpace(name)
				if name != "" && !hasAuthor(meta.Authors, name, role) {
					meta.Authors = append(meta.Authors, database.Author{Name: name, Role: role})
				}
			}
		}
	}

	// Ensure writer exists
	hasWriter := false
	hasPenciller := false
	for _, a := range meta.Authors {
		if a.Role == "writer" {
			hasWriter = true
		}
		if a.Role == "penciller" {
			hasPenciller = true
		}
	}
	if !hasWriter {
		for _, a := range meta.Authors {
			if a.Role == "penciller" {
				meta.Authors = append(meta.Authors, database.Author{Name: a.Name, Role: "writer"})
				break
			}
		}
	}
	if !hasPenciller && subject.Platform == "漫画" {
		for _, a := range meta.Authors {
			if a.Role == "writer" {
				meta.Authors = append(meta.Authors, database.Author{Name: a.Name, Role: "penciller"})
				break
			}
		}
	}

	// Tags from Bangumi tags - filter by whitelist
	knownTags := getKnownTags()
	for _, tag := range subject.Tags {
		tagName := tag.Name
		if !statusTags[tagName] && knownTags[tagName] {
			meta.Tags = append(meta.Tags, tagName)
		}
	}

	// Add rating as a tag (e.g. "7分")
	if meta.Rating > 0 {
		meta.Tags = append(meta.Tags, fmt.Sprintf("%d分", int(math.Round(meta.Rating))))
	}

	// Detect publisher/version info from folder name
	for _, kw := range publisherKeywords {
		if strings.Contains(ser.Name, kw) && !contains(meta.Tags, kw) {
			meta.Tags = append(meta.Tags, kw)
		}
	}
	// Also add rating tag if publisher was detected from folder but not from infobox
	if meta.Publisher == "" {
		for _, kw := range publisherKeywords {
			if strings.Contains(ser.Name, kw) {
				meta.Publisher = kw
				break
			}
		}
	}

	// Limit tags
	if len(meta.Tags) > 25 {
		meta.Tags = meta.Tags[:25]
	}

	return meta
}

func (s *Scraper) scrapeVolumeMeta(seriesID string, subject *bangumi.Subject) ([]database.BookMeta, error) {
	// Get related subjects (volumes)
	related, err := s.bangumi.GetSubjectSubjects(subject.ID)
	if err != nil {
		return nil, fmt.Errorf("get related subjects: %w", err)
	}

	// Filter for 单行本 only
	var volumes []bangumi.RelatedSubject
	for _, rel := range related {
		if rel.Relation == "单行本" {
			volumes = append(volumes, rel)
		}
	}

	if len(volumes) == 0 {
		return nil, nil
	}

	// Get the series books from fake-komga-115
	books, err := s.db.GetSeriesBooks(seriesID)
	if err != nil {
		return nil, fmt.Errorf("get series books: %w", err)
	}

	if len(books) == 0 {
		return nil, nil
	}

	// Match volumes to books by number
	volumeNumPattern := regexp.MustCompile(`(\d+)`)
	var bookMetas []database.BookMeta

	for _, vol := range volumes {
		volName := vol.NameCN
		if volName == "" {
			volName = vol.Name
		}

		// Extract volume number from the name
		numMatch := volumeNumPattern.FindStringSubmatch(volName)
		if numMatch == nil {
			continue
		}
		volNum, _ := strconv.Atoi(numMatch[1])

		// Find matching book
		for _, book := range books {
			bookNum := int(book.NumberSort)
			if bookNum == volNum || bookNum == 0 {
				// Match found
				meta := &database.BookMeta{
					BookID:       book.ID,
					SeriesID:     seriesID,
					VolumeNumber: volNum,
					UpdatedAt:    time.Now(),
				}

				// Get volume cover
				if vol.Images != nil && vol.Images.Large != "" {
					meta.CoverURL = vol.Images.Large
				} else if vol.Image != "" {
					meta.CoverURL = vol.Image
				}

				// Get detailed volume info if needed
				if meta.ISBN == "" || meta.ReleaseDate == "" {
					detail, err := s.bangumi.GetSubject(vol.ID)
					if err == nil {
						meta.ISBN = bangumi.ParseInfoboxValue(detail.Infobox, "ISBN")
						dateStr := bangumi.ParseInfoboxValue(detail.Infobox, "发售日")
						if dateStr == "" {
							dateStr = bangumi.ParseInfoboxValue(detail.Infobox, "放送开始")
						}
						meta.ReleaseDate = normalizeDate(dateStr)
						meta.Summary = detail.Summary
						if meta.CoverURL == "" {
							if detail.Images != nil && detail.Images.Large != "" {
								meta.CoverURL = detail.Images.Large
							} else if detail.Image != "" {
								meta.CoverURL = detail.Image
							}
						}
					}
				}

				if err := s.db.SaveBookMeta(meta); err != nil {
					log.Printf("  Warning: save book meta for vol %d: %v", volNum, err)
					continue
				}
				bookMetas = append(bookMetas, *meta)
				break
			}
		}
	}

	return bookMetas, nil
}

func mapStatus(val string) string {
	val = strings.ToLower(strings.TrimSpace(val))
	if strings.Contains(val, "休刊") || strings.Contains(val, "停刊") || strings.Contains(val, "停止连载") {
		return "HIATUS"
	}
	if strings.Contains(val, "连载中") || strings.Contains(val, "连载") {
		return "ONGOING"
	}
	if strings.Contains(val, "完结") || strings.Contains(val, "已完结") {
		return "ENDED"
	}
	return ""
}

func splitAuthorNames(val string) []string {
	// Remove content in brackets
	re := regexp.MustCompile(`[（(][^）)]*[）)]`)
	cleaned := re.ReplaceAllString(val, "")
	return strings.FieldsFunc(cleaned, func(r rune) bool {
		return r == '/' || r == '／' || r == '、' || r == '_' || r == '→' || r == '・' || r == '×' || r == '&' || r == '，' || r == ','
	})
}

func hasAuthor(authors []database.Author, name, role string) bool {
	for _, a := range authors {
		if a.Name == name && a.Role == role {
			return true
		}
	}
	return false
}

func normalizeDate(dateStr string) string {
	// Try to parse various date formats
	formats := []string{"2006-01-02", "2006-01", "2006年1月2日", "2006年01月02日", "2006年1月", "2006年01月"}
	for _, f := range formats {
		t, err := time.Parse(f, dateStr)
		if err == nil {
			return t.Format("2006-01-02")
		}
	}
	return dateStr
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func getKnownTags() map[string]bool {
	tagStr := "搞笑,欢乐,热血,运动,恋爱,后宫,校园,青年,少年,少女,青春,友情,治愈,战斗,魔法,科幻,冒险,推理,悬疑,侦探,竞技,体育,励志,职场,社会,史诗,历史,战争,机战,末世,奇幻,异界,轮回,穿越,重生,恐怖,短篇,反转,萌系,百合,日常,异世界,偶像,转生,伦理,黑暗,亲情,家庭,暴力,复仇,血腥,兄妹,生命,哲学,废土,致郁,性转,兄控,颜艺,感动,地下城,格斗,武术,美食,魔术,音乐,舞蹈,唱歌,绘画,摄影,童年,反套路,犯罪,超能力,游戏,梦想,怪物,摇滚,环保,猎奇,民俗,幽默,僵尸,动物,农业,生活,心理,生存,师生,卖肉,色气,轻改,机战,治愈,冒险,运动,竞技,推理,搞笑,少女,少年,青年,热血,战斗,魔法,奇幻,科幻,悬疑,恐怖,爱情,后宫,校园,日常,百合,萌系,治愈,音乐,舞蹈,美食,历史,战争,社会,职场,励志,青春,成长,友情,亲情,泪点,感动,温馨,欢乐,搞笑,讽刺,黑暗,致郁,哲学,神作,烂尾"
	tags := make(map[string]bool)
	for _, t := range strings.Split(tagStr, ",") {
		tags[strings.TrimSpace(t)] = true
	}
	return tags
}

// publisherKeywords detected from folder names
var publisherKeywords = []string{
	"台湾角川", "台湾东贩", "尖端", "青文", "东立", "长鸿", "尚禾", "大然", "龙成",
	"群英", "未来数位", "新视界", "玉皇朝", "天下", "传信", "天闻角川", "bili",
	"bilibili", "哔哩哔哩", "汉化", "生肉", "日版", "原版", "正版", "官方",
	"中文版", "简中", "繁中", "简体中文", "繁体中文", "简体", "繁体",
}

// statusTags that should be filtered out from regular tags
var statusTags = map[string]bool{
	"连载": true, "连载中": true, "完结": true, "已完结": true,
	"停刊": true, "长期休载": true, "停止连载": true, "休刊": true,
}

// ScrapeResult holds the result of a scraping run.
type ScrapeResult struct {
	Total   int      `json:"total"`
	Matched int      `json:"matched"`
	Failed  int      `json:"failed"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors,omitempty"`
}

// ScrapeSingle scrapes metadata for a single series by its ID and Bangumi subject ID.

// ScrapeSingle scrapes metadata for a single series by its ID and Bangumi subject ID.
func (s *Scraper) ScrapeSingle(seriesID string, bangumiID int) (*ScrapeResult, error) {
	target, err := s.db.GetSeriesByID(seriesID)
	if err != nil {
		return nil, fmt.Errorf("get series by id: %w", err)
	}
	if target == nil {
		return nil, fmt.Errorf("series not found: %s", seriesID)
	}

	result := &ScrapeResult{
		Total:   1,
		Matched: 0,
		Failed:  0,
		Skipped: 0,
		Errors:  make([]string, 0),
	}

	log.Printf("Scraping single: %s (ID: %d)", target.Name, bangumiID)

	subject, err := s.bangumi.GetSubject(bangumiID)
	if err != nil {
		errMsg := fmt.Sprintf("%s: get subject %d failed: %v", target.Name, bangumiID, err)
		log.Printf("  ERROR: %s", errMsg)
		result.Errors = append(result.Errors, errMsg)
		result.Failed++
		return result, nil
	}

	searchResult := bangumi.SearchResult{
		ID:       subject.ID,
		Name:     subject.Name,
		NameCN:   subject.NameCN,
		Image:    subject.Image,
		Platform: subject.Platform,
	}
	meta := s.buildSeriesMeta(*target, subject, searchResult)

	if err := s.db.SaveSeriesMeta(meta); err != nil {
		errMsg := fmt.Sprintf("%s: save metadata failed: %v", target.Name, err)
		log.Printf("  ERROR: %s", errMsg)
		result.Errors = append(result.Errors, errMsg)
		result.Failed++
		return result, nil
	}
	result.Matched++

	volumes, err := s.scrapeVolumeMeta(target.ID, subject)
	if err != nil {
		log.Printf("  Warning: volume scrape failed: %v", err)
	}
	_ = volumes

	log.Printf("  Matched: %s -> %s (ID: %d)", target.Name, subject.NameCN, subject.ID)
	return result, nil
}
