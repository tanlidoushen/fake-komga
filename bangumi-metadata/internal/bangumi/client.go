package bangumi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	baseURL   = "https://api.bgm.tv"
	legacyURL = "https://bangumi.tv"
	userAgent = "bangumi-metadata/0.1.0"
)

type Client struct {
	httpClient  *http.Client
	accessToken string
}

func New(accessToken string) *Client {
	return &Client{
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		accessToken: accessToken,
	}
}

// SetAccessToken 动态更新令牌（无需重启）
func (c *Client) SetAccessToken(token string) {
	c.accessToken = token
}

type SubjectType int

const SubjectBook SubjectType = 1

type SearchResult struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	NameCN   string `json:"name_cn"`
	Image    string `json:"image"`
	Platform string `json:"platform"`
	Infobox  []any  `json:"infobox"`
}

type SearchResponse struct {
	Data  []SearchResult `json:"data"`
	Total int            `json:"total"`
}

func (c *Client) SearchSubjects(keyword string, typeFilter SubjectType, limit int) ([]SearchResult, error) {
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	body := map[string]any{
		"keyword": keyword,
		"sort":    "match",
		"filter": map[string]any{
			"type": []int{int(typeFilter)},
			"nsfw": true,
		},
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", baseURL+"/v0/search/subjects?limit=20", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("bangumi API 401: access token invalid or expired")
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bangumi API %d: %s", resp.StatusCode, string(b))
	}
	var sr SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return sr.Data, nil
}

type Subject struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	NameCN   string `json:"name_cn"`
	Summary  string `json:"summary"`
	Image    string `json:"image"`
	Images   *struct {
		Large  string `json:"large"`
		Medium string `json:"medium"`
		Common string `json:"common"`
		Small  string `json:"small"`
	} `json:"images"`
	Platform string        `json:"platform"`
	Infobox  []InfoboxItem `json:"infobox"`
	Tags     []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"tags"`
	Rating *struct {
		Score float64     `json:"score"`
		Count interface{} `json:"count"` // can be string or int
	} `json:"rating"`
	Volumes int `json:"volumes"`
	Eps     int `json:"eps"`
}

type InfoboxItem struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

func (c *Client) GetSubject(id int) (*Subject, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/v0/subjects/%d", baseURL, id), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get subject: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("bangumi API %d for subject %d: %s", resp.StatusCode, id, string(bodyBytes))
	}
	var s Subject
	if err := json.Unmarshal(bodyBytes, &s); err != nil {
		return nil, fmt.Errorf("decode subject: %w", err)
	}
	return &s, nil
}

type RelatedSubject struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	NameCN   string `json:"name_cn"`
	Image    string `json:"image"`
	Relation string `json:"relation"`
	Images   *struct {
		Large  string `json:"large"`
		Medium string `json:"medium"`
		Common string `json:"common"`
		Small  string `json:"small"`
	} `json:"images"`
}

func (c *Client) GetSubjectSubjects(id int) ([]RelatedSubject, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/v0/subjects/%d/subjects", baseURL, id), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get related subjects: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bangumi API %d for subjects: %s", resp.StatusCode, string(b))
	}
	var related []RelatedSubject
	if err := json.NewDecoder(resp.Body).Decode(&related); err != nil {
		return nil, fmt.Errorf("decode related subjects: %w", err)
	}
	return related, nil
}

func (c *Client) DownloadImage(imageURL string) ([]byte, string, error) {
	parsedURL, err := url.Parse(imageURL)
	if err != nil {
		return nil, "", fmt.Errorf("parse image URL: %w", err)
	}
	_ = parsedURL
	req, err := http.NewRequest("GET", imageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create image request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", legacyURL)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("image download returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, "", fmt.Errorf("read image: %w", err)
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/jpeg"
	}
	return data, ct, nil
}

func SubjectURL(id int) string {
	return fmt.Sprintf("%s/subject/%d", legacyURL, id)
}

func ParseInfoboxValue(infobox []InfoboxItem, key string) string {
	for _, item := range infobox {
		if item.Key == key {
			switch v := item.Value.(type) {
			case string:
				return v
			case map[string]any:
				if s, ok := v["v"].(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

// GetRatingCount safely extracts the rating count from the interface{} field.
func GetRatingCount(rating *struct {
	Score float64     `json:"score"`
	Count interface{} `json:"count"`
}) int {
	if rating == nil || rating.Count == nil {
		return 0
	}
	switch v := rating.Count.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		// Try to parse as int
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

// CleanFolderName extracts a clean search name from a folder name.
// Removes common patterns like [xxx], (xxx), tags, publisher info.
func CleanFolderName(name string) string {
	// Remove [xxx] patterns
	replacer := strings.NewReplacer(
		"[", " ",
		"]", " ",
		"（", " ",
		"）", " ",
		"(", " ",
		")", " ",
		"【", " ",
		"】", " ",
	)
	cleaned := replacer.Replace(name)
	// Split and take the longest segment as the title
	parts := strings.Fields(cleaned)
	if len(parts) == 0 {
		return name
	}
	// Find the longest part as the likely title
	longest := ""
	for _, p := range parts {
		if len(p) > len(longest) {
			longest = p
		}
	}
	if longest != "" {
		return longest
	}
	return parts[0]
}
