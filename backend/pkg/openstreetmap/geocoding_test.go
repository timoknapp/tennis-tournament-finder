package openstreetmap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
)

// recordingGeocoder captures every query and answers from a lookup table.
type recordingGeocoder struct {
	mu      sync.Mutex
	queries []string
	answers map[string][]models.Geocoordinates
	calls   int64
}

func newRecordingServer(t *testing.T, answers map[string][]models.Geocoordinates) *recordingGeocoder {
	t.Helper()

	rec := &recordingGeocoder{answers: answers}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&rec.calls, 1)
		q := r.URL.Query().Get("q")

		rec.mu.Lock()
		rec.queries = append(rec.queries, q)
		rec.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rec.answers[q]) // nil -> "null", handled as no results
	}))
	t.Cleanup(srv.Close)

	t.Setenv("TTF_NOMINATIM_URL", srv.URL)

	return rec
}

func (r *recordingGeocoder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.queries))
	copy(out, r.queries)
	return out
}

// TestMultiStateFederationAcceptsEitherState is the regression test for
// federations spanning more than one state (TVBB: Berlin + Brandenburg,
// TNB: Niedersachsen + Bremen). Previously a tournament in the secondary
// state was rejected and fell back to the federation's default coordinates.
func TestMultiStateFederationAcceptsEitherState(t *testing.T) {
	initTestCache(t)

	newRecordingServer(t, map[string][]models.Geocoordinates{
		"Bad Saarow": {{
			Lat: "52.28", Lon: "14.06",
			DisplayName: "Bad Saarow, Oder-Spree, Brandenburg, Deutschland",
			Address:     models.Address{State: "Brandenburg", Village: "Bad Saarow"},
		}},
	})

	tvbb := models.Federation{
		Id:     "TVBB",
		State:  "Berlin",
		States: []string{"Berlin", "Brandenburg"},
	}

	got := GetGeocoordinatesForFederation(tvbb, models.Tournament{
		Id: "1", Location: "Bad Saarow", Organizer: "TC Bad Saarow",
	})

	if got.Lat != "52.28" {
		t.Errorf("got %+v, want the Brandenburg result to be accepted", got)
	}
}

func TestSingleStateFederationStillRejectsOtherStates(t *testing.T) {
	initTestCache(t)

	newRecordingServer(t, map[string][]models.Geocoordinates{
		"Musterstadt": {{
			Lat: "52.5", Lon: "13.4",
			DisplayName: "Musterstadt, Berlin, Deutschland",
			Address:     models.Address{State: "Berlin"},
		}},
	})

	fed := models.Federation{Id: "BAD", State: "Baden-Württemberg"}

	got := GetGeocoordinatesForFederation(fed, models.Tournament{
		Id: "1", Location: "Musterstadt",
	})

	if got.Lat != "" {
		t.Errorf("got %+v, want no result for a different state", got)
	}
}

// TestStateVerifiedViaStructuredAddress covers results whose display_name
// omits the state, which the old substring check rejected.
func TestStateVerifiedViaStructuredAddress(t *testing.T) {
	initTestCache(t)

	newRecordingServer(t, map[string][]models.Geocoordinates{
		"Neckarau": {{
			Lat: "49.45", Lon: "8.49",
			// No state in the display name, only in the structured address.
			DisplayName: "Neckarau, Mannheim, 68199, Deutschland",
			Address:     models.Address{State: "Baden-Württemberg", City: "Mannheim"},
		}},
	})

	fed := models.Federation{Id: "BAD", State: "Baden-Württemberg"}

	got := GetGeocoordinatesForFederation(fed, models.Tournament{
		Id: "1", Location: "Neckarau",
	})

	if got.Lat != "49.45" {
		t.Errorf("got %+v, want the result verified via address.state", got)
	}
}

// TestCandidatesAreTriedUntilOneResolves covers the central accuracy fix: a
// club name that yields no direct match must fall through to the derived
// candidate instead of giving up.
func TestCandidatesAreTriedUntilOneResolves(t *testing.T) {
	initTestCache(t)

	rec := newRecordingServer(t, map[string][]models.Geocoordinates{
		// Only the de-adjectived form resolves.
		"Heidelberg": {{
			Lat: "49.41", Lon: "8.69",
			DisplayName: "Heidelberg, Baden-Württemberg, Deutschland",
			Address:     models.Address{State: "Baden-Württemberg", City: "Heidelberg"},
		}},
	})

	fed := models.Federation{Id: "BAD", State: "Baden-Württemberg"}

	got := GetGeocoordinatesForFederation(fed, models.Tournament{
		Id: "1", Organizer: "Heidelberger Tennis-Club 1890 e.V.",
	})

	if got.Lat != "49.41" {
		t.Fatalf("got %+v, want the derived candidate to resolve", got)
	}

	queries := rec.recorded()
	if len(queries) < 2 {
		t.Errorf("queries = %v, want the literal form to be tried before the derived one", queries)
	}
}

