package database

import "time"

type Series struct {
	ID        string
	Name      string
	LibraryID string
	Path      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Book struct {
	ID        string
	SeriesID  string
	Name      string
	Path      string
	Size      int64
	MediaType string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Library struct {
	ID        string
	Name      string
	Root      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SeriesMeta struct {
	SeriesID     string
	BangumiID    int
	TitleCN      string
	TitleJP      string
	Summary      string
	Publisher    string
	Status       string
	TotalVolumes int
	Rating       float64
	RatingCount  int
	Tags         []string
	Authors      []Author
	CoverURL     string
	Platform     string
	UpdatedAt    time.Time
}

type Author struct {
	Name string `json:"name"`
	Role string `json:"role"`
}
