package tournament

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
)

// testFederationOld is a stand-in for a liga.nu based federation.
var testFederationOld = models.Federation{
	Id:             "BAD",
	Name:           "Badischer Tennisverband",
	State:          "Baden-Württemberg",
	ApiVersion:     "old",
	Geocoordinates: models.Geocoordinates{Lat: "49.0", Lon: "8.4"},
}

// testFederationNew is a stand-in for a TYPO3/nuPortal based federation.
var testFederationNew = models.Federation{
	Id:             "WTB",
	Name:           "Württembergischer Tennisbund",
	State:          "Baden-Württemberg",
	ApiVersion:     "new",
	Geocoordinates: models.Geocoordinates{Lat: "48.85", Lon: "9.13"},
}

var testFederationRLP = models.Federation{
	Id:             "RLP",
	Name:           "Rheinland-Pfälzischer Tennisverband",
	State:          "Rheinland-Pfalz",
	ApiVersion:     "new",
	Geocoordinates: models.Geocoordinates{Lat: "49.99", Lon: "8.27"},
}

// staticGeocoder returns fixed coordinates so parser tests never touch the
// network and stay deterministic.
func staticGeocoder(lat, lon string) geocoder {
	return func(fed models.Federation, tournament models.Tournament) models.Geocoordinates {
		return models.Geocoordinates{Lat: lat, Lon: lon, DisplayName: fed.State}
	}
}

// failingGeocoder simulates a lookup that cannot resolve coordinates.
func failingGeocoder() geocoder {
	return func(models.Federation, models.Tournament) models.Geocoordinates {
		return models.Geocoordinates{}
	}
}

func loadFixture(t *testing.T, name string) *os.File {
	t.Helper()

	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("failed to open fixture %s: %v", name, err)
	}
	t.Cleanup(func() { f.Close() })

	return f
}

func findTournament(t *testing.T, tournaments []models.Tournament, id string) models.Tournament {
	t.Helper()

	for _, tournament := range tournaments {
		if tournament.Id == id {
			return tournament
		}
	}

	t.Fatalf("tournament with id %q not found in %d results", id, len(tournaments))
	return models.Tournament{}
}

func competitions(tournament models.Tournament) []string {
	out := make([]string, 0, len(tournament.Entries))
	for _, e := range tournament.Entries {
		out = append(out, e.Competition)
	}
	return out
}

func TestParseOldApiDocument(t *testing.T) {
	tournaments, err := ParseOldApiDocument(
		loadFixture(t, "old_api_basic.html"),
		testFederationOld,
		staticGeocoder("49.0069", "8.4037"),
	)
	if err != nil {
		t.Fatalf("ParseOldApiDocument() error = %v", err)
	}

	if len(tournaments) != 3 {
		t.Fatalf("got %d tournaments, want 3", len(tournaments))
	}

	first := tournaments[0]
	if first.Id != "700001" {
		t.Errorf("first.Id = %q, want %q", first.Id, "700001")
	}
	if first.Title != "LK-Turnier Sommer Open" {
		t.Errorf("first.Title = %q", first.Title)
	}
	if first.Date != "01.08.2026 - 03.08.2026" {
		t.Errorf("first.Date = %q", first.Date)
	}
	// Regression for the whitespace normalization fix: the organizer contains a
	// run of spaces in the fixture and must come back collapsed.
	if first.Organizer != "TC Rot-Weiß Karlsruhe" {
		t.Errorf("first.Organizer = %q, want %q", first.Organizer, "TC Rot-Weiß Karlsruhe")
	}
	if first.Lat != "49.0069" || first.Lon != "8.4037" {
		t.Errorf("first coordinates = (%q,%q), want geocoder result", first.Lat, first.Lon)
	}

	// The two continuation rows must be attached to the first tournament.
	wantComps := []string{"Herren Einzel", "Damen Einzel", "Herren Doppel"}
	gotComps := competitions(first)
	if len(gotComps) != len(wantComps) {
		t.Fatalf("first competitions = %v, want %v", gotComps, wantComps)
	}
	for i := range wantComps {
		if gotComps[i] != wantComps[i] {
			t.Errorf("competition %d = %q, want %q", i, gotComps[i], wantComps[i])
		}
	}

	if got := first.Entries[0].SkillLevel; got != "LK 12,0" {
		t.Errorf("first entry skill level = %q, want %q", got, "LK 12,0")
	}
	// "&nbsp;" is a placeholder for "no LK" and must be normalized away.
	if got := first.Entries[2].SkillLevel; got != "" {
		t.Errorf("doubles skill level = %q, want empty", got)
	}

	// A title split across lines must not lose its word boundary.
	second := findTournament(t, tournaments, "700002")
	if second.Title != "Jugendturnier Heidelberg" {
		t.Errorf("second.Title = %q, want %q", second.Title, "Jugendturnier Heidelberg")
	}
	if second.Organizer != "TC Heidelberg" {
		t.Errorf("second.Organizer = %q, want %q", second.Organizer, "TC Heidelberg")
	}

	// A URL without "detail/" yields an empty ID but must still be returned.
	third := tournaments[2]
	if third.Id != "" {
		t.Errorf("third.Id = %q, want empty", third.Id)
	}
	if third.Title != "Turnier ohne ID" {
		t.Errorf("third.Title = %q", third.Title)
	}
}