func TestCandidateCountIsBounded(t *testing.T) {
	initTestCache(t)

	// Nothing resolves, so every candidate is attempted.
	rec := newRecordingServer(t, nil)

	fed := models.Federation{Id: "BAD", State: "Baden-Württemberg"}

	GetGeocoordinatesForFederation(fed, models.Tournament{
		Id:        "1",
		Location:  "Irgendwo an der Grenze",
		Organizer: "TC Rot-Weiß Musterhausen-Kleinkleckersdorf 1920 e.V.",
	})

	// Each candidate costs one rate-limited request and both lookup passes
	// share one budget, so the total must stay predictable regardless of how
	// long the club name is.
	if got := len(rec.recorded()); got > maxGeocodeRequests {
		t.Errorf("made %d requests, want at most %d", got, maxGeocodeRequests)
	}
}

// TestOverrideTakesPrecedence verifies the curated JSON file wins over the
// automatic extraction.
func TestOverrideTakesPrecedence(t *testing.T) {
	initTestCache(t)

	rec := newRecordingServer(t, map[string][]models.Geocoordinates{
		"Düsseldorf": {{
			Lat: "51.22", Lon: "6.77",
			DisplayName: "Düsseldorf, Nordrhein-Westfalen, Deutschland",
			Address:     models.Address{State: "Nordrhein-Westfalen", City: "Düsseldorf"},
		}},
	})

	fed := models.Federation{Id: "TVN", State: "Nordrhein-Westfalen"}

	// "Lohausener Sport-Verein" cannot be resolved from its name; the
	// override maps it to Düsseldorf.
	got := GetGeocoordinatesForFederation(fed, models.Tournament{
		Id: "1", Organizer: "Lohausener Sport-Verein",
	})

	if got.Lat != "51.22" {
		t.Fatalf("got %+v, want the override to resolve to Düsseldorf", got)
	}

	queries := rec.recorded()
	if len(queries) == 0 || queries[0] != "Düsseldorf" {
		t.Errorf("queries = %v, want the override to be tried first", queries)
	}
}

func TestOverrideWithPinnedCoordinatesSkipsNetwork(t *testing.T) {
	initTestCache(t)
	t.Setenv("TTF_CLUB_LOCATIONS", writeOverrides(t, `{"overrides":[
		{"match":"TC Sonderfall","lat":"49.0069","lon":"8.4037","note":"pinned"}
	]}`))
	resetClubLocationsForTest()

	rec := newRecordingServer(t, nil)

	fed := models.Federation{Id: "BAD", State: "Baden-Württemberg"}
	got := GetGeocoordinatesForFederation(fed, models.Tournament{
		Id: "1", Organizer: "TC Sonderfall",
	})

	if got.Lat != "49.0069" || got.Lon != "8.4037" {
		t.Errorf("got %+v, want the pinned coordinates", got)
	}
	if calls := len(rec.recorded()); calls != 0 {
		t.Errorf("made %d geocoding requests, want 0 for pinned coordinates", calls)
	}
}

