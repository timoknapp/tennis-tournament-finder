// Command cachebench measures the effect of the result cache against live
// federation endpoints: a cold run scrapes, a warm run should not.
//
// It makes real network requests and is a manual tool, never run by CI.
//
// Usage:
//
//	go run ./cmd/cachebench                # all federations, 30-day window
//	go run ./cmd/cachebench -fed BAD,HTV   # a subset
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/timoknapp/tennis-tournament-finder/pkg/federation"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/resultcache"
	"github.com/timoknapp/tennis-tournament-finder/pkg/tournament"
)

func main() {
	days := flag.Int("days", 30, "size of the date window in days")
	feds := flag.String("fed", "", "comma-separated federation IDs (default: all)")
	flag.Parse()

	logger.SetLogLevel(logger.ErrorLevel)

	tmp, err := os.MkdirTemp("", "cachebench")
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to create temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	os.Setenv("TTF_CACHE_PATH", tmp+"/geo.bolt")
	// Geocoding is rate limited and would dominate the measurement; the cache
	// effect being measured here is about federation scraping.
	os.Setenv("TTF_NOMINATIM_URL", "http://127.0.0.1:9/disabled")
	os.Setenv("TTF_NOMINATIM_INTERVAL_MS", "0")
	os.Setenv("TTF_GEOCODING_TIMEOUT_SECONDS", "1")

	store, err := resultcache.NewBoltStore(tmp + "/results.bolt")
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to open result cache:", err)
		os.Exit(1)
	}
	cache := resultcache.New(store, resultcache.Options{
		TTL:      time.Hour,
		StaleTTL: 24 * time.Hour,
	})
	defer cache.Close()
	tournament.SetResultCache(cache)

	selected := tournament.FilterFederations(federation.GetFederations(), *feds)

	now := time.Now()
	dateFrom := now.Format("02.01.2006")
	dateTo := now.AddDate(0, 0, *days).Format("02.01.2006")

	fmt.Printf("Window: %s .. %s, %d federations\n\n", dateFrom, dateTo, len(selected))

	// Cold: nothing cached yet.
	coldStart := time.Now()
	coldTournaments, coldResults := tournament.CollectTournaments(
		context.Background(), selected, dateFrom, dateTo, "")
	cold := time.Since(coldStart)

	// Warm: everything should now come from the cache.
	warmStart := time.Now()
	warmTournaments, warmResults := tournament.CollectTournaments(
		context.Background(), selected, dateFrom, dateTo, "")
	warm := time.Since(warmStart)

	cachedCount := 0
	for _, r := range warmResults {
		if r.Cached {
			cachedCount++
		}
	}

	failed := 0
	for _, r := range coldResults {
		if r.Err != nil {
			failed++
		}
	}

	fmt.Printf("%-22s %s\n", "cold (scraping):", cold.Round(time.Millisecond))
	fmt.Printf("%-22s %s\n", "warm (cached):", warm.Round(time.Millisecond))
	if warm > 0 {
		fmt.Printf("%-22s %.0fx faster\n", "speedup:", float64(cold)/float64(warm))
	}
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("tournaments: cold %d, warm %d\n", len(coldTournaments), len(warmTournaments))
	fmt.Printf("served from cache on warm run: %d/%d federations\n", cachedCount, len(warmResults))
	fmt.Printf("federations failing on cold run: %d\n", failed)

	stats, err := cache.Stats()
	if err == nil {
		fmt.Printf("cache entries: %d (fresh %d), tournaments stored: %d\n",
			stats.Entries, stats.Fresh, stats.Tournaments)
	}

	if len(coldTournaments) != len(warmTournaments) {
		fmt.Fprintln(os.Stderr, "\nWARNING: cached run returned a different tournament count")
		os.Exit(1)
	}
	if cachedCount != len(warmResults) {
		fmt.Fprintln(os.Stderr, "\nWARNING: not every federation was served from cache")
		os.Exit(1)
	}
}