func TestParseOldApiDocumentFallsBackToFederationCoordinates(t *testing.T) {
	tournaments, err := ParseOldApiDocument(
		loadFixture(t, "old_api_basic.html"),
		testFederationOld,
		failingGeocoder(),
	)
	if err != nil {
		t.Fatalf("ParseOldApiDocument() error = %v", err)
	}
	if len(tournaments) == 0 {
		t.Fatal("no tournaments parsed")
	}

	for _, tournament := range tournaments {
		if tournament.Lat != testFederationOld.Geocoordinates.Lat ||
			tournament.Lon != testFederationOld.Geocoordinates.Lon {
			t.Errorf("tournament %s coordinates = (%q,%q), want federation default (%q,%q)",
				tournament.Id, tournament.Lat, tournament.Lon,
				testFederationOld.Geocoordinates.Lat, testFederationOld.Geocoordinates.Lon)
		}
	}
}

func TestParseNewApiDocumentWTB(t *testing.T) {
	tournaments, err := ParseNewApiDocument(
		loadFixture(t, "new_api_wtb.html"),
		testFederationNew,
		staticGeocoder("48.7758", "9.1829"),
	)
	if err != nil {
		t.Fatalf("ParseNewApiDocument() error = %v", err)
	}

	if len(tournaments) != 2 {
		t.Fatalf("got %d tournaments, want 2", len(tournaments))
	}

	first := findTournament(t, tournaments, "800001")
	if first.Title != "Stuttgart Open" {
		t.Errorf("first.Title = %q, want %q", first.Title, "Stuttgart Open")
	}
	if first.Date != "01.09.2026 - 03.09.2026" {
		t.Errorf("first.Date = %q", first.Date)
	}
	if first.Organizer != "TC Stuttgart" {
		t.Errorf("first.Organizer = %q, want %q", first.Organizer, "TC Stuttgart")
	}
	if first.Location != "Stuttgart" {
		t.Errorf("first.Location = %q, want %q", first.Location, "Stuttgart")
	}
	if first.Lat != "48.7758" || first.Lon != "9.1829" {
		t.Errorf("first coordinates = (%q,%q)", first.Lat, first.Lon)
	}

	// The continuation row must append to the existing tournament rather than
	// creating a duplicate entry.
	wantComps := []string{"Herren Einzel", "Damen Einzel", "Herren Doppel"}
	gotComps := competitions(first)
	if len(gotComps) != len(wantComps) {
		t.Fatalf("first competitions = %v, want %v", gotComps, wantComps)
	}
	for i := range wantComps {
		if gotComps[i] != wantComps[i] {
			t.Errorf("competition %d = %q, want %q", i, gotComps[i], wantComps[i])
		}
	}

	second := findTournament(t, tournaments, "800002")
	if second.Title != "Ulmer Herbstturnier" {
		t.Errorf("second.Title = %q, want %q", second.Title, "Ulmer Herbstturnier")
	}
	if len(second.Entries) != 1 {
		t.Errorf("second entries = %d, want 1", len(second.Entries))
	}
}