func TestMatchesState(t *testing.T) {
	tests := []struct {
		name     string
		geo      models.Geocoordinates
		accepted []string
		want     bool
	}{
		{
			name:     "structured address matches",
			geo:      models.Geocoordinates{Address: models.Address{State: "Hessen"}},
			accepted: []string{"Hessen"},
			want:     true,
		},
		{
			name:     "structured address mismatches",
			geo:      models.Geocoordinates{Address: models.Address{State: "Berlin"}},
			accepted: []string{"Hessen"},
			want:     false,
		},
		{
			name:     "second accepted state matches",
			geo:      models.Geocoordinates{Address: models.Address{State: "Bremen"}},
			accepted: []string{"Niedersachsen", "Bremen"},
			want:     true,
		},
		{
			name:     "falls back to display name when address is absent",
			geo:      models.Geocoordinates{DisplayName: "Kassel, Hessen, Deutschland"},
			accepted: []string{"Hessen"},
			want:     true,
		},
		{
			name: "structured address wins over display name",
			geo: models.Geocoordinates{
				DisplayName: "Irgendwo, Hessen, Deutschland",
				Address:     models.Address{State: "Berlin"},
			},
			accepted: []string{"Hessen"},
			want:     false,
		},
		{
			name:     "no restriction accepts anything",
			geo:      models.Geocoordinates{Address: models.Address{State: "Sachsen"}},
			accepted: nil,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesState(tt.geo, tt.accepted); got != tt.want {
				t.Errorf("matchesState() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildGeocodeQueriesOrder(t *testing.T) {
	queries, _ := buildGeocodeQueries(models.Tournament{
		Location:  "Karlsruhe",
		Organizer: "TC Rot-Weiß Karlsruhe e.V.",
	})

	if len(queries) == 0 {
		t.Fatal("no queries built")
	}
	// The published location is the most trustworthy signal.
	if queries[0].value != "Karlsruhe" {
		t.Errorf("first query = %q, want the published location", queries[0].value)
	}

	// No duplicates, even though the organizer derives the same city.
	seen := make(map[string]bool)
	for _, q := range queries {
		if seen[q.value] {
			t.Errorf("duplicate query %q in %+v", q.value, queries)
		}
		seen[q.value] = true
	}
}

func TestBuildGeocodeQueriesWithoutUsableInput(t *testing.T) {
	queries, override := buildGeocodeQueries(models.Tournament{Id: "1"})
	if len(queries) != 0 {
		t.Errorf("queries = %+v, want none", queries)
	}
	if override != nil {
		t.Errorf("override = %+v, want nil", override)
	}
}

// writeOverrides writes a club-locations file and returns its path.
func writeOverrides(t *testing.T, content string) string {
	t.Helper()

	path := t.TempDir() + "/club-locations.json"
	if err := osWriteFile(path, content); err != nil {
		t.Fatalf("failed to write overrides: %v", err)
	}
	return path
}

// TestPlaceMatchesQuery covers the guard against Nominatim returning a
// different place that merely contains the queried word.
func TestPlaceMatchesQuery(t *testing.T) {
	tests := []struct {
		name  string
		place string
		query string
		want  bool
	}{
		{"exact match", "Bremen", "Bremen", true},
		{"case insensitive", "bremen", "Bremen", true},
		{"official long form", "Bad Homburg vor der Höhe", "Bad Homburg", true},
		{"city with suffix", "Frankfurt am Main", "Frankfurt", true},
		// Regression: "Bremer" must not settle for the hamlet "Bremer Sand".
		{"different place containing the query", "Bremer Sand", "Bremer", false},
		{"street-like name", "Ratinger Straße", "Ratinger", false},
		{"unrelated", "Bösel", "Bremen", false},
		{"empty place", "", "Bremen", false},
		{"empty query", "Bremen", "", false},
		{"query longer than place", "Bad", "Bad Homburg", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			geo := models.Geocoordinates{Address: models.Address{City: tt.place}}
			if got := placeMatchesQuery(geo, tt.query); got != tt.want {
				t.Errorf("placeMatchesQuery(%q, %q) = %v, want %v",
					tt.place, tt.query, got, tt.want)
			}
		})
	}
}

// TestExactPlaceMatchWinsOverInexactHit is the regression test for the
// "Bremer Tennisclub" case: an inexact hit from an early candidate must not
// beat an exact match from a later one.
func TestExactPlaceMatchWinsOverInexactHit(t *testing.T) {
	initTestCache(t)

	newRecordingServer(t, map[string][]models.Geocoordinates{
		// The literal token resolves to an unrelated hamlet ...
		"Bremer": {{
			Lat: "53.0", Lon: "7.9",
			DisplayName: "Bremer Sand, Bösel, Niedersachsen, Deutschland",
			Address:     models.Address{State: "Bremen", Village: "Bremer Sand"},
		}},
		// ... while the de-adjectived candidate resolves to the real city.
		"Bremen": {{
			Lat: "53.07", Lon: "8.80",
			DisplayName: "Bremen, Deutschland",
			Address:     models.Address{State: "Bremen", City: "Bremen"},
		}},
	})

	fed := models.Federation{Id: "TNB", State: "Niedersachsen",
		States: []string{"Niedersachsen", "Bremen"}}

	got := GetGeocoordinatesForFederation(fed, models.Tournament{
		Id: "1", Organizer: "Bremer Tennisclub",
	})

	if got.Address.Place() != "Bremen" {
		t.Errorf("resolved to %q (%s), want the exact city match",
			got.Address.Place(), got.DisplayName)
	}
}

// TestInexactHitUsedWhenNothingBetterExists ensures the stricter matching does
// not throw away the only available result.
func TestInexactHitUsedWhenNothingBetterExists(t *testing.T) {
	initTestCache(t)

	newRecordingServer(t, map[string][]models.Geocoordinates{
		"Kleinkleckersdorf": {{
			Lat: "50.0", Lon: "9.0",
			DisplayName: "Kleinkleckersdorf Siedlung, Hessen, Deutschland",
			Address:     models.Address{State: "Hessen", Village: "Kleinkleckersdorf Siedlung"},
		}},
	})

	fed := models.Federation{Id: "HTV", State: "Hessen"}

	got := GetGeocoordinatesForFederation(fed, models.Tournament{
		Id: "1", Location: "Kleinkleckersdorf",
	})

	if got.Lat != "50.0" {
		t.Errorf("got %+v, want the inexact hit rather than no result at all", got)
	}
}
