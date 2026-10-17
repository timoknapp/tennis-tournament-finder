package clublocations

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultTableLoads(t *testing.T) {
	table, err := Default()
	if err != nil {
		t.Fatalf("Default() error = %v", err)
	}
	if table.Len() == 0 {
		t.Fatal("Default() returned an empty table; the embedded file should have entries")
	}
}

// TestEmbeddedOverridesResolve guards the shipped data file: a typo in the
// JSON would otherwise only surface as a wrong pin in production.
func TestEmbeddedOverridesResolve(t *testing.T) {
	table, err := Default()
	if err != nil {
		t.Fatalf("Default() error = %v", err)
	}

	tests := []struct {
		organizer string
		wantCity  string
	}{
		{"Lohausener Sport-Verein", "Düsseldorf"},
		{"Unterbarmer Tennisclub", "Wuppertal"},
		{"Post Südstadt Karlsruhe", "Karlsruhe"},
		{"TG Frankfurt-Höchst", "Frankfurt am Main"},
		{"TC Grün-Weiß Mannheim-Neckarau", "Mannheim"},
	}

	for _, tt := range tests {
		t.Run(tt.organizer, func(t *testing.T) {
			got, ok := table.Lookup(tt.organizer)
			if !ok {
				t.Fatalf("Lookup(%q) found no override", tt.organizer)
			}
			if got.City != tt.wantCity {
				t.Errorf("Lookup(%q).City = %q, want %q", tt.organizer, got.City, tt.wantCity)
			}
		})
	}
}

