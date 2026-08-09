package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SeriesReadProgress 系列级阅读进度聚合（仿 fake-komga-115）
type SeriesReadProgress struct {
	BooksCount                   int     `json:"booksCount"`
	BooksReadCount               int     `json:"booksReadCount"`
	BooksUnreadCount             int     `json:"booksUnreadCount"`
	BooksInProgressCount         int     `json:"booksInProgressCount"`
	LastReadContinuousNumberSort float64 `json:"lastReadContinuousNumberSort"`
	MaxNumberSort                float64 `json:"maxNumberSort"`
}

// BookReadProgress 单本书阅读进度（仿 fake-komga-115）
type BookReadProgress struct {
	BookID      string
	SeriesID    string
	Completed   bool
	Page        *int
	ReadDate    *time.Time
	CompletedAt *time.Time
	UpdatedAt   time.Time
}

func nowText() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Unix(0, 0).UTC()
	}
	return parsed
}

func parseNullableTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed := parseTime(value.String)
	return &parsed
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return fmt.Sprintf("(%s)", strings.TrimSuffix(strings.Repeat("?,", count), ","))
}

// SeriesReadProgress 聚合系列阅读进度（LEFT JOIN book_read_progress）
func (d *DB) SeriesReadProgress(ctx context.Context, seriesID string) (SeriesReadProgress, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT b.number_sort, CASE WHEN p.book_id IS NULL THEN 0 ELSE 1 END AS has_progress,
 coalesce(p.completed,0)
FROM books b
LEFT JOIN book_read_progress p ON p.book_id=b.id
WHERE b.series_id=?
ORDER BY b.number_sort ASC,b.name COLLATE NOCASE ASC,b.id ASC`, seriesID)
	if err != nil {
		return SeriesReadProgress{}, err
	}
	defer rows.Close()

	var progress SeriesReadProgress
	continuous := true
	for rows.Next() {
		var numberSort float64
		var hasProgress, completed int
		if err := rows.Scan(&numberSort, &hasProgress, &completed); err != nil {
			return SeriesReadProgress{}, err
		}
		progress.BooksCount++
		progress.MaxNumberSort = numberSort
		switch {
		case hasProgress != 0 && completed != 0:
			progress.BooksReadCount++
			if continuous {
				progress.LastReadContinuousNumberSort = numberSort
			}
		case hasProgress != 0:
			progress.BooksInProgressCount++
			continuous = false
		default:
			continuous = false
		}
	}
	if err := rows.Err(); err != nil {
		return SeriesReadProgress{}, err
	}
	progress.BooksUnreadCount = progress.BooksCount -
		progress.BooksReadCount - progress.BooksInProgressCount
	return progress, nil
}

// BookReadProgress 查询单本书进度
func (d *DB) BookReadProgress(ctx context.Context, bookID string) (BookReadProgress, bool, error) {
	var progress BookReadProgress
	var completed int
	var page sql.NullInt64
	var readDate, completedAt, updatedAt sql.NullString
	err := d.db.QueryRowContext(ctx, `
SELECT book_id,series_id,completed,page,read_date,completed_at,updated_at
FROM book_read_progress WHERE book_id=?`, bookID).Scan(
		&progress.BookID, &progress.SeriesID, &completed,
		&page, &readDate, &completedAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return BookReadProgress{}, false, nil
		}
		return BookReadProgress{}, false, err
	}
	progress.Completed = completed != 0
	if page.Valid {
		value := int(page.Int64)
		progress.Page = &value
	}
	progress.ReadDate = parseNullableTime(readDate)
	progress.CompletedAt = parseNullableTime(completedAt)
	progress.UpdatedAt = parseTime(updatedAt.String)
	return progress, true, nil
}

// BookReadProgresses 批量查询进度（用于 books 列表）
func (d *DB) BookReadProgresses(ctx context.Context, bookIDs []string) (map[string]BookReadProgress, error) {
	out := map[string]BookReadProgress{}
	if len(bookIDs) == 0 {
		return out, nil
	}
	query := `
SELECT book_id,series_id,completed,page,read_date,completed_at,updated_at
FROM book_read_progress WHERE book_id IN ` + placeholders(len(bookIDs))
	args := make([]any, 0, len(bookIDs))
	for _, id := range bookIDs {
		args = append(args, id)
	}
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var progress BookReadProgress
		var completed int
		var page sql.NullInt64
		var readDate, completedAt, updatedAt sql.NullString
		if err := rows.Scan(
			&progress.BookID, &progress.SeriesID, &completed,
			&page, &readDate, &completedAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		progress.Completed = completed != 0
		if page.Valid {
			value := int(page.Int64)
			progress.Page = &value
		}
		progress.ReadDate = parseNullableTime(readDate)
		progress.CompletedAt = parseNullableTime(completedAt)
		progress.UpdatedAt = parseTime(updatedAt.String)
		out[progress.BookID] = progress
	}
	return out, rows.Err()
}

// UpdateBookReadProgress 更新/插入单本书进度（UPSERT，仿 fake-komga-115）
func (d *DB) UpdateBookReadProgress(
	ctx context.Context,
	bookID, seriesID string,
	completed *bool,
	page *int,
) error {
	now := nowText()
	completedValue := false
	if completed != nil {
		completedValue = *completed
	}
	completedInt := 0
	if completedValue {
		completedInt = 1
	}
	var completedAt any
	if completedValue {
		completedAt = now
	}
	var pageValue any
	if page != nil && *page > 0 {
		pageValue = *page
	}
	_, err := d.db.ExecContext(ctx, `
INSERT INTO book_read_progress(
 book_id,series_id,completed,page,read_date,completed_at,updated_at
) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(book_id) DO UPDATE SET
 series_id=excluded.series_id,
 completed=excluded.completed,
 page=coalesce(excluded.page, book_read_progress.page),
 read_date=excluded.read_date,
 completed_at=excluded.completed_at,
 updated_at=excluded.updated_at`,
		bookID, seriesID, completedInt, pageValue, now, completedAt, now)
	return err
}

// DeleteBookReadProgress 删除进度
func (d *DB) DeleteBookReadProgress(ctx context.Context, bookID string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM book_read_progress WHERE book_id=?`, bookID)
	return err
}
