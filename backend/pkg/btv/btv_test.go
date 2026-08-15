package btv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
)

var testFederation = models.Federation{
	Id:             "BTV",
	Name:           "Bayerischer Tennis-Verband",
	State:          "Bayern",
	ApiVersion:     "btv",
	Geocoordinates: models.Geocoordinates{Lat: "48.13", Lon: "11.57"},
}

func loadFixture(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "grid_response.txt"))
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	return string(data)
}

// TestParseGridFixture runs against a recorded response from the live widget,
// so a change in the upstream markup shows up as a test failure rather than as
// silently missing tournaments.
func TestParseGridFixture(t *testing.T) {
	tournaments := ParseGrid(loadFixture(t), testFederation)

	if len(tournaments) == 0 {
		t.Fatal("no tournaments parsed from the fixture")
	}

	first := tournaments[0]

	if first.Title != "Bad Füssing Masters by npsports.de" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.Date != "10.08. - 16.08.2026" {
		t.Errorf("Date = %q", first.Date)
	}
	if first.Location != "Bad Füssing" {
		t.Errorf("Location = %q, want the venue city", first.Location)
	}
	// BTV publishes no organizer; the city stands in so the map popup and the
	// Google Maps link still work.
	if first.Organizer != "Bad Füssing" {
		t.Errorf("Organizer = %q, want the venue city as a stand-in", first.Organizer)
	}
	if first.Id != "759920" {
		t.Errorf("Id = %q, want the id from the tennis.de detail link", first.Id)
	}
	if !strings.HasPrefix(first.URL, "https://www.tennis.de/") {
		t.Errorf("URL = %q, want the tennis.de detail link", first.URL)
	}

	if len(first.Entries) == 0 {
		t.Fatal("no competitions parsed")
	}
	// "W30/E" must be expanded rather than passed through raw.
	if got := first.Entries[0].Competition; got != "Damen 30 Einzel" {
		t.Errorf("first competition = %q, want %q", got, "Damen 30 Einzel")
	}
}

func TestParseGridExtractsEveryRow(t *testing.T) {
	tournaments := ParseGrid(loadFixture(t), testFederation)

	// The fixture holds five rows; every one must yield a tournament.
	if len(tournaments) != 5 {
		t.Fatalf("parsed %d tournaments, want 5", len(tournaments))
	}

	seen := make(map[string]bool)
	for _, tr := range tournaments {
		if tr.Title == "" {
			t.Error("tournament without a title")
		}
		if tr.Id == "" {
			t.Errorf("tournament %q has no id", tr.Title)
		}
		if seen[tr.Id] {
			t.Errorf("duplicate tournament id %q", tr.Id)
		}
		seen[tr.Id] = true
	}
}

func TestParseGridHandlesMalformedInput(t *testing.T) {
	for _, name := range []string{"empty", "not zk", "truncated row", "no rows"} {
		t.Run(name, func(t *testing.T) {
			inputs := map[string]string{
				"empty":         "",
				"not zk":        "%%% not a zk payload %%%",
				"truncated row": `['zul.grid.Row','x',{},{},[ ['zul.wgt.Label','y',{value:'10.08.`,
				"no rows":       `{"rs":[],"rid":1}`,
			}

			// Must not panic and must not invent tournaments.
			got := ParseGrid(inputs[name], testFederation)
			if len(got) != 0 {
				t.Errorf("got %d tournaments, want 0", len(got))
			}
		})
	}
}

