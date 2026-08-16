// Command pincheck reports which tournaments fall back to their federation's
// default coordinates, so wrong map pins can be found and fixed deliberately
// instead of waiting for a user to notice.
//
// Venue-exact addresses are not available from the upstream sources without a
// login (see issue #55), so the practical way to correct a pin is to add an
// entry to club-locations.json. This tool produces that list, and can emit
// ready-to-paste JSON for the clubs it could not resolve.
//
// It makes real network requests and is a manual tool, never run by CI.
//
// Usage:
//
//	go run ./cmd/pincheck                    # next 30 days, all federations
//	go run ./cmd/pincheck -days 60 -fed BAD  # narrow it down
//	go run ./cmd/pincheck -json              # emit override stubs
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/timoknapp/tennis-tournament-finder/pkg/federation"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
	"github.com/timoknapp/tennis-tournament-finder/pkg/placename"
	"github.com/timoknapp/tennis-tournament-finder/pkg/tournament"
)

type fallback struct {
	Organizer  string
	Federation string
	State      string
	Count      int
	Candidates []string
}

func main() {
	days := flag.Int("days", 30, "size of the date window in days")
	feds := flag.String("fed", "", "comma-separated federation IDs (default: all)")
	asJSON := flag.Bool("json", false, "emit override stubs for club-locations.json")
	flag.Parse()

	logger.SetLogLevel(logger.ErrorLevel)

	tmp, err := os.MkdirTemp("", "pincheck")
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to create temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)
	os.Setenv("TTF_CACHE_PATH", tmp+"/cache.bolt")

	selected := tournament.FilterFederations(federation.GetFederations(), *feds)

	now := time.Now()
	dateFrom := now.Format("02.01.2006")
	dateTo := now.AddDate(0, 0, *days).Format("02.01.2006")

	fmt.Fprintf(os.Stderr, "Checking %s .. %s across %d federations (this is rate limited, please wait)\n\n",
		dateFrom, dateTo, len(selected))

	states := make(map[string]string, len(selected))
	for _, fed := range selected {
		states[fed.Id] = fed.State
	}

	var total int
	byOrganizer := make(map[string]*fallback)

	for _, fed := range selected {
		tournaments, results := tournament.CollectTournaments(
			context.Background(), []models.Federation{fed}, dateFrom, dateTo, "")
		if results[0].Err != nil {
			fmt.Fprintf(os.Stderr, "  %s: %v\n", fed.Id, results[0].Err)
			continue
		}

		for _, t := range tournaments {
			total++
			// Comparing against the federation default used to stand in for
			// "did not resolve", but several defaults sit exactly on a major
			// city: the BTV default is München and the WTV default is Dortmund,
			// to seven decimal places. Clubs in those cities geocoded correctly
			// and were still reported as failures, which was 17 of the 32 hits
			// in a live sweep. The parser now records the outcome directly.
			if !t.ApproximateLocation {
				continue
			}

			key := fed.Id + "|" + t.Organizer
			if entry, ok := byOrganizer[key]; ok {
				entry.Count++
				continue
			}
			byOrganizer[key] = &fallback{
				Organizer:  t.Organizer,
				Federation: fed.Id,
				State:      states[fed.Id],
				Count:      1,
				Candidates: placename.Candidates(t.Organizer),
			}
		}
	}

	list := make([]*fallback, 0, len(byOrganizer))
	for _, f := range byOrganizer {
		list = append(list, f)
	}
	// Most-affected clubs first: fixing those helps the most users.
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return list[i].Organizer < list[j].Organizer
	})

	var affected int
	for _, f := range list {
		affected += f.Count
	}

	if *asJSON {
		emitStubs(list)
	} else {
		emitTable(list)
	}

	fmt.Fprintf(os.Stderr, "\n%d tournaments checked, %d on a fallback pin (%.1f%%), %d distinct clubs\n",
		total, affected, percent(affected, total), len(list))
	if len(list) > 0 && !*asJSON {
		fmt.Fprintln(os.Stderr, "Re-run with -json to get override stubs for club-locations.json")
	}
}

func emitTable(list []*fallback) {
	fmt.Printf("%-6s %-45s %-6s %s\n", "FED", "ORGANIZER", "COUNT", "CANDIDATES TRIED")
	fmt.Println(strings.Repeat("-", 110))
	for _, f := range list {
		cands := strings.Join(f.Candidates, ", ")
		if len(cands) > 42 {
			cands = cands[:41] + "…"
		}
		fmt.Printf("%-6s %-45s %-6d %s\n", f.Federation, truncate(f.Organizer, 44), f.Count, cands)
	}
}

// emitStubs prints entries ready to paste into club-locations.json, with the
// city left blank so it has to be filled in deliberately.
func emitStubs(list []*fallback) {
	type stub struct {
		Contains string `json:"contains"`
		City     string `json:"city"`
		State    string `json:"state"`
		Note     string `json:"note"`
	}

	stubs := make([]stub, 0, len(list))
	for _, f := range list {
		stubs = append(stubs, stub{
			Contains: f.Organizer,
			City:     "TODO",
			State:    f.State,
			Note: fmt.Sprintf("%d tournament(s) fell back to the %s default pin; tried: %s",
				f.Count, f.Federation, strings.Join(f.Candidates, ", ")),
		})
	}

	out, err := json.MarshalIndent(map[string]any{"overrides": stubs}, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to encode stubs:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

func percent(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(part) / float64(total)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
