package archive

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/nwaples/rardecode/v2"
)

type Reader interface {
	io.Closer
	Page(ctx context.Context, index int) ([]byte, error)
	PageCount(ctx context.Context) (int, error)
}

func Open(path string) (Reader, error) {
	ext := strings.ToLower(filepath.Ext(path))
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	info, _ := f.Stat()
	switch ext {
	case ".zip", ".cbz":
		return newZipReader(f, info.Size()), nil
	case ".rar", ".cbr":
		return newRarReader(f, info.Size()), nil
	default:
		f.Close()
		return nil, fmt.Errorf("unsupported: %s", ext)
	}
}

type zipReader struct {
	path string
	size int64
	mu   sync.Mutex
}

func newZipReader(f *os.File, sz int64) *zipReader {
	return &zipReader{path: f.Name(), size: sz}
}
func (r *zipReader) Close() error { return nil }

func (r *zipReader) Page(ctx context.Context, index int) ([]byte, error) {
	z, err := zip.OpenReader(r.path)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	var im []*zip.File
	for _, f := range z.File {
		if isImage(f.Name) {
			im = append(im, f)
		}
	}
	sort.Slice(im, func(i, j int) bool {
		return strings.ToLower(im[i].Name) < strings.ToLower(im[j].Name)
	})
	if index < 0 || index >= len(im) {
		return nil, fmt.Errorf("page %d not found", index)
	}
	rc, err := im[index].Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (r *zipReader) PageCount(ctx context.Context) (int, error) {
	z, err := zip.OpenReader(r.path)
	if err != nil {
		return 0, err
	}
	defer z.Close()
	c := 0
	for _, f := range z.File {
		if isImage(f.Name) {
			c++
		}
	}
	return c, nil
}

type rarReader struct {
	path string
	size int64
	mu   sync.Mutex
}

func newRarReader(f *os.File, sz int64) *rarReader {
	return &rarReader{path: f.Name(), size: sz}
}
func (r *rarReader) Close() error { return nil }

func (r *rarReader) Page(ctx context.Context, index int) ([]byte, error) {
	f, err := os.Open(r.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rr, err := rardecode.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("rar: %w", err)
	}
	type entry struct {
		name string
		data []byte
	}
	var im []entry
	for {
		hdr, err := rr.Next()
		if err != nil {
			break
		}
		if isImage(hdr.Name) {
			data, _ := io.ReadAll(rr)
			im = append(im, entry{hdr.Name, data})
		}
	}
	sort.Slice(im, func(i, j int) bool {
		return strings.ToLower(im[i].name) < strings.ToLower(im[j].name)
	})
	if index < 0 || index >= len(im) {
		return nil, fmt.Errorf("page %d not found", index)
	}
	return im[index].data, nil
}

func (r *rarReader) PageCount(ctx context.Context) (int, error) {
	f, err := os.Open(r.path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	rr, err := rardecode.NewReader(f)
	if err != nil {
		return 0, fmt.Errorf("rar: %w", err)
	}
	c := 0
	for {
		hdr, err := rr.Next()
		if err != nil {
			break
		}
		if isImage(hdr.Name) {
			c++
		}
	}
	return c, nil
}

func isImage(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".gif" || ext == ".bmp"
}
