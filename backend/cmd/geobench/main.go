// Command geobench measures geocoding accuracy against a live Nominatim
// instance using the same code path the service uses.
//
// It is a manual tool, not part of the test suite: it performs real network
// requests and is therefore rate limited and slow. CI never runs it.
//
// Usage:
//
//	go run ./cmd/geobench            # run the built-in benchmark set
//	go run ./cmd/geobench -v         # also print every resolved location
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
	"github.com/timoknapp/tennis-tournament-finder/pkg/openstreetmap"
)

// benchCase is one club whose expected city is known.
type benchCase struct {
	organizer string
	location  string // as published by the federation; often empty
	state     string
	states    []string
	wantCity  string
}

// cases covers the failure modes that motivated the rewrite: adjectival club
// names, multi-word places, districts, hyphenated compounds and multi-state
// federations.
var cases = []benchCase{
	// Plain names.
	{organizer: "TC Rot-Weiß Karlsruhe e.V.", state: "Baden-Württemberg", wantCity: "Karlsruhe"},
	{organizer: "TSG Weinheim Abt. Tennis", state: "Baden-Württemberg", wantCity: "Weinheim"},
	{organizer: "SV Blau-Weiß Bühlertal", state: "Baden-Württemberg", wantCity: "Bühlertal"},
	{organizer: "TV 1877 Ettlingen", state: "Baden-Württemberg", wantCity: "Ettlingen"},
	{organizer: "TC Neckargemünd", state: "Baden-Württemberg", wantCity: "Neckargemünd"},
	{organizer: "TC Blau-Weiß Neuss", state: "Nordrhein-Westfalen", wantCity: "Neuss"},
	{organizer: "SV Grün-Weiß Erfurt", state: "Thüringen", wantCity: "Erfurt"},
	{organizer: "TC Weimar 1912", state: "Thüringen", wantCity: "Weimar"},
	{organizer: "TuS Griesheim", state: "Hessen", wantCity: "Griesheim"},

	// Adjectival club names.
	{organizer: "Heidelberger Tennis-Club 1890 e.V.", state: "Baden-Württemberg", wantCity: "Heidelberg"},
	{organizer: "Eppelheimer Tennis-Club", state: "Baden-Württemberg", wantCity: "Eppelheim"},
	{organizer: "Karbener Sportverein", state: "Hessen", wantCity: "Karben"},
	{organizer: "Ratinger Tennisclub Grün-Weiss", state: "Nordrhein-Westfalen", wantCity: "Ratingen"},

	// Multi-word place names.
	{organizer: "TC Bad Schönborn", state: "Baden-Württemberg", wantCity: "Bad Schönborn"},
	{organizer: "TC Bad Homburg v.d.H.", state: "Hessen", wantCity: "Bad Homburg"},

	// Districts: only solvable via the override file.
	{organizer: "TC Grün-Weiß Mannheim-Neckarau", state: "Baden-Württemberg", wantCity: "Mannheim"},
	{organizer: "TG Frankfurt-Höchst", state: "Hessen", wantCity: "Frankfurt"},
	{organizer: "Unterbarmer Tennisclub", state: "Nordrhein-Westfalen", wantCity: "Wuppertal"},
	{organizer: "Lohausener Sport-Verein", state: "Nordrhein-Westfalen", wantCity: "Düsseldorf"},
	{organizer: "Post Südstadt Karlsruhe", state: "Baden-Württemberg", wantCity: "Karlsruhe"},

	// Multi-state federations: the tournament sits in the secondary state.
	{organizer: "TC Bad Saarow", state: "Berlin",
		states: []string{"Berlin", "Brandenburg"}, wantCity: "Bad Saarow"},
	{organizer: "Bremer Tennisclub", state: "Niedersachsen",
		states: []string{"Niedersachsen", "Bremen"}, wantCity: "Bremen"},
}

func main() {
	verbose := flag.Bool("v", false, "print every resolved location")
	flag.Parse()

	// Keep the output readable; the benchmark reports its own results.
	logger.SetLogLevel(logger.ErrorLevel)

	// Use a throwaway cache so results are not served from a previous run.
	tmp, err := os.MkdirTemp("", "geobench")
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to create temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	os.Setenv("TTF_CACHE_PATH", tmp+"/cache.bolt")
	openstreetmap.InitCache()
	defer openstreetmap.CloseCache()

	var hits int
	fmt.Printf("%-40s %-20s %-8s %s\n", "ORGANIZER", "EXPECTED", "RESULT", "RESOLVED")
	fmt.Println(strings.Repeat("-", 110))

	for _, c := range cases {
		fed := models.Federation{
			State:  c.state,
			States: c.states,
		}
		tournament := models.Tournament{
			Id:        c.organizer,
			Organizer: c.organizer,
			Location:  c.location,
		}

		geo := openstreetmap.GetGeocoordinatesForFederation(fed, tournament)

		resolved := geo.DisplayName
		if resolved == "" {
			resolved = "(nothing)"
		}

		// Verify against the structured settlement name. Matching the free-text
		// display name would count "Ratinger Straße, Düsseldorf" as a hit for
		// Ratingen, which is exactly the wrong-pin bug this benchmark tracks.
		place := geo.Address.Place()
		ok := geo.Lat != "" && place != "" && strings.Contains(
			strings.ToLower(place), strings.ToLower(firstWord(c.wantCity)))
		if ok {
			hits++
		}

		status := "MISS"
		if ok {
			status = "ok"
		}

		fmt.Printf("%-40s %-20s %-8s %s\n",
			truncate(c.organizer, 39), truncate(c.wantCity, 19), status, truncate(place+" | "+resolved, 45))

		if *verbose && geo.Lat != "" {
			fmt.Printf("%-40s   lat=%s lon=%s place=%q state=%s\n",
				"", geo.Lat, geo.Lon, place, geo.Address.State)
		}
	}

	fmt.Println(strings.Repeat("-", 110))
	fmt.Printf("Accuracy: %d/%d = %.0f%%\n", hits, len(cases), 100*float64(hits)/float64(len(cases)))
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