func TestLookupIsInsensitiveToSpellingVariants(t *testing.T) {
	table, err := Parse([]byte(`{"overrides":[
		{"match":"TC Rot-Weiß Karlsruhe e.V.","city":"Karlsruhe"}
	]}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	// All of these describe the same club.
	variants := []string{
		"TC Rot-Weiß Karlsruhe e.V.",
		"tc rot-weiß karlsruhe e.v.",
		"TC  Rot-Weiß   Karlsruhe  e. V.",
		"TC Rot Weiß Karlsruhe eV",
		"TC Rot-Weiss Karlsruhe e.V.",
	}

	for _, v := range variants {
		if _, ok := table.Lookup(v); !ok {
			t.Errorf("Lookup(%q) found no override", v)
		}
	}
}

func TestExactMatchWinsOverContains(t *testing.T) {
	table, err := Parse([]byte(`{"overrides":[
		{"contains":"Karlsruhe","city":"Generic"},
		{"match":"TC Spezial Karlsruhe","city":"Specific"}
	]}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	got, ok := table.Lookup("TC Spezial Karlsruhe")
	if !ok {
		t.Fatal("no override found")
	}
	if got.City != "Specific" {
		t.Errorf("City = %q, want the exact match to win", got.City)
	}
}

func TestLongestContainsPatternWins(t *testing.T) {
	table, err := Parse([]byte(`{"overrides":[
		{"contains":"Mannheim","city":"Mannheim"},
		{"contains":"Mannheim-Sandhofen","city":"Sandhofen"}
	]}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	got, _ := table.Lookup("TC Mannheim-Sandhofen 1920")
	if got.City != "Sandhofen" {
		t.Errorf("City = %q, want the more specific pattern to win", got.City)
	}

	got, _ = table.Lookup("TC Mannheim")
	if got.City != "Mannheim" {
		t.Errorf("City = %q, want the generic pattern for a generic name", got.City)
	}
}

func TestLookupMisses(t *testing.T) {
	table, err := Default()
	if err != nil {
		t.Fatalf("Default() error = %v", err)
	}

	for _, organizer := range []string{"", "   ", "TC Unbekannt", "e.V."} {
		if o, ok := table.Lookup(organizer); ok {
			t.Errorf("Lookup(%q) unexpectedly matched %+v", organizer, o)
		}
	}
}

func TestParseRejectsInvalidOverrides(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"no matcher", `{"overrides":[{"city":"Karlsruhe"}]}`},
		{"no target", `{"overrides":[{"match":"TC X"}]}`},
		{"lat without lon", `{"overrides":[{"match":"TC X","lat":"49.0"}]}`},
		{"lon without lat", `{"overrides":[{"match":"TC X","lon":"8.4"}]}`},
		{"malformed json", `{"overrides":[`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.json)); err == nil {
				t.Error("Parse() returned nil error for invalid input")
			}
		})
	}
}

func TestParseAcceptsExplicitCoordinates(t *testing.T) {
	table, err := Parse([]byte(`{"overrides":[
		{"match":"TC Sonderfall","lat":"49.0069","lon":"8.4037"}
	]}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	got, ok := table.Lookup("TC Sonderfall")
	if !ok {
		t.Fatal("no override found")
	}
	if !got.HasCoordinates() || got.Lat != "49.0069" || got.Lon != "8.4037" {
		t.Errorf("override = %+v, want explicit coordinates", got)
	}
}

func TestFileOverrideViaEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/custom.json"

	if err := writeFile(path, `{"overrides":[{"match":"TC Custom","city":"Teststadt"}]}`); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	// Default() memoizes, so exercise the same code path directly.
	raw, err := readFile(path)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	table, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	got, ok := table.Lookup("TC Custom")
	if !ok || got.City != "Teststadt" {
		t.Errorf("Lookup() = %+v, %v; want the custom file to be used", got, ok)
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"TC Rot-Weiß Karlsruhe e.V.", "tc rot weiss karlsruhe e v"},
		{"  TC   Karlsruhe  ", "tc karlsruhe"},
		{"TC/Karlsruhe", "tc karlsruhe"},
		{"", ""},
		{"...", ""},
		{"Grün-Weiss", "grun weiss"},
	}

	for _, tt := range tests {
		if got := Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeFoldsSharpS(t *testing.T) {
	if Normalize("Weiß") != Normalize("Weiss") {
		t.Errorf("ß and ss must normalize identically: %q vs %q",
			Normalize("Weiß"), Normalize("Weiss"))
	}
}

func TestLookupOnNilTable(t *testing.T) {
	var table *Table
	if _, ok := table.Lookup("TC Karlsruhe"); ok {
		t.Error("Lookup on nil table returned a match")
	}
	if table.Len() != 0 {
		t.Error("Len on nil table should be 0")
	}
}

// Small helpers to keep the file-based test readable.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// TestOverrideNotesAreDocumented keeps the data file self-explanatory: every
// entry should say why the automatic extraction cannot handle it.
func TestOverrideNotesAreDocumented(t *testing.T) {
	table, err := Default()
	if err != nil {
		t.Fatalf("Default() error = %v", err)
	}

	check := func(o Override) {
		if strings.TrimSpace(o.Note) == "" {
			t.Errorf("override %q has no note explaining why it is needed",
				o.Match+o.Contains)
		}
	}

	for _, o := range table.exact {
		check(o)
	}
	for _, o := range table.contains {
		check(o)
	}
}

// TestOverridesForReportedClubs pins the entries added after a live sweep found
// them falling back to their federation's default pin. Each name here was
// observed in production data, so a regression in matching would put real
// tournaments back in the middle of a state.
func TestOverridesForReportedClubs(t *testing.T) {
	table, err := Default()
	if err != nil {
		t.Fatalf("loading the default table failed: %v", err)
	}

	cases := []struct {
		organizer string
		wantCity  string
	}{
		{"Tus Berne e.V. Tennisabteilung", "Hamburg"},
		{"Tennisclub Ellerbek e.V.", "Ellerbek"},
		{"ETUF Tennisriege", "Essen"},

		{"Tennisclub am Falkenberg", "Norderstedt"},
		{"Gemünden", "Gemünden am Main"},
		{"TC Viktoria", "Köln"},
	}

	for _, tc := range cases {
		override, ok := table.Lookup(tc.organizer)
		if !ok {
			t.Errorf("%q has no override; it will fall back to the federation default", tc.organizer)
			continue
		}
		if override.City != tc.wantCity {
			t.Errorf("%q resolved to %q, want %q", tc.organizer, override.City, tc.wantCity)
		}
	}
}

// TestAmbiguousNamesArePinnedNotGuessed covers the two clubs whose names reach
// the wrong place even when a place is found.
//
// Both were left open in the first pass because guessing produces a confidently
// wrong pin, which is worse than an obvious fallback. They were resolved from
// evidence rather than by picking the most likely candidate, and both carry
// explicit coordinates because no place name reaches them:
//
//   - BTV publishes a club as plain "Neustadt". Bavaria has several, but the
//     tournament titles name the venue ("beim ASV Neustadt"), and ASV Neustadt
//     is in Neustadt an der Waldnaab.
//   - "TSV Neuenkirchen (SFA)" is in Delmsen in the Heidekreis, which is none
//     of the three places called Neuenkirchen in Niedersachsen.
func TestAmbiguousNamesArePinnedNotGuessed(t *testing.T) {
	table, err := Default()
	if err != nil {
		t.Fatalf("loading the default table failed: %v", err)
	}

	cases := []struct {
		organizer string
		lat, lon  string
	}{
		{"Neustadt", "49.727316", "12.179680"},
		{"TSV Neuenkirchen (SFA)", "53.027311", "9.705497"},
	}

	for _, tc := range cases {
		override, ok := table.Lookup(tc.organizer)
		if !ok {
			t.Errorf("%q has no override", tc.organizer)
			continue
		}
		if !override.HasCoordinates() {
			t.Errorf("%q resolves via the city name %q, which does not reach the right place; "+
				"it needs explicit coordinates", tc.organizer, override.City)
			continue
		}
		if override.Lat != tc.lat || override.Lon != tc.lon {
			t.Errorf("%q pinned at %s,%s want %s,%s",
				tc.organizer, override.Lat, override.Lon, tc.lat, tc.lon)
		}
	}
}

// TestNeustadtDoesNotSwallowOtherClubs checks the match/contains distinction.
//
// "Neustadt" is matched exactly: as a `contains` rule it would capture every
// club with Neustadt in its name across all federations and pin them all in
// the Oberpfalz.
func TestNeustadtDoesNotSwallowOtherClubs(t *testing.T) {
	table, err := Default()
	if err != nil {
		t.Fatalf("loading the default table failed: %v", err)
	}

	for _, other := range []string{
		"TC Neustadt an der Weinstraße",
		"TSV Neustadt bei Coburg",
		"SV Neustadt-Glewe",
	} {
		if override, ok := table.Lookup(other); ok && override.Lat == "49.727316" {
			t.Errorf("%q was captured by the plain Neustadt override", other)
		}
	}
}
