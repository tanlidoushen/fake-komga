package database

import (
	"database/sql"
	"context"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// SeriesMeta represents Bangumi metadata for a fake-komga-115 series.
type SeriesMeta struct {
	SeriesID      string   `json:"seriesId"`
	BangumiID     int      `json:"bangumiId"`
	TitleCN       string   `json:"titleCn"`
	TitleJP       string   `json:"titleJp"`
	Summary       string   `json:"summary"`
	Publisher     string   `json:"publisher"`
	Status        string   `json:"status"` // ONGOING, ENDED, HIATUS
	TotalVolumes  int      `json:"totalVolumes"`
	Rating        float64  `json:"rating"`
	RatingCount   int      `json:"ratingCount"`
	Tags          []string `json:"tags"`
	Authors       []Author `json:"authors"`
	CoverURL      string   `json:"coverUrl"`
	Platform      string   `json:"platform"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Author represents a person involved in creating the work.
type Author struct {
	Name string `json:"name"`
	Role string `json:"role"` // writer, penciller, illustrator, etc.
}

// BookMeta represents Bangumi metadata for a single volume/book.
type BookMeta struct {
	BookID       string `json:"bookId"`
	SeriesID     string `json:"seriesId"`
	VolumeNumber int    `json:"volumeNumber"`
	ISBN         string `json:"isbn"`
	ReleaseDate  string `json:"releaseDate"`
	Summary      string `json:"summary"`
	CoverURL     string `json:"coverUrl"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// DB wraps the SQLite connection.
type DB struct {
	db *sql.DB
}

// Open opens the fake-komga-115 database and ensures metadata tables exist.
func Open(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=10000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	d := &DB{db: db}
	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS bangumi_series_meta (
		series_id TEXT PRIMARY KEY,
		bangumi_id INTEGER NOT NULL,
		title_cn TEXT NOT NULL DEFAULT '',
		title_jp TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		publisher TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		total_volumes INTEGER NOT NULL DEFAULT 0,
		rating REAL NOT NULL DEFAULT 0,
		rating_count INTEGER NOT NULL DEFAULT 0,
		tags_json TEXT NOT NULL DEFAULT '[]',
		authors_json TEXT NOT NULL DEFAULT '[]',
		cover_url TEXT NOT NULL DEFAULT '',
		platform TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL,
		FOREIGN KEY(series_id) REFERENCES series(id) ON DELETE CASCADE
	);
	CREATE TABLE IF NOT EXISTS bangumi_book_meta (
		book_id TEXT PRIMARY KEY,
		series_id TEXT NOT NULL,
		volume_number INTEGER NOT NULL DEFAULT 0,
		isbn TEXT NOT NULL DEFAULT '',
		release_date TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		cover_url TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL,
		FOREIGN KEY(book_id) REFERENCES books(id) ON DELETE CASCADE,
		FOREIGN KEY(series_id) REFERENCES series(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_bangumi_book_series ON bangumi_book_meta(series_id);
	CREATE TABLE IF NOT EXISTS failed_series (
		series_id TEXT PRIMARY KEY,
		reason TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		FOREIGN KEY(series_id) REFERENCES series(id) ON DELETE CASCADE
	);
	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	`
	_, err := d.db.Exec(schema)
	return err
}

// GetSeriesList returns all series from fake-komga-115 that need metadata.
// It excludes series that already have metadata unless forceRefresh is true.
func (d *DB) GetSeriesList(forceRefresh bool) ([]SeriesInfo, error) {
	var query string
	if forceRefresh {
		query = `SELECT s.id, s.name, s.library_id, l.name as library_name
			FROM series s JOIN libraries l ON s.library_id = l.id
			ORDER BY s.name`
	} else {
		query = `SELECT s.id, s.name, s.library_id, l.name as library_name
			FROM series s
			JOIN libraries l ON s.library_id = l.id
			LEFT JOIN bangumi_series_meta bm ON s.id = bm.series_id
			WHERE bm.series_id IS NULL
			ORDER BY s.name`
	}
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query series: %w", err)
	}
	defer rows.Close()

	var series []SeriesInfo
	for rows.Next() {
		var s SeriesInfo
		if err := rows.Scan(&s.ID, &s.Name, &s.LibraryID, &s.LibraryName); err != nil {
			return nil, fmt.Errorf("scan series: %w", err)
		}
		series = append(series, s)
	}
	return series, rows.Err()
}

type SeriesInfo struct {
	ID           string
	Name         string
	LibraryID    string
	LibraryName  string
}

// GetSeriesBooks returns books for a series, ordered by number_sort.
func (d *DB) GetSeriesBooks(seriesID string) ([]BookInfo, error) {
	rows, err := d.db.Query(
		`SELECT id, name, number_sort FROM books WHERE series_id = ? ORDER BY number_sort ASC`,
		seriesID,
	)
	if err != nil {
		return nil, fmt.Errorf("query books: %w", err)
	}
	defer rows.Close()

	var books []BookInfo
	for rows.Next() {
		var b BookInfo
		if err := rows.Scan(&b.ID, &b.Name, &b.NumberSort); err != nil {
			return nil, fmt.Errorf("scan book: %w", err)
		}
		books = append(books, b)
	}
	return books, rows.Err()
}

type BookInfo struct {
	ID         string
	Name       string
	NumberSort float64
}

// SaveSeriesMeta saves or updates Bangumi metadata for a series.
func (d *DB) SaveSeriesMeta(meta *SeriesMeta) error {
	tagsJSON, _ := json.Marshal(meta.Tags)
	authorsJSON, _ := json.Marshal(meta.Authors)

	_, err := d.db.Exec(`
		INSERT INTO bangumi_series_meta
			(series_id, bangumi_id, title_cn, title_jp, summary, publisher, status,
			 total_volumes, rating, rating_count, tags_json, authors_json, cover_url, platform, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(series_id) DO UPDATE SET
			bangumi_id=excluded.bangumi_id, title_cn=excluded.title_cn, title_jp=excluded.title_jp,
			summary=excluded.summary, publisher=excluded.publisher, status=excluded.status,
			total_volumes=excluded.total_volumes, rating=excluded.rating, rating_count=excluded.rating_count,
			tags_json=excluded.tags_json, authors_json=excluded.authors_json, cover_url=excluded.cover_url,
			platform=excluded.platform, updated_at=excluded.updated_at
	`,
		meta.SeriesID, meta.BangumiID, meta.TitleCN, meta.TitleJP, meta.Summary,
		meta.Publisher, meta.Status, meta.TotalVolumes, meta.Rating, meta.RatingCount,
		tagsJSON, authorsJSON, meta.CoverURL, meta.Platform, meta.UpdatedAt.Format(time.RFC3339),
	)
	return err
}

// SaveBookMeta saves or updates Bangumi metadata for a book.
func (d *DB) SaveBookMeta(meta *BookMeta) error {
	_, err := d.db.Exec(`
		INSERT INTO bangumi_book_meta
			(book_id, series_id, volume_number, isbn, release_date, summary, cover_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(book_id) DO UPDATE SET
			series_id=excluded.series_id, volume_number=excluded.volume_number,
			isbn=excluded.isbn, release_date=excluded.release_date,
			summary=excluded.summary, cover_url=excluded.cover_url, updated_at=excluded.updated_at
	`,
		meta.BookID, meta.SeriesID, meta.VolumeNumber, meta.ISBN, meta.ReleaseDate,
		meta.Summary, meta.CoverURL, meta.UpdatedAt.Format(time.RFC3339),
	)
	return err
}

// GetSeriesMeta retrieves metadata for a series.
func (d *DB) GetSeriesMeta(seriesID string) (*SeriesMeta, error) {
	row := d.db.QueryRow(`
		SELECT series_id, bangumi_id, title_cn, title_jp, summary, publisher, status,
		       total_volumes, rating, rating_count, tags_json, authors_json, cover_url, platform, updated_at
		FROM bangumi_series_meta WHERE series_id = ?
	`, seriesID)

	var meta SeriesMeta
	var tagsJSON, authorsJSON, updatedAt string
	err := row.Scan(&meta.SeriesID, &meta.BangumiID, &meta.TitleCN, &meta.TitleJP,
		&meta.Summary, &meta.Publisher, &meta.Status, &meta.TotalVolumes,
		&meta.Rating, &meta.RatingCount, &tagsJSON, &authorsJSON, &meta.CoverURL, &meta.Platform, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan series meta: %w", err)
	}

	json.Unmarshal([]byte(tagsJSON), &meta.Tags)
	json.Unmarshal([]byte(authorsJSON), &meta.Authors)
	meta.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &meta, nil
}

// GetStats returns scraping statistics.
func (d *DB) GetStats() (map[string]any, error) {
	var total, scraped int
	d.db.QueryRow("SELECT COUNT(*) FROM series").Scan(&total)
	d.db.QueryRow("SELECT COUNT(*) FROM bangumi_series_meta").Scan(&scraped)
	return map[string]any{
		"totalSeries":  total,
		"scrapedSeries": scraped,
		"pendingSeries": total - scraped,
	}, nil
}

// GetSeriesByID returns a single series by its ID.
func (d *DB) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := d.db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := d.db.Exec(
		"INSERT INTO settings(key,value,updated_at) VALUES(?,?,datetime('now')) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=datetime('now')",
		key, value)
	return err
}

func (d *DB) GetSeriesByID(seriesID string) (*SeriesInfo, error) {
	row := d.db.QueryRow(
		`SELECT s.id, s.name, s.library_id, l.name as library_name
		 FROM series s JOIN libraries l ON s.library_id = l.id
		 WHERE s.id = ?`, seriesID,
	)
	var s SeriesInfo
	err := row.Scan(&s.ID, &s.Name, &s.LibraryID, &s.LibraryName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query series by id: %w", err)
	}
	return &s, nil
}

// GetAllSeries returns all series with their library info.
func (d *DB) GetFailedSeries() ([]SeriesInfo, error) {
	rows, err := d.db.Query(
		`SELECT s.id, s.name, s.library_id, l.name as library_name, fs.reason
		 FROM series s
		 JOIN libraries l ON s.library_id = l.id
		 JOIN failed_series fs ON s.id = fs.series_id
		 ORDER BY fs.created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query failed series: %w", err)
	}
	defer rows.Close()

	var series []SeriesInfo
	for rows.Next() {
		var s SeriesInfo
		var reason string
		if err := rows.Scan(&s.ID, &s.Name, &s.LibraryID, &s.LibraryName, &reason); err != nil {
			return nil, fmt.Errorf("scan failed series: %w", err)
		}
		series = append(series, s)
	}
	return series, rows.Err()
}

func (d *DB) MarkFailed(seriesID, reason string) error {
	_, err := d.db.Exec(
		"INSERT OR REPLACE INTO failed_series(series_id, reason, created_at) VALUES(?,?,datetime('now'))",
		seriesID, reason)
	return err
}

func (d *DB) ClearFailed(seriesID string) error {
	_, err := d.db.Exec("DELETE FROM failed_series WHERE series_id=?", seriesID)
	return err
}

func (d *DB) GetAllSeries() ([]SeriesInfo, error) {
	rows, err := d.db.Query(
		`SELECT s.id, s.name, s.library_id, l.name as library_name
		 FROM series s JOIN libraries l ON s.library_id = l.id
		 ORDER BY s.name`,
	)
	if err != nil {
		return nil, fmt.Errorf("query all series: %w", err)
	}
	defer rows.Close()

	var series []SeriesInfo
	for rows.Next() {
		var s SeriesInfo
		if err := rows.Scan(&s.ID, &s.Name, &s.LibraryID, &s.LibraryName); err != nil {
			return nil, fmt.Errorf("scan series: %w", err)
		}
		series = append(series, s)
	}
	return series, rows.Err()
}
