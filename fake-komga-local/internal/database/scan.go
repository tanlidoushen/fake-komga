package database

import (
	"context"
	"database/sql"
	"time"
)

type ScanRun struct {
	ID            string   `json:"id"`
	LibraryID     string   `json:"library_id"`
	Status        string   `json:"status"`
	TriggerType   string   `json:"trigger_type"`
	SeriesTotal   int      `json:"series_total"`
	BooksTotal    int      `json:"books_total"`
	SeriesAdded   int      `json:"series_added"`
	SeriesRemoved int      `json:"series_removed"`
	BooksAdded    int      `json:"books_added"`
	BooksRemoved  int      `json:"books_removed"`
	Error         string   `json:"error"`
	StartedAt     *string  `json:"started_at"`
	CompletedAt   *string  `json:"completed_at"`
	CreatedAt     string   `json:"created_at"`
}

func (d *DB) CreateScanRun(ctx context.Context, id, libraryID, trigger string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO scan_runs(id,library_id,status,trigger_type,created_at)
		VALUES(?,?,?,?,?)`, id, libraryID, "pending", trigger, now)
	return err
}

func (d *DB) UpdateScanRunStatus(ctx context.Context, id, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := d.db.ExecContext(ctx, `
		UPDATE scan_runs SET status=?,completed_at=? WHERE id=?`, status, now, id)
	return err
}

func (d *DB) UpdateScanRunProgress(ctx context.Context, id string, seriesTotal, booksTotal, seriesAdded, seriesRemoved, booksAdded, booksRemoved int) error {
	_, err := d.db.ExecContext(ctx, `
		UPDATE scan_runs SET series_total=?,books_total=?,series_added=?,series_removed=?,books_added=?,books_removed=?
		WHERE id=?`, seriesTotal, booksTotal, seriesAdded, seriesRemoved, booksAdded, booksRemoved, id)
	return err
}

func (d *DB) UpdateScanRunError(ctx context.Context, id, errMsg string) {
	d.db.ExecContext(ctx, `UPDATE scan_runs SET error=? WHERE id=?`, errMsg, id)
}

func (d *DB) GetScanRuns(ctx context.Context, limit int) ([]ScanRun, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT id,library_id,status,trigger_type,series_total,books_total,
			series_added,series_removed,books_added,books_removed,error,started_at,completed_at,created_at
		FROM scan_runs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []ScanRun
	for rows.Next() {
		var r ScanRun
		var startedAt, completedAt sql.NullString
		if err := rows.Scan(&r.ID, &r.LibraryID, &r.Status, &r.TriggerType,
			&r.SeriesTotal, &r.BooksTotal, &r.SeriesAdded, &r.SeriesRemoved,
			&r.BooksAdded, &r.BooksRemoved, &r.Error,
			&startedAt, &completedAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.StartedAt = nullableStringPtr(startedAt)
		r.CompletedAt = nullableStringPtr(completedAt)
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

func nullableStringPtr(ns sql.NullString) *string {
	if ns.Valid {
		return &ns.String
	}
	return nil
}

func (d *DB) GetLastScanRun(ctx context.Context) (*ScanRun, error) {
	runs, err := d.GetScanRuns(ctx, 1)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}