func TestExpandCompetition(t *testing.T) {
	tests := []struct{ in, want string }{
		{"W30/E", "Damen 30 Einzel"},
		{"M40/D", "Herren 40 Doppel"},
		{"X00/D", "Mixed Doppel"},
		{"W00/E", "Damen Einzel"},
		{"M00/E", "Herren Einzel"},
		{"M85/E", "Herren 85 Einzel"},
		{"W12/E", "Damen 12 Einzel"},

		// Unusable codes must be dropped rather than guessed at.
		{"", ""},
		{"XX", ""},
		{"Q30/E", ""},
		{"W30/X", ""},
		{"W30", ""},
		{"/E", ""},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := expandCompetition(tt.in); got != tt.want {
				t.Errorf("expandCompetition(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnescapeZK(t *testing.T) {
	tests := []struct{ in, want string }{
		{`Bad F\xFCssing`, "Bad Füssing"},
		{`Gr\xF6benzell`, "Gröbenzell"},
		{`plain`, "plain"},
		{`\xZZ`, `\xZZ`}, // invalid escape stays as-is
		{``, ``},
	}

	for _, tt := range tests {
		if got := unescapeZK(tt.in); got != tt.want {
			t.Errorf("unescapeZK(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveURL(t *testing.T) {
	tests := []struct {
		base, ref, want string
	}{
		{
			"https://btv-prod.burdadigitalsystems.de/btvtrnsearch/",
			"/btvtrnsearch/zkau;jsessionid=ABC",
			"https://btv-prod.burdadigitalsystems.de/btvtrnsearch/zkau;jsessionid=ABC",
		},
		{
			"https://example.test/widget/",
			"zkau",
			"https://example.test/widget/zkau",
		},
	}

	for _, tt := range tests {
		got, err := resolveURL(tt.base, tt.ref)
		if err != nil {
			t.Fatalf("resolveURL(%q, %q) error = %v", tt.base, tt.ref, err)
		}
		if got != tt.want {
			t.Errorf("resolveURL(%q, %q) = %q, want %q", tt.base, tt.ref, got, tt.want)
		}
	}
}

// TestGetTournamentsAgainstMockWidget exercises the full two-step ZK
// conversation without touching the network.
func TestGetTournamentsAgainstMockWidget(t *testing.T) {
	fixture := loadFixture(t)

	var gotBootstrap, gotUpdate bool
	var forms []string
	var sentCookie string

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/btvtrnsearch/zkau", func(w http.ResponseWriter, r *http.Request) {
		gotUpdate = true
		if c, err := r.Cookie("JSESSIONID"); err == nil {
			sentCookie = c.Value
		}
		body := make([]byte, 4096)
		n, _ := r.Body.Read(body)
		forms = append(forms, string(body[:n]))

		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(fixture))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotBootstrap = true
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "TESTSESSION"})
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script>
zk.themeName='iceblue';zkmx(
[0,'zQbU_',{dt:'z_test123',cu:'\x2Fbtvtrnsearch',uu:'\x2Fbtvtrnsearch\x2Fzkau',rsu:'x'}]
)</script></body></html>`))
	})

	client := New(srv.URL + "/")
	tournaments, err := client.GetTournaments(context.Background(), testFederation)
	if err != nil {
		t.Fatalf("GetTournaments() error = %v", err)
	}

	if !gotBootstrap {
		t.Error("bootstrap request was not made")
	}
	if !gotUpdate {
		t.Error("update request was not made")
	}
	if sentCookie != "TESTSESSION" {
		t.Errorf("session cookie = %q, want it forwarded to the update request", sentCookie)
	}
	if len(forms) == 0 {
		t.Fatal("no update request was recorded")
	}
	if !strings.Contains(forms[0], "dtid=z_test123") {
		t.Errorf("update form %q does not carry the desktop id", forms[0])
	}
	if !strings.Contains(forms[0], "cmd_0=onClientInfo") {
		t.Errorf("first update form %q does not carry the grid command", forms[0])
	}

	// The fixture advertises 18 pages, so the client must page through them
	// rather than stopping at the first 10 rows.
	if len(forms) < 2 {
		t.Fatalf("made %d update requests, want the client to request further pages", len(forms))
	}
	if !strings.Contains(forms[1], "cmd_0=onPaging") {
		t.Errorf("second update form %q is not a paging request", forms[1])
	}

	// Every page returns the same fixture, so deduplication must collapse them.
	if len(tournaments) != 5 {
		t.Errorf("got %d tournaments, want 5 after deduplication", len(tournaments))
	}
}

// TestPaginationStopsWhenPagesRepeat guards against an endless loop if the
// widget ever stops honouring the page offset.
func TestPaginationStopsWhenPagesRepeat(t *testing.T) {
	fixture := loadFixture(t)

	var updates int
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/btvtrnsearch/zkau", func(w http.ResponseWriter, r *http.Request) {
		updates++
		_, _ = w.Write([]byte(fixture))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "S"})
		_, _ = w.Write([]byte(`<script>zkmx([0,'u',{dt:'z_1',uu:'\x2Fbtvtrnsearch\x2Fzkau'}])</script>`))
	})

	client := New(srv.URL + "/")
	if _, err := client.GetTournaments(context.Background(), testFederation); err != nil {
		t.Fatalf("GetTournaments() error = %v", err)
	}

	// One grid request plus one paging request that adds nothing new.
	if updates > 3 {
		t.Errorf("made %d update requests; pagination should stop once a page adds nothing", updates)
	}
}

func TestParsePaging(t *testing.T) {
	payload := `['zul.mesh.Paging','abc123',{$onPaging:true,totalSize:180,pageSize:10,pageCount:18,detailed:true}`

	uuid, count := parsePaging(payload)
	if uuid != "abc123" {
		t.Errorf("uuid = %q, want abc123", uuid)
	}
	if count != 18 {
		t.Errorf("pageCount = %d, want 18", count)
	}

	if uuid, count := parsePaging("no paging widget here"); uuid != "" || count != 0 {
		t.Errorf("parsePaging(no match) = %q, %d; want empty", uuid, count)
	}
}

func TestGetTournamentsReportsUpstreamFailures(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "bootstrap 500",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			},
		},
		{
			name: "no zk bootstrap in markup",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("<html><body>maintenance</body></html>"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			client := New(srv.URL + "/")
			if _, err := client.GetTournaments(context.Background(), testFederation); err == nil {
				t.Error("GetTournaments() returned nil error for a broken upstream")
			}
		})
	}
}

func TestGetTournamentsRespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := New(srv.URL + "/")
	if _, err := client.GetTournaments(ctx, testFederation); err == nil {
		t.Error("GetTournaments() with a cancelled context returned nil error")
	}
}

// TestZKSequenceIncrements is the regression test for a subtle protocol
// requirement: ZK expects a monotonically increasing ZK-SID per desktop.
// Sending a constant value makes the server treat later requests as duplicates
// and replay the first response, so pagination silently returned page 0 forever
// and Bavaria appeared to have only 10 tournaments instead of 180.
func TestZKSequenceIncrements(t *testing.T) {
	fixture := loadFixture(t)

	var sids []string
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/btvtrnsearch/zkau", func(w http.ResponseWriter, r *http.Request) {
		sids = append(sids, r.Header.Get("ZK-SID"))
		_, _ = w.Write([]byte(fixture))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "S"})
		_, _ = w.Write([]byte(`<script>zkmx([0,'u',{dt:'z_1',uu:'\x2Fbtvtrnsearch\x2Fzkau'}])</script>`))
	})

	client := New(srv.URL + "/")
	if _, err := client.GetTournaments(context.Background(), testFederation); err != nil {
		t.Fatalf("GetTournaments() error = %v", err)
	}

	if len(sids) < 2 {
		t.Fatalf("recorded %d requests, want at least 2", len(sids))
	}

	for i, sid := range sids {
		want := strconv.Itoa(i + 1)
		if sid != want {
			t.Errorf("request %d sent ZK-SID %q, want %q", i, sid, want)
		}
	}
}
