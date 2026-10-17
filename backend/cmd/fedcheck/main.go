// Command fedcheck performs a live smoke test against every configured
// federation and reports how many tournaments each one returns.
//
// It exists to catch a federation whose endpoint or markup changed, which
// otherwise surfaces only as silently missing tournaments. Like geobench it
// makes real network requests and is never run by CI.
//
// Usage:
//
//	go run ./cmd/fedcheck                 # next 60 days, all federations
//	go run ./cmd/fedcheck -days 30        # shorter window
//	go run ./cmd/fedcheck -fed TNB,WTV    # only some federations
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
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
	"github.com/timoknapp/tennis-tournament-finder/pkg/tournament"
)

func main() {
	days := flag.Int("days", 60, "size of the date window in days")
	feds := flag.String("fed", "", "comma-separated federation IDs (default: all)")
	geocode := flag.Bool("geocode", false, "also resolve coordinates (slow, rate limited)")
	flag.Parse()

	logger.SetLogLevel(logger.ErrorLevel)

	if !*geocode {
		// Geocoding is rate limited to 1 req/s, which would dominate the
		// runtime of a smoke test. Point it at a closed port and remove the
		// spacing so lookups fail instantly instead of throttling the run.
		os.Setenv("TTF_NOMINATIM_URL", "http://127.0.0.1:9/disabled")
		os.Setenv("TTF_NOMINATIM_INTERVAL_MS", "0")
		os.Setenv("TTF_GEOCODING_TIMEOUT_SECONDS", "1")
	}

	tmp, err := os.MkdirTemp("", "fedcheck")
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to create temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)
	os.Setenv("TTF_CACHE_PATH", tmp+"/cache.bolt")

	now := time.Now()
	dateFrom := now.Format("02.01.2006")
	dateTo := now.AddDate(0, 0, *days).Format("02.01.2006")

	all := federation.GetFederations()
	selected := tournament.FilterFederations(all, *feds)

	fmt.Printf("Window: %s .. %s\n\n", dateFrom, dateTo)
	fmt.Printf("%-6s %-38s %-10s %s\n", "ID", "NAME", "COUNT", "STATUS")
	fmt.Println(strings.Repeat("-", 90))

	var failures, empty int

	for _, fed := range selected {
		start := time.Now()
		tournaments, results := tournament.CollectTournaments(
			context.Background(), []models.Federation{fed}, dateFrom, dateTo, "")

		status := "ok"
		if err := results[0].Err; err != nil {
			status = "ERROR: " + truncate(err.Error(), 40)
			failures++
		} else if len(tournaments) == 0 {
			status = "EMPTY (check parser)"
			empty++
		}

		fmt.Printf("%-6s %-38s %-10d %s  (%.1fs)\n",
			fed.Id, truncate(fed.Name, 37), len(tournaments), status, time.Since(start).Seconds())
	}

	fmt.Println(strings.Repeat("-", 90))
	fmt.Printf("%d federations, %d errors, %d empty\n", len(selected), failures, empty)

	if failures > 0 || empty > 0 {
		os.Exit(1)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
