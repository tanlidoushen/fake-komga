package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"fake-komga-local/internal/database"
	"fake-komga-local/internal/httpserver"
	"fake-komga-local/internal/scanner"
	"fake-komga-local/internal/thumbnail"
)

func main() {
	var (
		host      = flag.String("host", envOr("FK115_HOST", "0.0.0.0"), "listen host")
		port      = flag.Int("port", envInt("FK115_PORT", 25600), "listen port")
		dataDir   = flag.String("data-dir", envOr("FK115_DATA_DIR", "./data"), "data directory")
		comicsDir = flag.String("comics-dir", envOr("FK115_COMICS_DIR", "./comics"), "comics directory")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	os.MkdirAll(*dataDir, 0o700)
	db, err := database.Open(filepath.Join(*dataDir, "fake-komga-115.db"))
	if err != nil {
		log.Error("open db", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	thumb := thumbnail.New(db, filepath.Join(*dataDir, "thumbnails", "series"))
	scan := scanner.New(db, []string{*comicsDir}, log)

	// Initial scan
	if err := scan.Scan(context.Background()); err != nil {
		log.Warn("initial scan", "error", err)
	}

	srv := httpserver.New(db, scan, thumb, *comicsDir, log)
	server := &http.Server{
		Addr:    net.JoinHostPort(*host, fmt.Sprintf("%d", *port)),
		Handler: srv.Handler(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Error("serve", "error", err)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	server.Shutdown(context.Background())
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	var v int
	if _, err := fmt.Sscanf(os.Getenv(key), "%d", &v); err == nil && v > 0 {
		return v
	}
	return def
}
