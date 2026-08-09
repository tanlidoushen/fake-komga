package bangumi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const mangadexAPI = "https://api.mangadex.org"

// MangaDexResult represents a search result from MangaDex.
type MangaDexResult struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	NameCN   string `json:"nameCn"`
	Image    string `json:"image"`
	Platform string `json:"platform"`
}

// MangaDexSearchResponse is the response from MangaDex search API.
type MangaDexSearchResponse struct {
	Data []struct {
		ID         string `json:"id"`
		Attributes struct {
			Title           map[string]string `json:"title"`
			AltTitles       []map[string]string `json:"altTitles"`
			Description     map[string]string `json:"description"`
			Status          string            `json:"status"`
			ContentRating   string            `json:"contentRating"`
			Tags            []struct {
				ID         string `json:"id"`
				Attributes struct {
					Name map[string]string `json:"name"`
				} `json:"attributes"`
			} `json:"tags"`
		} `json:"attributes"`
		Relationships []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"relationships"`
	} `json:"data"`
	Total int `json:"total"`
}

// SearchMangaDex searches MangaDex for manga by title.
func (c *Client) SearchMangaDex(keyword string, limit int) ([]MangaDexResult, error) {
	if limit <= 0 || limit > 20 {
		limit = 8
	}

	params := url.Values{}
	params.Set("title", keyword)
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("contentRating[]", "safe")
	params.Set("contentRating[]", "suggestive")
	params.Set("contentRating[]", "erotica")
	params.Set("availableTranslatedLanguage[]", "zh")
	params.Set("availableTranslatedLanguage[]", "en")
	params.Set("order[relevance]", "desc")

	req, err := http.NewRequest("GET", mangadexAPI+"/manga?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mangadex search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mangadex API %d: %s", resp.StatusCode, string(body)[:200])
	}

	var searchResp MangaDexSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("decode mangadex: %w", err)
	}

	var results []MangaDexResult
	for _, item := range searchResp.Data {
		title := item.Attributes.Title["en"]
		if cn, ok := item.Attributes.Title["zh"]; ok {
			title = cn
		}
		if cn, ok := item.Attributes.Title["zh-hk"]; ok {
			title = cn
		}
		if jp, ok := item.Attributes.Title["ja"]; ok {
			title = jp
		}

		// Get cover image
		coverID := ""
		for _, rel := range item.Relationships {
			if rel.Type == "cover_art" {
				coverID = rel.ID
				break
			}
		}

		coverURL := ""
		if coverID != "" {
			coverURL = fmt.Sprintf("https://uploads.mangadex.org/covers/%s/%s.jpg", item.ID, coverID)
		}

		results = append(results, MangaDexResult{
			ID:       item.ID,
			Name:     title,
			NameCN:   title,
			Image:    coverURL,
			Platform: "manga",
		})
	}

	return results, nil
}

// GetMangaDexCoverURL fetches the actual cover filename for a MangaDex manga.
func (c *Client) GetMangaDexCoverURL(mangaID string) (string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/cover?manga[]=%s&limit=1", mangadexAPI, mangaID), nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("get cover: %w", err)
	}
	defer resp.Body.Close()

	var coverResp struct {
		Data []struct {
			Attributes struct {
				FileName string `json:"fileName"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&coverResp); err != nil {
		return "", err
	}

	if len(coverResp.Data) > 0 {
		return fmt.Sprintf("https://uploads.mangadex.org/covers/%s/%s", mangaID, coverResp.Data[0].Attributes.FileName), nil
	}
	return "", nil
}

// NormalizeMangaDexResult converts a MangaDex result to the common SearchResult format.
func NormalizeMangaDexResult(r MangaDexResult) SearchResult {
	return SearchResult{
		Name:     r.Name,
		NameCN:   r.NameCN,
		Image:    r.Image,
		Platform: "漫画",
	}
}