func TestParseNewApiDocumentRLPVariant(t *testing.T) {
	tournaments, err := ParseNewApiDocument(
		loadFixture(t, "new_api_rlp.html"),
		testFederationRLP,
		staticGeocoder("49.9929", "8.2473"),
	)
	if err != nil {
		t.Fatalf("ParseNewApiDocument() error = %v", err)
	}

	if len(tournaments) != 2 {
		t.Fatalf("got %d tournaments, want 2", len(tournaments))
	}

	// h3 title and "Offen für" location terminator.
	first := findTournament(t, tournaments, "900001")
	if first.Title != "Mainz Cup" {
		t.Errorf("first.Title = %q", first.Title)
	}
	if first.Location != "Mainz" {
		t.Errorf("first.Location = %q, want %q", first.Location, "Mainz")
	}
	if first.Organizer != "TC Mainz" {
		t.Errorf("first.Organizer = %q", first.Organizer)
	}

	// Without a location the parser must not call the geocoder, so the
	// coordinates stay empty here (the caller shows the federation default).
	second := findTournament(t, tournaments, "900002")
	if second.Location != "" {
		t.Errorf("second.Location = %q, want empty", second.Location)
	}
	if second.Lat != "" || second.Lon != "" {
		t.Errorf("second coordinates = (%q,%q), want empty", second.Lat, second.Lon)
	}
}

func TestParseDocumentsHandleEmptyAndMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		html string
	}{
		{"empty", ""},
		{"no matching table", "<html><body><p>Keine Turniere</p></body></html>"},
		{"empty result set", `<table class="result-set"><tr><th>Datum</th></tr></table>`},
		{"empty responsive table", `<table class="responsive-individual"><tbody></tbody></table>`},
		{"unclosed tags", `<table class="result-set"><tr><td rowspan="1">01.01.2026`},
		{"not html at all", "%%%not-html%%%"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/old", func(t *testing.T) {
			got, err := ParseOldApiDocument(strings.NewReader(tt.html), testFederationOld, staticGeocoder("1", "2"))
			if err != nil {
				t.Fatalf("ParseOldApiDocument() error = %v", err)
			}
			if len(got) != 0 {
				t.Errorf("got %d tournaments, want 0", len(got))
			}
		})

		t.Run(tt.name+"/new", func(t *testing.T) {
			got, err := ParseNewApiDocument(strings.NewReader(tt.html), testFederationNew, staticGeocoder("1", "2"))
			if err != nil {
				t.Fatalf("ParseNewApiDocument() error = %v", err)
			}
			if len(got) != 0 {
				t.Errorf("got %d tournaments, want 0", len(got))
			}
		})
	}
}

func TestParseNewApiDocumentUsesDefaultGeocoderWhenNil(t *testing.T) {
	// A nil geocoder must not panic; it falls back to the default lookup.
	// The fixture without a location never triggers a network call.
	tournaments, err := ParseNewApiDocument(
		strings.NewReader(`<table class="responsive-individual"><tbody></tbody></table>`),
		testFederationNew,
		nil,
	)
	if err != nil {
		t.Fatalf("ParseNewApiDocument() error = %v", err)
	}
	if len(tournaments) != 0 {
		t.Errorf("got %d tournaments, want 0", len(tournaments))
	}
}

