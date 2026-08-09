package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fake-komga-local/internal/database"
)

type Scanner struct {
	db   *database.DB
	dirs []string
	log  *slog.Logger
}

func New(db *database.DB, dirs []string, log *slog.Logger) *Scanner {
	return &Scanner{db: db, dirs: dirs, log: log}
}

func (s *Scanner) SetDirs(dirs []string) { s.dirs = dirs }

func (s *Scanner) Scan(ctx context.Context) error {
	now := time.Now().UTC()
	runID := s.hash("scan", fmt.Sprintf("%d", now.UnixNano()))
	s.db.CreateScanRun(ctx, runID, "", "manual")

	for _, dir := range s.dirs {
		if err := s.scanDir(ctx, runID, dir); err != nil {
			s.log.Warn("scan dir", "dir", dir, "error", err)
			s.db.UpdateScanRunError(ctx, runID, err.Error())
		}
	}
	s.db.UpdateScanRunStatus(ctx, runID, "success")
	s.log.Info("scan completed", "run", runID)
	return nil
}

func (s *Scanner) scanDir(ctx context.Context, runID, dir string) error {
	s.log.Info("scanning", "dir", dir)
	libID := s.ensureLibrary(dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var seriesTotal, booksTotal, seriesAdded, seriesRemoved, booksAdded, booksRemoved int

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		seriesPath := filepath.Join(dir, e.Name())
		sid := s.hash("s", seriesPath)

		info, err := e.Info()
		modified := ""
		if err == nil {
			modified = info.ModTime().UTC().Format(time.RFC3339Nano)
		}

		// UPSERT series (仿 fake-komga-115)
		res, err := s.db.DB().ExecContext(ctx, `
			INSERT INTO series(id,name,library_id,path,file_modified_at,seen_scan_id,created_at,updated_at)
			VALUES(?,?,?,?,?,?,datetime('now'),datetime('now'))
			ON CONFLICT(id) DO UPDATE SET
				name=excluded.name, library_id=excluded.library_id,
				path=excluded.path, file_modified_at=excluded.file_modified_at,
				seen_scan_id=excluded.seen_scan_id, updated_at=datetime('now')`,
			sid, e.Name(), libID, seriesPath, modified, runID)
		if err != nil {
			s.log.Warn("upsert series", "name", e.Name(), "error", err)
			continue
		}
		seriesTotal++
		if n, _ := res.RowsAffected(); n == 1 {
			seriesAdded++
		}

		files, _ := os.ReadDir(seriesPath)
		bookIdx := 0
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(f.Name()))
			if ext != ".zip" && ext != ".cbz" && ext != ".rar" && ext != ".cbr" {
				continue
			}
			bp := filepath.Join(seriesPath, f.Name())
			bid := s.hash("b", bp)
			fi, err := f.Info()
			sz := int64(0)
			fileMod := ""
			if err == nil {
				sz = fi.Size()
				fileMod = fi.ModTime().UTC().Format(time.RFC3339Nano)
			}
			bookIdx++
			bookName := strings.TrimSuffix(f.Name(), ext)
			res, err := s.db.DB().ExecContext(ctx, `
				INSERT INTO books(id,series_id,name,path,size,media_type,number_sort,file_modified_at,seen_scan_id,created_at,updated_at)
				VALUES(?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))
				ON CONFLICT(id) DO UPDATE SET
					series_id=excluded.series_id, name=excluded.name, path=excluded.path,
					size=excluded.size, media_type=excluded.media_type,
					number_sort=excluded.number_sort, file_modified_at=excluded.file_modified_at,
					seen_scan_id=excluded.seen_scan_id, updated_at=datetime('now')`,
				bid, sid, bookName, bp, sz, "application/zip", bookIdx, fileMod, runID)
			if err != nil {
				s.log.Warn("upsert book", "name", f.Name(), "error", err)
				continue
			}
			booksTotal++
			if n, _ := res.RowsAffected(); n == 1 {
				booksAdded++
			}
		}
	}

	// 清理已删除的 book (seen_scan_id 不匹配, books表无library_id, 用子查询)
	delBooks, _ := s.db.DB().ExecContext(ctx,
		`DELETE FROM books WHERE series_id IN (SELECT id FROM series WHERE library_id=?) AND (seen_scan_id IS NULL OR seen_scan_id!=?)`, libID, runID)
	if n, _ := delBooks.RowsAffected(); n > 0 {
		booksRemoved = int(n)
		s.log.Info("removed books", "count", n)
	}
	// 清理已删除的 series
	delSeries, _ := s.db.DB().ExecContext(ctx,
		`DELETE FROM series WHERE library_id=? AND (seen_scan_id IS NULL OR seen_scan_id!=?)`, libID, runID)
	if n, _ := delSeries.RowsAffected(); n > 0 {
		seriesRemoved = int(n)
		s.log.Info("removed series", "count", n)
	}

	s.db.UpdateScanRunProgress(ctx, runID, seriesTotal, booksTotal, seriesAdded, seriesRemoved, booksAdded, booksRemoved)
	s.log.Info("scan done", "dir", dir, "series", seriesTotal, "books", booksTotal)
	return nil
}

func (s *Scanner) ensureLibrary(dir string) string {
	rows, _ := s.db.DB().Query("SELECT id FROM libraries WHERE root=?", dir)
	if rows != nil {
		defer rows.Close()
		var id string
		if rows.Next() {
			rows.Scan(&id)
			return id
		}
	}
	id := s.hash("lib", dir)
	s.db.DB().Exec(
		`INSERT INTO libraries(id,name,root,created_at,updated_at)
		 VALUES(?,?,?,datetime('now'),datetime('now'))`,
		id, filepath.Base(dir)+" Library", dir)
	return id
}

func (s *Scanner) hash(prefix, path string) string {
	h := sha256.Sum256([]byte(path))
	return prefix + "_" + hex.EncodeToString(h[:])
}
