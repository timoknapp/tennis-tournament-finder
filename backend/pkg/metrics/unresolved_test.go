package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/timoknapp/tennis-tournament-finder/pkg/unresolved"
)

func TestUnresolvedClubsHandlerReportsRecordedClubs(t *testing.T) {
	unresolved.Reset()
	unresolved.Record("Lohausener Sport-Verein", "TVM", "Nordrhein-Westfalen", []string{"Lohausener"})
	unresolved.Record("Lohausener Sport-Verein", "TVM", "Nordrhein-Westfalen", []string{"Lohausener"})
	unresolved.Record("TC Beispiel", "BAD", "Baden-Württemberg", nil)

	rec := httptest.NewRecorder()
	UnresolvedClubsHandler(rec, httptest.NewRequest(http.MethodGet, UnresolvedClubsPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected JSON, got %q", ct)
	}

	var got struct {
		Count int `json:"count"`
		Clubs []struct {
			Organizer  string `json:"organizer"`
			Federation string `json:"federation"`
			Count      int    `json:"count"`
		} `json:"clubs"`
		Stubs []struct {
			Contains string `json:"contains"`
			City     string `json:"city"`
			State    string `json:"state"`
			Note     string `json:"note"`
		} `json:"club_locations_stubs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if got.Count != 2 {
		t.Errorf("expected 2 clubs, got %d", got.Count)
	}
	// Most affected first, so the entry worth fixing leads.
	if got.Clubs[0].Organizer != "Lohausener Sport-Verein" || got.Clubs[0].Count != 2 {
		t.Errorf("expected the most affected club first, got %+v", got.Clubs[0])
	}
}

func TestStubsArePasteableIntoClubLocations(t *testing.T) {
	unresolved.Reset()
	unresolved.Record("Lohausener Sport-Verein", "TVM", "Nordrhein-Westfalen", []string{"Lohausener"})

	rec := httptest.NewRecorder()
	UnresolvedClubsHandler(rec, httptest.NewRequest(http.MethodGet, UnresolvedClubsPath, nil))

	var got struct {
		Stubs []map[string]string `json:"club_locations_stubs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(got.Stubs) != 1 {
		t.Fatalf("expected 1 stub, got %d", len(got.Stubs))
	}

	stub := got.Stubs[0]
	// The organizer must survive verbatim: it is the key the override matches
	// on, so any reformatting here would produce a rule that never fires.
	if stub["contains"] != "Lohausener Sport-Verein" {
		t.Errorf("organizer was altered: %q", stub["contains"])
	}
	// TODO rather than a guess: a wrong city is worse than an obvious gap,
	// because it looks resolved.
	if stub["city"] != "TODO" {
		t.Errorf("expected the city to be left as TODO, got %q", stub["city"])
	}
	if stub["state"] != "Nordrhein-Westfalen" {
		t.Errorf("expected the federation state to be carried over, got %q", stub["state"])
	}
	if !strings.Contains(stub["note"], "Lohausener") {
		t.Errorf("expected the note to record what was tried, got %q", stub["note"])
	}
}

func TestUnresolvedClubsHandlerWithNothingRecorded(t *testing.T) {
	unresolved.Reset()

	rec := httptest.NewRecorder()
	UnresolvedClubsHandler(rec, httptest.NewRequest(http.MethodGet, UnresolvedClubsPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var got struct {
		Count int               `json:"count"`
		Clubs []json.RawMessage `json:"clubs"`
		Stubs []json.RawMessage `json:"club_locations_stubs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if got.Count != 0 {
		t.Errorf("expected an empty report, got %d", got.Count)
	}
	// Empty arrays rather than null, so a consumer can iterate unconditionally.
	if got.Clubs == nil || got.Stubs == nil {
		t.Error("expected empty arrays rather than null")
	}
}

func TestUnresolvedClubsHandlerRejectsNonGet(t *testing.T) {
	rec := httptest.NewRecorder()
	UnresolvedClubsHandler(rec, httptest.NewRequest(http.MethodPost, UnresolvedClubsPath, nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
		t.Errorf("expected an Allow header of GET, got %q", allow)
	}
}
