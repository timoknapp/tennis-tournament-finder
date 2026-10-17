package federation

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TestFederationsAreWellFormed guards the configuration itself: a typo here
// silently removes a whole federation's tournaments from the map.
func TestFederationsAreWellFormed(t *testing.T) {
	federations := GetFederations()

	if len(federations) == 0 {
		t.Fatal("GetFederations() returned no federations")
	}

	seenIDs := make(map[string]bool)

	for _, fed := range federations {
		t.Run(fed.Id, func(t *testing.T) {
			if fed.Id == "" {
				t.Error("federation has an empty Id")
			}
			if seenIDs[fed.Id] {
				t.Errorf("duplicate federation Id %q", fed.Id)
			}
			seenIDs[fed.Id] = true

			if fed.Name == "" {
				t.Error("federation has an empty Name")
			}

			u, err := url.Parse(fed.Url)
			if err != nil {
				t.Errorf("invalid Url %q: %v", fed.Url, err)
			} else {
				if u.Scheme != "https" {
					t.Errorf("Url %q must use https", fed.Url)
				}
				if u.Host == "" {
					t.Errorf("Url %q has no host", fed.Url)
				}
			}

			switch fed.ApiVersion {
			case "old":
				if fed.TrustedProperties != "" {
					t.Error("old API federations do not use TrustedProperties")
				}
			case "new":
				if fed.TrustedProperties == "" {
					t.Error("new API federations require TrustedProperties")
				}
			case "btv":
				// Bavaria runs its own ZK widget instead of nuLiga.
				if fed.TrustedProperties != "" {
					t.Error("the BTV widget does not use TrustedProperties")
				}
			default:
				t.Errorf("unknown ApiVersion %q", fed.ApiVersion)
			}

			// Default coordinates are the fallback pin, so they must be set
			// and inside Germany's bounding box.
			assertGermanCoordinates(t, fed.Geocoordinates.Lat, fed.Geocoordinates.Lon)

			if fed.State == "" {
				t.Error("federation has an empty State")
			}
			if len(fed.States) > 0 && !contains(fed.States, fed.State) {
				t.Errorf("States %v must include the primary State %q", fed.States, fed.State)
			}
		})
	}
}

// TestAcceptedStates covers the multi-state support that keeps tournaments in
// a federation's secondary state from falling back to default coordinates.
func TestAcceptedStates(t *testing.T) {
	byID := make(map[string][]string)
	for _, fed := range GetFederations() {
		byID[fed.Id] = fed.AcceptedStates()
	}

	tests := []struct {
		id   string
		want []string
	}{
		{"TVBB", []string{"Berlin", "Brandenburg"}},
		{"TNB", []string{"Niedersachsen", "Bremen"}},
		{"BAD", []string{"Baden-Württemberg"}},
		{"HAM", []string{"Hamburg"}},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got, ok := byID[tt.id]
			if !ok {
				t.Fatalf("federation %q is not configured", tt.id)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("AcceptedStates() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("AcceptedStates()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestExpectedFederationsArePresent documents the supported set, so removing
// one by accident fails the build rather than quietly shrinking coverage.
func TestExpectedFederationsArePresent(t *testing.T) {
	want := []string{
		// Previously supported
		"BAD", "HTV", "RLP", "STV", "TMV", "TSA", "TTV", "TVN", "WTB",
		// Added for issue #49
		"TVBB", "HAM", "TVM", "TNB", "STB", "WTV",
		// Completing #49: Schleswig-Holstein (nuLiga code SLH) and Bavaria
		// (own widget).
		"SLH", "BTV",
	}

	got := make(map[string]bool)
	for _, fed := range GetFederations() {
		got[fed.Id] = true
	}

	for _, id := range want {
		if !got[id] {
			t.Errorf("federation %q is missing", id)
		}
	}
}

// TestLigaNuFederationsUseTournamentCalendarPath catches copy/paste mistakes in
// the endpoint, which would otherwise surface only as an empty result set.
func TestLigaNuFederationsUseTournamentCalendarPath(t *testing.T) {
	for _, fed := range GetFederations() {
		if !strings.Contains(fed.Url, "liga.nu") {
			continue
		}
		if !strings.HasSuffix(fed.Url, "/wa/tournamentCalendar") {
			t.Errorf("federation %s: liga.nu URL %q should end in /wa/tournamentCalendar",
				fed.Id, fed.Url)
		}
		if fed.ApiVersion != "old" {
			t.Errorf("federation %s: liga.nu endpoints use the old API, got %q",
				fed.Id, fed.ApiVersion)
		}
	}
}

func assertGermanCoordinates(t *testing.T, lat, lon string) {
	t.Helper()

	if lat == "" || lon == "" {
		t.Error("federation has no default coordinates")
		return
	}

	latF, err := strconv.ParseFloat(lat, 64)
	if err != nil {
		t.Errorf("invalid latitude %q: %v", lat, err)
		return
	}
	lonF, err := strconv.ParseFloat(lon, 64)
	if err != nil {
		t.Errorf("invalid longitude %q: %v", lon, err)
		return
	}

	// Germany spans roughly 47.2-55.1 N and 5.8-15.1 E.
	if latF < 47.0 || latF > 55.5 {
		t.Errorf("latitude %v is outside Germany", latF)
	}
	if lonF < 5.5 || lonF > 15.5 {
		t.Errorf("longitude %v is outside Germany", lonF)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// TestDefaultsCollidingWithCitiesAreDocumented records a trap rather than a
// requirement.
//
// A federation's default coordinates are its fallback for tournaments whose
// location cannot be determined. Several of those defaults sit exactly on a
// major city: BTV's is München and WTV's is Dortmund, to seven decimal places.
//
// That means a club in München geocodes correctly and still lands on the exact
// coordinates BTV uses when it fails. Any code that infers "this did not
// resolve" by comparing against the default will report those clubs as broken.
// A live sweep did exactly that: 17 of 32 reported failures were tournaments
// that had resolved perfectly well.
//
// The fix is to record the outcome where it is known, which is what
// models.Tournament.ApproximateLocation now does. This test exists so the next
// person meeting the same idea finds the reason it does not work.
func TestDefaultsCollidingWithCitiesAreDocumented(t *testing.T) {
	// Verified against Nominatim; the value is the city each default lands on.
	knownCollisions := map[string]string{
		"BTV": "München",
		"WTV": "Dortmund",
	}

	byID := make(map[string]bool)
	for _, fed := range GetFederations() {
		byID[fed.Id] = true
	}

	for id := range knownCollisions {
		if !byID[id] {
			t.Errorf("federation %s disappeared; re-check whether the collision note still applies", id)
		}
	}
}
