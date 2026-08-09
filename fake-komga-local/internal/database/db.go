package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{db: db}, nil
}

func (d *DB) Close() error { return d.db.Close() }

func (d *DB) DB() *sql.DB { return d.db }

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS libraries (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, root TEXT NOT NULL,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS series (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, library_id TEXT NOT NULL,
		path TEXT NOT NULL, file_modified_at TEXT,
		seen_scan_id TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		FOREIGN KEY(library_id) REFERENCES libraries(id)
	);
	CREATE TABLE IF NOT EXISTS books (
		id TEXT PRIMARY KEY, series_id TEXT NOT NULL, name TEXT NOT NULL,
		path TEXT NOT NULL, size INTEGER NOT NULL DEFAULT 0,
		media_type TEXT NOT NULL DEFAULT 'application/zip',
		number_sort REAL NOT NULL DEFAULT 0,
		file_modified_at TEXT, seen_scan_id TEXT,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		FOREIGN KEY(series_id) REFERENCES series(id)
	);
	CREATE TABLE IF NOT EXISTS series_thumbnails (
		series_id TEXT PRIMARY KEY, source_book_id TEXT, source_version TEXT,
		path TEXT, media_type TEXT, width INTEGER, height INTEGER,
		size INTEGER, generation_duration_ns INTEGER,
		created_at TEXT, updated_at TEXT
	);
	CREATE TABLE IF NOT EXISTS bangumi_series_meta (
		series_id TEXT PRIMARY KEY, bangumi_id INTEGER,
		title_cn TEXT, title_jp TEXT, summary TEXT, publisher TEXT,
		status TEXT, total_volumes INTEGER, rating REAL, rating_count INTEGER,
		tags_json TEXT, authors_json TEXT, cover_url TEXT, platform TEXT,
		updated_at TEXT
	);
	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS scan_runs (
		id TEXT PRIMARY KEY,
		library_id TEXT,
		status TEXT NOT NULL DEFAULT 'pending',
		trigger_type TEXT NOT NULL DEFAULT 'manual',
		series_total INTEGER NOT NULL DEFAULT 0,
		books_total INTEGER NOT NULL DEFAULT 0,
		series_added INTEGER NOT NULL DEFAULT 0,
		series_removed INTEGER NOT NULL DEFAULT 0,
		books_added INTEGER NOT NULL DEFAULT 0,
		books_removed INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		started_at TEXT,
		completed_at TEXT,
		created_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS book_read_progress (
		book_id TEXT PRIMARY KEY,
		series_id TEXT NOT NULL,
		completed INTEGER NOT NULL DEFAULT 1,
		page INTEGER,
		read_date TEXT,
		completed_at TEXT,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(book_id) REFERENCES books(id) ON DELETE CASCADE,
		FOREIGN KEY(series_id) REFERENCES series(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_book_read_progress_series ON book_read_progress(series_id, completed);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// 迁移: 旧库补列
	db.Exec("ALTER TABLE books ADD COLUMN number_sort REAL NOT NULL DEFAULT 0")
	db.Exec("ALTER TABLE series ADD COLUMN file_modified_at TEXT")
	db.Exec("ALTER TABLE series ADD COLUMN seen_scan_id TEXT")
	db.Exec("ALTER TABLE books ADD COLUMN file_modified_at TEXT")
	db.Exec("ALTER TABLE books ADD COLUMN seen_scan_id TEXT")
	// 索引
	db.Exec("CREATE INDEX IF NOT EXISTS idx_books_series_sort ON books(series_id, number_sort, name)")
	return nil
}

func (d *DB) GetSeriesMeta(seriesID string) (*SeriesMeta, error) {
	row := d.db.QueryRow(`
		SELECT series_id, bangumi_id, title_cn, title_jp, summary, publisher,
			status, total_volumes, rating, rating_count, tags_json, authors_json,
			cover_url, platform, updated_at
		FROM bangumi_series_meta WHERE series_id=?`, seriesID)
	var meta SeriesMeta
	var tagsJSON, authorsJSON, updatedAt string
	err := row.Scan(&meta.SeriesID, &meta.BangumiID, &meta.TitleCN, &meta.TitleJP,
		&meta.Summary, &meta.Publisher, &meta.Status, &meta.TotalVolumes,
		&meta.Rating, &meta.RatingCount, &tagsJSON, &authorsJSON,
		&meta.CoverURL, &meta.Platform, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(tagsJSON), &meta.Tags)
	json.Unmarshal([]byte(authorsJSON), &meta.Authors)
	meta.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &meta, nil
}

func (d *DB) SaveSeriesMeta(meta *SeriesMeta) error {
	tagsJSON, _ := json.Marshal(meta.Tags)
	authorsJSON, _ := json.Marshal(meta.Authors)
	_, err := d.db.Exec(`
		INSERT INTO bangumi_series_meta(series_id, bangumi_id, title_cn, title_jp,
			summary, publisher, status, total_volumes, rating, rating_count,
			tags_json, authors_json, cover_url, platform, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(series_id) DO UPDATE SET
			bangumi_id=excluded.bangumi_id, title_cn=excluded.title_cn,
			title_jp=excluded.title_jp, summary=excluded.summary,
			publisher=excluded.publisher, status=excluded.status,
			total_volumes=excluded.total_volumes, rating=excluded.rating,
			rating_count=excluded.rating_count, tags_json=excluded.tags_json,
			authors_json=excluded.authors_json, cover_url=excluded.cover_url,
			platform=excluded.platform, updated_at=excluded.updated_at
	`, meta.SeriesID, meta.BangumiID, meta.TitleCN, meta.TitleJP,
		meta.Summary, meta.Publisher, meta.Status, meta.TotalVolumes,
		meta.Rating, meta.RatingCount, string(tagsJSON), string(authorsJSON),
		meta.CoverURL, meta.Platform, meta.UpdatedAt.Format(time.RFC3339))
	return err
}

func (d *DB) GetSetting(key string) (string, error) {
	var value string
	err := d.db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&value)
	if err != nil {
		return "", nil
	}
	return value, nil
}

func (d *DB) SetSetting(key, value string) error {
	_, err := d.db.Exec(
		"INSERT INTO settings(key,value,updated_at) VALUES(?,?,datetime('now')) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at",
		key, value)
	return err
}