func TestGetTournamentIdByUrl(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.tennis.de/spielen/turniersuche.html#detail/699982", "699982"},
		{"https://mybigpoint.tennis.de/web/guest/turniersuche?tournamentId=484582", ""},
		{"", ""},
		{"detail/", ""},
		{"https://example.com/detail/12345", "12345"},
	}

	for _, tt := range tests {
		if got := getTournamentIdByUrl(tt.url); got != tt.want {
			t.Errorf("getTournamentIdByUrl(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestNormalizeSkillLevel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"LK 12,0", "LK 12,0"},
		{"  LK   12,0  ", "LK 12,0"},
		{"&nbsp;", ""},
		{"", ""},
		{"\u00a0", ""},
	}

	for _, tt := range tests {
		if got := normalizeSkillLevel(tt.in); got != tt.want {
			t.Errorf("normalizeSkillLevel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAgeCategoryForCompType(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"Herren+Einzel", "general"},
		{"Damen+Doppel", "general"},
		{"Senioren+Einzel", "seniors"},
		{"Jugend+Doppel", "juniors"},
		{"Unsinn", ""},
	}

	for _, tt := range tests {
		if got := ageCategoryForCompType(tt.in); got != tt.want {
			t.Errorf("ageCategoryForCompType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFilterFederations(t *testing.T) {
	all := []models.Federation{
		{Id: "BAD"}, {Id: "WTB"}, {Id: "HTV"},
	}

	tests := []struct {
		name      string
		selection string
		want      []string
	}{
		{"empty selects all", "", []string{"BAD", "WTB", "HTV"}},
		{"whitespace selects all", "   ", []string{"BAD", "WTB", "HTV"}},
		{"single", "WTB", []string{"WTB"}},
		{"multiple keeps source order", "HTV,BAD", []string{"BAD", "HTV"}},
		{"tolerates spaces", " BAD , HTV ", []string{"BAD", "HTV"}},
		{"unknown ids ignored", "BAD,NOPE", []string{"BAD"}},
		{"duplicates collapse", "BAD,BAD", []string{"BAD"}},
		{"only unknown", "NOPE", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterFederations(all, tt.selection)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d federations, want %d (%v)", len(got), len(tt.want), tt.want)
			}
			for i := range tt.want {
				if got[i].Id != tt.want[i] {
					t.Errorf("federation %d = %q, want %q", i, got[i].Id, tt.want[i])
				}
			}
		})
	}
}

func TestBuildOldApiPayload(t *testing.T) {
	payload := buildOldApiPayload(testFederationOld, "01.08.2026", "15.08.2026", "Herren+Einzel")

	for _, want := range []string{
		"queryDateFrom=01.08.2026",
		"queryDateTo=15.08.2026",
		"federation=BAD",
		"valuationState=1",
		"region=DE",
	} {
		if !strings.Contains(payload, want) {
			t.Errorf("payload %q does not contain %q", payload, want)
		}
	}

	// The "+" in the competition type must survive as an encoded plus rather
	// than being interpreted as a space.
	if !strings.Contains(payload, "compType=Herren%2BEinzel") {
		t.Errorf("payload %q does not contain encoded compType", payload)
	}

	// Without a competition type the parameter must be omitted entirely.
	if got := buildOldApiPayload(testFederationOld, "01.08.2026", "15.08.2026", ""); strings.Contains(got, "compType") {
		t.Errorf("payload %q should not contain compType", got)
	}
}

func TestBuildNewApiURL(t *testing.T) {
	fed := testFederationNew
	fed.Url = "https://example.test/turnierkalender"
	fed.TrustedProperties = "trusted-value"

	raw, err := buildNewApiURL(fed, "01.09.2026", "30.09.2026", "Jugend+Einzel", 200)
	if err != nil {
		t.Fatalf("buildNewApiURL() error = %v", err)
	}

	for _, want := range []string{
		"tx_nuportalrs_tournaments%5BtournamentsFilter%5D%5BstartDate%5D=01.09.2026",
		"tx_nuportalrs_tournaments%5BtournamentsFilter%5D%5BendDate%5D=30.09.2026",
		"tx_nuportalrs_tournaments%5BtournamentsFilter%5D%5BageCategory%5D=juniors",
		"tx_nuportalrs_tournaments%5BtournamentsFilter%5D%5BfirstResult%5D=200",
		"tx_nuportalrs_tournaments%5BtournamentsFilter%5D%5BmaxResults%5D=100",
		"trusted-value",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("URL %q does not contain %q", raw, want)
		}
	}

	// RLP uses a different parameter prefix.
	rlp := testFederationRLP
	rlp.Url = "https://example.test/turnierkalender"
	rlpURL, err := buildNewApiURL(rlp, "01.09.2026", "30.09.2026", "", 0)
	if err != nil {
		t.Fatalf("buildNewApiURL(RLP) error = %v", err)
	}
	if !strings.Contains(rlpURL, "tx_nuportalrs_nuportalrs") {
		t.Errorf("RLP URL %q does not use the nuportalrs prefix", rlpURL)
	}

	// An invalid base URL must surface as an error.
	broken := testFederationNew
	broken.Url = "://not a url"
	if _, err := buildNewApiURL(broken, "", "", "", 0); err == nil {
		t.Error("buildNewApiURL() with invalid URL returned nil error")
	}
}

func entry(comp, lk string) models.CompetitionEntry {
	return models.CompetitionEntry{Competition: comp, SkillLevel: lk}
}

func TestFilterByLK(t *testing.T) {
	tournaments := []models.Tournament{
		{
			Id: "open-to-all", Title: "Offen",
			Entries: []models.CompetitionEntry{entry("Herren Einzel", "1,0–25,0")},
		},
		{
			Id: "strong-only", Title: "Nur Starke",
			Entries: []models.CompetitionEntry{entry("Herren Einzel", "1,0–12,0")},
		},
		{
			Id: "weak-only", Title: "Nur Schwache",
			Entries: []models.CompetitionEntry{entry("Herren Einzel", "15,0–25,0")},
		},
		{
			Id: "mixed", Title: "Gemischt",
			Entries: []models.CompetitionEntry{
				entry("Herren Einzel", "1,0–12,0"),
				entry("Herren 40 Einzel", "15,0–25,0"),
				entry("Herren Doppel", "1,0–25,0"),
			},
		},
	}

	t.Run("strong player", func(t *testing.T) {
		got := FilterByLK(tournaments, 5.0)

		ids := make([]string, 0, len(got))
		for _, tr := range got {
			ids = append(ids, tr.Id)
		}
		want := []string{"open-to-all", "strong-only", "mixed"}
		if len(ids) != len(want) {
			t.Fatalf("got %v, want %v", ids, want)
		}
		for i := range want {
			if ids[i] != want[i] {
				t.Errorf("tournament %d = %q, want %q", i, ids[i], want[i])
			}
		}

		// Only the competitions the player may enter survive.
		mixed := got[2]
		if len(mixed.Entries) != 2 {
			t.Fatalf("mixed has %d entries, want 2", len(mixed.Entries))
		}
		for _, e := range mixed.Entries {
			if e.Competition == "Herren 40 Einzel" {
				t.Error("kept a competition the player cannot enter")
			}
		}
	})

	t.Run("weak player", func(t *testing.T) {
		got := FilterByLK(tournaments, 20.0)

		if len(got) != 3 {
			t.Fatalf("got %d tournaments, want 3", len(got))
		}
		for _, tr := range got {
			if tr.Id == "strong-only" {
				t.Error("kept a tournament that excludes this player")
			}
		}
	})

	t.Run("boundaries are inclusive", func(t *testing.T) {
		// A player at exactly the upper bound must still qualify.
		if got := FilterByLK(tournaments, 12.0); len(got) != 3 {
			t.Errorf("LK 12.0 matched %d tournaments, want 3 (bound is inclusive)", len(got))
		}
		if got := FilterByLK(tournaments, 15.0); len(got) != 3 {
			t.Errorf("LK 15.0 matched %d tournaments, want 3 (bound is inclusive)", len(got))
		}
	})
}

// TestFilterByLKKeepsUnrestrictedEntries is the deliberate choice not to hide
// tournaments whose LK range could not be parsed or was never published.
func TestFilterByLKKeepsUnrestrictedEntries(t *testing.T) {
	tournaments := []models.Tournament{
		{Id: "no-lk", Entries: []models.CompetitionEntry{entry("Herren Einzel", "")}},
		{Id: "unparseable", Entries: []models.CompetitionEntry{entry("Herren Einzel", "offen für alle")}},
		{Id: "no-entries-at-all"},
	}

	got := FilterByLK(tournaments, 5.0)
	if len(got) != 3 {
		t.Fatalf("got %d tournaments, want all 3 kept", len(got))
	}
}

func TestFilterByLKOnEmptyInput(t *testing.T) {
	if got := FilterByLK(nil, 10); len(got) != 0 {
		t.Errorf("got %d tournaments for nil input", len(got))
	}
}

// TestFilterByLKDoesNotMutateInput guards against the filter corrupting the
// cached result set, which is shared between requests.
func TestFilterByLKDoesNotMutateInput(t *testing.T) {
	original := []models.Tournament{
		{
			Id: "t1",
			Entries: []models.CompetitionEntry{
				entry("Herren Einzel", "1,0–12,0"),
				entry("Herren 40 Einzel", "15,0–25,0"),
			},
		},
	}

	FilterByLK(original, 5.0)

	if len(original[0].Entries) != 2 {
		t.Errorf("input was mutated: %d entries remain, want 2", len(original[0].Entries))
	}
}
