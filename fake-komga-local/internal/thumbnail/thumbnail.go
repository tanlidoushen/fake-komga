package thumbnail

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/image/draw"

	"fake-komga-local/internal/archive"
	"fake-komga-local/internal/database"
)

const maxEdge = 300
const jpegQ = 75

type Service struct {
	db  *database.DB
	dir string
	mu  sync.Mutex
}

func New(db *database.DB, dir string) *Service {
	os.MkdirAll(dir, 0o700)
	return &Service{db: db, dir: dir}
}

func (s *Service) Generate(ctx context.Context, bookPath, seriesID string) error {
	ar, err := archive.Open(bookPath)
	if err != nil {
		return err
	}
	defer ar.Close()

	page, err := ar.Page(ctx, 0)
	if err != nil {
		return err
	}

	src, _, err := image.Decode(bytes.NewReader(page))
	if err != nil {
		return err
	}

	w, h := scaleSize(src.Bounds().Dx(), src.Bounds().Dy())
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQ}); err != nil {
		return err
	}

	name := seriesID + ".jpg"
	path := filepath.Join(s.dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return err
	}

	version := fmt.Sprintf("%d-%d", len(page), w)
	_, err = s.db.DB().ExecContext(ctx, `
		INSERT INTO series_thumbnails(series_id,source_book_id,source_version,path,media_type,width,height,size,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))
		ON CONFLICT(series_id) DO UPDATE SET
		source_book_id=excluded.source_book_id,source_version=excluded.source_version,
		path=excluded.path,media_type=excluded.media_type,width=excluded.width,height=excluded.height,
		size=excluded.size,updated_at=excluded.updated_at`,
		seriesID, seriesID, version, name, "image/jpeg", w, h, len(buf.Bytes()))
	return err
}

func (s *Service) ThumbnailPath(seriesID string) string {
	return filepath.Join(s.dir, seriesID+".jpg")
}

func (s *Service) Delete(ctx context.Context, seriesID string) error {
	os.Remove(s.ThumbnailPath(seriesID))
	_, err := s.db.DB().ExecContext(ctx, "DELETE FROM series_thumbnails WHERE series_id=?", seriesID)
	return err
}

func scaleSize(w, h int) (int, int) {
	if w <= maxEdge && h <= maxEdge {
		return w, h
	}
	if w >= h {
		return maxEdge, max(1, h*maxEdge/w)
	}
	return max(1, w*maxEdge/h), maxEdge
}
