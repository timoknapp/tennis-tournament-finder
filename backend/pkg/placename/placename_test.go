package placename

import (
	"slices"
	"strings"
	"testing"
)

// containsCandidate reports whether want appears in the candidate list.
func containsCandidate(got []string, want string) bool {
	return slices.ContainsFunc(got, func(c string) bool {
		return strings.EqualFold(c, want)
	})
}

// TestCandidatesContainExpectedCity is the core regression suite: every entry
// is a real-world club name whose city must appear among the candidates.
func TestCandidatesContainExpectedCity(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		// Straightforward: city is the trailing token.
		{"TC Rot-Weiß Karlsruhe e.V.", "Karlsruhe"},
		{"TSG Weinheim Abt. Tennis", "Weinheim"},
		{"SV Blau-Weiß Bühlertal", "Bühlertal"},
		{"TV 1877 Ettlingen", "Ettlingen"},
		{"TC Neckargemünd", "Neckargemünd"},
		{"TC Blau-Weiß Neuss", "Neuss"},
		{"SV Grün-Weiß Erfurt", "Erfurt"},
		{"TC Weimar 1912", "Weimar"},
		{"TuS Griesheim", "Griesheim"},

		// Adjectival forms: the previous implementation needed a hardcoded
		// list for each of these.
		{"Heidelberger Tennis-Club 1890 e.V.", "Heidelberg"},
		{"Eppelheimer Tennis-Club", "Eppelheim"},
		{"Karbener Sportverein", "Karben"},
		{"Ratinger Tennisclub Grün-Weiss", "Ratingen"},

		// Multi-word place names must survive tokenization.
		{"TC Bad Schönborn", "Bad Schönborn"},
		{"TC Bad Homburg v.d.H.", "Bad Homburg"},
		{"TC Sankt Augustin", "Sankt Augustin"},
		{"TC Bad Vilbel 1954", "Bad Vilbel"},

		// Hyphenated compounds: both the compound and its parts are offered.
		{"TC Grün-Weiß Mannheim-Neckarau", "Mannheim"},
		{"TG Frankfurt-Höchst", "Frankfurt"},

		// Noise-heavy names.
		{"DJK Sportgemeinschaft Wiesbaden", "Wiesbaden"},
		{"Post SV Nürnberg", "Nürnberg"},
		{"TC Rot-Weiss Bergisch Gladbach", "Gladbach"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Candidates(tt.name)
			if !containsCandidate(got, tt.want) {
				t.Errorf("Candidates(%q) = %v\n  want it to contain %q", tt.name, got, tt.want)
			}
		})
	}
}

// TestCandidatesRankExpectedCityHighly guards against the city being buried
// behind many unlikely guesses: a geocoder lookup is attempted per candidate,
// so position matters for both accuracy and request volume.
func TestCandidatesRankExpectedCityHighly(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		withinN int
	}{
		{"TC Rot-Weiß Karlsruhe e.V.", "Karlsruhe", 1},
		{"TC Blau-Weiß Neuss", "Neuss", 1},
		{"TC Bad Schönborn", "Bad Schönborn", 1},
		{"Heidelberger Tennis-Club 1890 e.V.", "Heidelberg", 3},
		{"Karbener Sportverein", "Karben", 3},
		{"TSG Weinheim Abt. Tennis", "Weinheim", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Candidates(tt.name)
			if len(got) > tt.withinN {
				got = got[:tt.withinN]
			}
			if !containsCandidate(got, tt.want) {
				t.Errorf("Candidates(%q) top %d = %v\n  want it to contain %q",
					tt.name, tt.withinN, got, tt.want)
			}
		})
	}
}

func TestCandidatesNeverReturnNoise(t *testing.T) {
	// These tokens must never be offered as a place name; querying them
	// produces the arbitrary far-away matches that cause wrong pins.
	forbidden := []string{
		"TC", "SV", "Tennis", "Club", "Verein", "Sport", "Post",
		"e.V.", "eV", "Blau", "Weiß", "Rot", "Grün", "1890", "1912",
	}

	names := []string{
		"TC Rot-Weiß Karlsruhe e.V.",
		"Post SV Nürnberg",
		"SV Blau-Weiß Bühlertal",
		"Heidelberger Tennis-Club 1890 e.V.",
		"DJK Sportgemeinschaft Wiesbaden",
	}

	for _, name := range names {
		for _, cand := range Candidates(name) {
			for _, bad := range forbidden {
				if strings.EqualFold(cand, bad) {
					t.Errorf("Candidates(%q) returned noise token %q", name, cand)
				}
			}
		}
	}
}

func TestCandidatesHandleDegenerateInput(t *testing.T) {
	tests := []string{
		"",
		"   ",
		"e.V.",
		"TC",
		"SV 1890",
		"1890",
		"...",
		"\u00a0\u00a0",
	}

	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			// Must not panic, and must not invent nonsense candidates.
			for _, c := range Candidates(in) {
				if len([]rune(c)) < 3 {
					t.Errorf("Candidates(%q) produced too-short candidate %q", in, c)
				}
			}
		})
	}
}

func TestCandidatesAreDeduplicated(t *testing.T) {
	got := Candidates("TC Karlsruhe Karlsruhe e.V.")

	seen := make(map[string]bool)
	for _, c := range got {
		key := strings.ToLower(c)
		if seen[key] {
			t.Errorf("Candidates() returned duplicate %q in %v", c, got)
		}
		seen[key] = true
	}
}

func TestDeadjective(t *testing.T) {
	tests := []struct {
		word string
		want []string // must all be present
	}{
		{"Heidelberger", []string{"Heidelberg"}},
		{"Ratinger", []string{"Ratingen"}},
		{"Karbener", []string{"Karben"}},
		{"Eppelheimer", []string{"Eppelheim"}},
		{"Lohausener", []string{"Lohausen"}},
	}

	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			got := Deadjective(tt.word)
			for _, w := range tt.want {
				if !containsCandidate(got, w) {
					t.Errorf("Deadjective(%q) = %v, want it to contain %q", tt.word, got, w)
				}
			}
		})
	}
}

func TestDeadjectiveIgnoresNonAdjectives(t *testing.T) {
	for _, word := range []string{"Neuss", "Erfurt", "TC", "der", "Bad", "Ulm", "SV"} {
		if got := Deadjective(word); got != nil {
			t.Errorf("Deadjective(%q) = %v, want nil", word, got)
		}
	}
}

func TestCandidatesIsDeterministic(t *testing.T) {
	// Map iteration must never leak into the output order, otherwise the
	// number of upstream requests would vary between runs.
	const name = "TC Grün-Weiß Mannheim-Neckarau 1920"

	first := Candidates(name)
	for i := 0; i < 50; i++ {
		got := Candidates(name)
		if !slices.Equal(first, got) {
			t.Fatalf("Candidates() is not deterministic:\n  run 0: %v\n  run %d: %v", first, i, got)
		}
	}
}
