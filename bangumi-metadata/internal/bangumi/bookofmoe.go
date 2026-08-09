package bangumi

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const bofURL = "https://bookof.moe"

// BookOfMoeResult represents a search result from bookof.moe.
type BookOfMoeResult struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Cover  string `json:"cover"`
}

// SearchBookOfMoe searches bookof.moe for a series by name.
// bookof.moe is a traditional Chinese comic database.
func (c *Client) SearchBookOfMoe(keyword string, limit int) ([]BookOfMoeResult, error) {
	if limit <= 0 || limit > 20 {
		limit = 8
	}

	// bookof.moe uses traditional Chinese
	searchURL := fmt.Sprintf("%s/data_list.php?s=%s&p=1", bofURL, url.QueryEscape(keyword))

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html")

	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bookofmoe search: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// Parse search results from HTML
	// datainfo-B=[分类],[ID],[标题],[作者],[出版日期]
	re := regexp.MustCompile(`datainfo-B=\[?\d+\]?,\[?(\d+)\]?,\[?([^,]*?)\]?,\[?([^,]*?)\]?,\[[\d-]+\]?`)
	matches := re.FindAllStringSubmatch(string(body), -1)

	var results []BookOfMoeResult
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) >= 4 {
			id := strings.TrimSpace(m[1])
			title := strings.TrimSpace(m[2])
			author := strings.TrimSpace(m[3])
			if id != "" && title != "" && !seen[id] {
				seen[id] = true
				results = append(results, BookOfMoeResult{
					ID:     id,
					Title:  title,
					Author: author,
				})
			}
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// GetBookOfMoeDetail fetches detailed info from bookof.moe page.
func (c *Client) GetBookOfMoeDetail(id string) (title, author, cover, publisher, summary string, err error) {
	pageURL := fmt.Sprintf("%s/b/%s.htm", bofURL, id)
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("get detail: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("read body: %w", err)
	}
	_ = body
	// Basic parsing - extract title from name_main class
	titleRe := regexp.MustCompile(`<[^>]*class\s*=\s*["']name_main["'][^>]*>([^<]+)`)
	if m := titleRe.FindStringSubmatch(string(body)); len(m) >= 2 {
		title = strings.TrimSpace(m[1])
	}

	// Cover URL from script
	coverRe := regexp.MustCompile(`window\.iframe_action\.location\.href\s*=\s*"([^"]+)"`)
	if m := coverRe.FindStringSubmatch(string(body)); len(m) >= 2 {
		coverURL := strings.TrimSpace(m[1])
		if !strings.HasPrefix(coverURL, "http") {
			coverURL = bofURL + "/" + strings.TrimPrefix(coverURL, "/")
		}
		cover = coverURL
	}

	return title, author, cover, publisher, summary, nil
}
