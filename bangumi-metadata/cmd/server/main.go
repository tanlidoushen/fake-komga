package main

import (
	"flag"
	"log"
	"os"

	"github.com/user/bangumi-metadata/internal/bangumi"
	"github.com/user/bangumi-metadata/internal/database"
	"github.com/user/bangumi-metadata/internal/httpserver"
	"github.com/user/bangumi-metadata/internal/scraper"
)

func main() {
	dbPath := flag.String("db", "", "Path to fake-komga-115 SQLite database (required)")
	addr := flag.String("addr", ":25601", "HTTP listen address")
	accessToken := flag.String("token", "", "Bangumi API access token (for NSFW)")
	scrapeOnce := flag.Bool("scrape", false, "Run scrape once and exit")
	force := flag.Bool("force", false, "Force re-scrape all series")
	flag.Parse()

	if *dbPath == "" {
		*dbPath = os.Getenv("FK115_DB_PATH")
	}
	if *dbPath == "" {
		log.Fatal("Database path required. Use --db or set FK115_DB_PATH")
	}

	if *accessToken == "" {
		*accessToken = os.Getenv("BANGUMI_ACCESS_TOKEN")
	}

	// Open database
	log.Printf("Opening database: %s", *dbPath)
	db, err := database.Open(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create Bangumi client
	bgClient := bangumi.New(*accessToken)

	if *scrapeOnce {
		// One-shot mode
		log.Printf("Running one-shot scrape (force=%v)", *force)
		scr := scraper.New(bgClient, db, *accessToken, false, "")
		result, err := scr.ScrapeAll(*force)
		if err != nil {
			log.Fatalf("Scrape failed: %v", err)
		}
		log.Printf("Result: %d total, %d matched, %d failed, %d skipped",
			result.Total, result.Matched, result.Failed, result.Skipped)
		for _, e := range result.Errors {
			log.Printf("  Error: %s", e)
		}
		return
	}

	// Server mode
	server := httpserver.New(db, bgClient, *accessToken)
	log.Fatal(server.ListenAndServe(*addr))
}
