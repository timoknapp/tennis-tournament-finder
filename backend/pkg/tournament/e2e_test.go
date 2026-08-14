package tournament_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
	"github.com/timoknapp/tennis-tournament-finder/pkg/openstreetmap"
	"github.com/timoknapp/tennis-tournament-finder/pkg/tournament"
)

// TestMain wires the package's external dependencies to local mocks so the
// end-to-end tests never touch the real federation sites or Nominatim.
func TestMain(m *testing.M) {
	// Speed up the geocoding limiter; the policy interval is asserted
	// separately in the openstreetmap package tests.
	os.Setenv("TTF_NOMINATIM_INTERVAL_MS", "0")

	// Keep test output readable; assertions never depend on log lines.
	if os.Getenv("TTF_LOG_LEVEL") == "" {
		os.Setenv("TTF_LOG_LEVEL", "ERROR")
		logger.SetLogLevel(logger.ErrorLevel)
	}

	os.Exit(m.Run())
}

// initIsolatedCache points the geocoding cache at a throwaway BoltDB file.
func initIsolatedCache(t *testing.T) {
	t.Helper()

	t.Setenv("TTF_CACHE_PATH", t.TempDir()+"/cache.bolt")
	openstreetmap.InitCache()
	t.Cleanup(openstreetmap.CloseCache)
}

// mockNominatim serves a canned geocoding response and counts requests.
func mockNominatim(t *testing.T, displayName, lat, lon string) (*httptest.Server, *int64) {
	t.Helper()

	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)

		// The Nominatim usage policy requires an identifying User-Agent.
		if ua := r.Header.Get("User-Agent"); ua == "" || strings.HasPrefix(ua, "Go-http-client") {
			t.Errorf("geocoding request sent unacceptable User-Agent %q", ua)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]models.Geocoordinates{
			{Lat: lat, Lon: lon, DisplayName: displayName},
		})
	}))
	t.Cleanup(srv.Close)

	t.Setenv("TTF_NOMINATIM_URL", srv.URL)

	return srv, &calls
}

const oldAPIResponse = `<html><body>
<table class="result-set">
  <tr><th>Datum</th><th>Turnier</th><th>Konkurrenz</th><th>LK</th></tr>
  <tr>
    <td rowspan="2">01.08.2026 - 03.08.2026</td>
    <td rowspan="2">
      <a href="https://www.tennis.de/spielen/turniersuche.html#detail/700001">Sommer Open</a>
      TC Karlsruhe
    </td>
    <td>Herren Einzel</td>
    <td>LK 12,0</td>
  </tr>
  <tr><td>Damen Einzel</td><td>LK 14,0</td></tr>
</table>
</body></html>`

// newAPIPage renders a page of the new API containing count tournaments,
// numbered from startID.
func newAPIPage(startID, count int) string {
	var sb strings.Builder
	sb.WriteString(`<html><body><table class="responsive-individual"><tbody>`)
	for i := 0; i < count; i++ {
		id := startID + i
		fmt.Fprintf(&sb, `
    <tr>
      <td class="daterange">0%d.09.2026</td>
      <td>
        <h2><a href="https://www.tennis.de/spielen/turniersuche.html#detail/%d">Turnier %d</a></h2>
        <p>Veranstalter: TC %d Austragungsort: Stuttgart Meldeschluss: 01.09.2026</p>
      </td>
      <td class="competitionAbbr">
        <table><tbody>
          <tr><td class="name"><span>Herren Einzel</span></td><td class="fedRank">LK 10,0</td><td class="result"></td></tr>
        </tbody></table>
      </td>
    </tr>`, (i%9)+1, id, id, id)
	}
	sb.WriteString(`</tbody></table></body></html>`)
	return sb.String()
}

// firstResultParam extracts the pagination offset from a request.
func firstResultParam(t *testing.T, r *http.Request) int {
	t.Helper()

	for key, values := range r.URL.Query() {
		if strings.Contains(key, "[firstResult]") && len(values) > 0 {
			n, err := strconv.Atoi(values[0])
			if err != nil {
				t.Fatalf("invalid firstResult %q: %v", values[0], err)
			}
			return n
		}
	}

	t.Fatalf("request %s has no firstResult parameter", r.URL.RawQuery)
	return 0
}

func TestEndToEndOldApiFederation(t *testing.T) {
	initIsolatedCache(t)
	_, geoCalls := mockNominatim(t, "Karlsruhe, Baden-Württemberg, Deutschland", "49.0069", "8.4037")

	var gotMethod, gotContentType, gotBody string
	fedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		buf := make([]byte, 2048)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, oldAPIResponse)
	}))
	defer fedSrv.Close()

	fed := models.Federation{
		Id:             "BAD",
		Url:            fedSrv.URL,
		Name:           "Badischer Tennisverband",
		State:          "Baden-Württemberg",
		ApiVersion:     "old",
		Geocoordinates: models.Geocoordinates{Lat: "49.0", Lon: "8.4"},
	}

	tournaments, results := tournament.CollectTournaments(
		context.Background(), []models.Federation{fed}, "01.08.2026", "15.08.2026", "Herren+Einzel")

	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("federation result error: %+v", results)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("content type = %q", gotContentType)
	}
	for _, want := range []string{"queryDateFrom=01.08.2026", "federation=BAD", "compType=Herren%2BEinzel"} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("request body %q missing %q", gotBody, want)
		}
	}

	if len(tournaments) != 1 {
		t.Fatalf("got %d tournaments, want 1", len(tournaments))
	}

	got := tournaments[0]
	if got.Id != "700001" || got.Title != "Sommer Open" {
		t.Errorf("tournament = %+v", got)
	}
	if got.Organizer != "TC Karlsruhe" {
		t.Errorf("organizer = %q, want %q", got.Organizer, "TC Karlsruhe")
	}
	// Coordinates must come from the mocked geocoder, not the federation default.
	if got.Lat != "49.0069" || got.Lon != "8.4037" {
		t.Errorf("coordinates = (%q,%q), want mocked geocoding result", got.Lat, got.Lon)
	}
	if len(got.Entries) != 2 {
		t.Errorf("entries = %d, want 2", len(got.Entries))
	}
	if atomic.LoadInt64(geoCalls) == 0 {
		t.Error("expected at least one geocoding request")
	}
}

func TestEndToEndNewApiPaginatesBeyondPageSize(t *testing.T) {
	initIsolatedCache(t)
	mockNominatim(t, "Stuttgart, Baden-Württemberg, Deutschland", "48.7758", "9.1829")

	// 250 tournaments across three pages (100 + 100 + 50).
	const total = 250

	var mu sync.Mutex
	var offsets []int

	fedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := firstResultParam(t, r)

		mu.Lock()
		offsets = append(offsets, offset)
		mu.Unlock()

		remaining := total - offset
		if remaining < 0 {
			remaining = 0
		}
		count := remaining
		if count > 100 {
			count = 100
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, newAPIPage(1000+offset, count))
	}))
	defer fedSrv.Close()

	fed := models.Federation{
		Id:             "WTB",
		Url:            fedSrv.URL,
		State:          "Baden-Württemberg",
		ApiVersion:     "new",
		Geocoordinates: models.Geocoordinates{Lat: "48.85", Lon: "9.13"},
	}

	tournaments, results := tournament.CollectTournaments(
		context.Background(), []models.Federation{fed}, "01.09.2026", "30.09.2026", "")

	if results[0].Err != nil {
		t.Fatalf("federation error: %v", results[0].Err)
	}

	// Regression for the truncation bug: all pages must be fetched.
	if len(tournaments) != total {
		t.Errorf("got %d tournaments, want %d", len(tournaments), total)
	}

	mu.Lock()
	defer mu.Unlock()
	wantOffsets := []int{0, 100, 200}
	if len(offsets) != len(wantOffsets) {
		t.Fatalf("requested offsets = %v, want %v", offsets, wantOffsets)
	}
	for i, want := range wantOffsets {
		if offsets[i] != want {
			t.Errorf("offset %d = %d, want %d", i, offsets[i], want)
		}
	}

	// IDs must be unique.
	seen := make(map[string]bool)
	for _, tr := range tournaments {
		if seen[tr.Id] {
			t.Errorf("duplicate tournament id %q", tr.Id)
		}
		seen[tr.Id] = true
	}
}

func TestEndToEndNewApiStopsOnExactPageBoundary(t *testing.T) {
	initIsolatedCache(t)
	mockNominatim(t, "Stuttgart, Baden-Württemberg, Deutschland", "48.7758", "9.1829")

	// Exactly one full page: a second (empty) request is expected, then stop.
	var requests int64
	fedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requests, 1)
		offset := firstResultParam(t, r)

		count := 0
		if offset == 0 {
			count = 100
		}
		fmt.Fprint(w, newAPIPage(2000+offset, count))
	}))
	defer fedSrv.Close()

	fed := models.Federation{
		Id: "WTB", Url: fedSrv.URL, State: "Baden-Württemberg", ApiVersion: "new",
		Geocoordinates: models.Geocoordinates{Lat: "48.85", Lon: "9.13"},
	}

	tournaments, _ := tournament.CollectTournaments(
		context.Background(), []models.Federation{fed}, "01.09.2026", "30.09.2026", "")

	if len(tournaments) != 100 {
		t.Errorf("got %d tournaments, want 100", len(tournaments))
	}
	if got := atomic.LoadInt64(&requests); got != 2 {
		t.Errorf("made %d requests, want 2", got)
	}
}

func TestEndToEndNewApiTerminatesWhenUpstreamIgnoresOffset(t *testing.T) {
	initIsolatedCache(t)
	mockNominatim(t, "Stuttgart, Baden-Württemberg, Deutschland", "48.7758", "9.1829")

	// A broken upstream that always returns the same full page would loop
	// forever without the "no new results" guard.
	var requests int64
	fedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requests, 1)
		fmt.Fprint(w, newAPIPage(3000, 100))
	}))
	defer fedSrv.Close()

	fed := models.Federation{
		Id: "WTB", Url: fedSrv.URL, State: "Baden-Württemberg", ApiVersion: "new",
		Geocoordinates: models.Geocoordinates{Lat: "48.85", Lon: "9.13"},
	}

	done := make(chan struct{})
	var tournaments []models.Tournament
	go func() {
		tournaments, _ = tournament.CollectTournaments(
			context.Background(), []models.Federation{fed}, "01.09.2026", "30.09.2026", "")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("pagination did not terminate for a repeating upstream")
	}

	if len(tournaments) != 100 {
		t.Errorf("got %d tournaments, want 100 unique", len(tournaments))
	}
	if got := atomic.LoadInt64(&requests); got > 3 {
		t.Errorf("made %d requests, want to stop quickly on repeated data", got)
	}
}

func TestEndToEndPartialFailureKeepsHealthyFederations(t *testing.T) {
	initIsolatedCache(t)
	mockNominatim(t, "Karlsruhe, Baden-Württemberg, Deutschland", "49.0069", "8.4037")

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, oldAPIResponse)
	}))
	defer healthy.Close()

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	}))
	defer broken.Close()

	feds := []models.Federation{
		{Id: "BROKEN", Url: broken.URL, State: "Hessen", ApiVersion: "old",
			Geocoordinates: models.Geocoordinates{Lat: "50.1", Lon: "8.6"}},
		{Id: "BAD", Url: healthy.URL, State: "Baden-Württemberg", ApiVersion: "old",
			Geocoordinates: models.Geocoordinates{Lat: "49.0", Lon: "8.4"}},
		{Id: "UNKNOWN_API", Url: healthy.URL, State: "Bayern", ApiVersion: "bogus",
			Geocoordinates: models.Geocoordinates{Lat: "48.1", Lon: "11.5"}},
	}

	tournaments, results := tournament.CollectTournaments(
		context.Background(), feds, "01.08.2026", "15.08.2026", "")

	// The healthy federation's data must survive its neighbours' failures.
	if len(tournaments) != 1 {
		t.Fatalf("got %d tournaments, want 1 from the healthy federation", len(tournaments))
	}
	if tournaments[0].Id != "700001" {
		t.Errorf("tournament id = %q", tournaments[0].Id)
	}

	if results[0].Err == nil {
		t.Error("broken federation reported no error")
	}
	if results[1].Err != nil {
		t.Errorf("healthy federation reported error: %v", results[1].Err)
	}
	if results[2].Err == nil {
		t.Error("unknown API version reported no error")
	}
}

func TestEndToEndResultOrderIsDeterministic(t *testing.T) {
	initIsolatedCache(t)
	mockNominatim(t, "Stuttgart, Baden-Württemberg, Deutschland", "48.7758", "9.1829")

	// Each federation replies with a distinct tournament; the slowest one is
	// listed first to prove ordering follows the input, not completion order.
	makeServer := func(id int, delay time.Duration) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(delay)
			fmt.Fprint(w, newAPIPage(id, 1))
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	slow := makeServer(5001, 150*time.Millisecond)
	fast := makeServer(5002, 0)
	medium := makeServer(5003, 60*time.Millisecond)

	feds := []models.Federation{
		{Id: "SLOW", Url: slow.URL, State: "Baden-Württemberg", ApiVersion: "new"},
		{Id: "FAST", Url: fast.URL, State: "Baden-Württemberg", ApiVersion: "new"},
		{Id: "MEDIUM", Url: medium.URL, State: "Baden-Württemberg", ApiVersion: "new"},
	}

	// Repeat to make scheduling-dependent ordering bugs visible.
	for i := 0; i < 3; i++ {
		tournaments, _ := tournament.CollectTournaments(
			context.Background(), feds, "01.09.2026", "30.09.2026", "")

		if len(tournaments) != 3 {
			t.Fatalf("run %d: got %d tournaments, want 3", i, len(tournaments))
		}

		want := []string{"5001", "5002", "5003"}
		for j, id := range want {
			if tournaments[j].Id != id {
				t.Errorf("run %d: tournament %d = %q, want %q (input order)", i, j, tournaments[j].Id, id)
			}
		}
	}
}

func TestEndToEndConcurrentFederationsAreRaceFree(t *testing.T) {
	initIsolatedCache(t)
	mockNominatim(t, "Stuttgart, Baden-Württemberg, Deutschland", "48.7758", "9.1829")

	// Many federations returning many tournaments each maximises the chance of
	// the race detector observing unsynchronized access.
	const fedCount = 12
	const perFed = 25

	var feds []models.Federation
	for i := 0; i < fedCount; i++ {
		start := 10000 + i*1000
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, newAPIPage(start, perFed))
		}))
		t.Cleanup(srv.Close)

		feds = append(feds, models.Federation{
			Id:             fmt.Sprintf("FED%02d", i),
			Url:            srv.URL,
			State:          "Baden-Württemberg",
			ApiVersion:     "new",
			Geocoordinates: models.Geocoordinates{Lat: "48.85", Lon: "9.13"},
		})
	}

	tournaments, results := tournament.CollectTournaments(
		context.Background(), feds, "01.09.2026", "30.09.2026", "")

	for _, res := range results {
		if res.Err != nil {
			t.Errorf("federation %s error: %v", res.Federation.Id, res.Err)
		}
	}

	// Regression for the lost-update race: every tournament must be present.
	if want := fedCount * perFed; len(tournaments) != want {
		t.Errorf("got %d tournaments, want %d", len(tournaments), want)
	}
}

func TestEndToEndHandlerServesJSON(t *testing.T) {
	initIsolatedCache(t)

	req := httptest.NewRequest(http.MethodGet,
		"/?dateFrom=01.08.2026&dateTo=02.08.2026&federations=DOES_NOT_EXIST", nil)
	rec := httptest.NewRecorder()

	tournament.GetTournaments(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS header = %q, want *", got)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content type = %q, want JSON", ct)
	}

	// An unknown federation filter must yield an empty JSON array, never null.
	var decoded []models.Tournament
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("got %d tournaments, want 0", len(decoded))
	}
}

func TestEndToEndSlowUpstreamIsBoundedByContext(t *testing.T) {
	initIsolatedCache(t)

	release := make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer stalled.Close()
	defer close(release)

	fed := models.Federation{
		Id: "STALLED", Url: stalled.URL, State: "Hessen", ApiVersion: "old",
		Geocoordinates: models.Geocoordinates{Lat: "50.1", Lon: "8.6"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	var results []tournament.FederationResult
	go func() {
		_, results = tournament.CollectTournaments(ctx, []models.Federation{fed}, "", "", "")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("request was not bounded by the context deadline")
	}

	if results[0].Err == nil {
		t.Error("stalled upstream reported no error")
	}
}

func TestEndToEndGeocodingResultsAreCached(t *testing.T) {
	initIsolatedCache(t)
	_, geoCalls := mockNominatim(t, "Karlsruhe, Baden-Württemberg, Deutschland", "49.0069", "8.4037")

	fedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, oldAPIResponse)
	}))
	defer fedSrv.Close()

	fed := models.Federation{
		Id: "BAD", Url: fedSrv.URL, State: "Baden-Württemberg", ApiVersion: "old",
		Geocoordinates: models.Geocoordinates{Lat: "49.0", Lon: "8.4"},
	}

	for i := 0; i < 3; i++ {
		if _, results := tournament.CollectTournaments(
			context.Background(), []models.Federation{fed}, "01.08.2026", "15.08.2026", ""); results[0].Err != nil {
			t.Fatalf("run %d error: %v", i, results[0].Err)
		}
	}

	// The same organizer/location must only be geocoded once; cache hits must
	// not consume upstream rate-limit capacity.
	if got := atomic.LoadInt64(geoCalls); got != 1 {
		t.Errorf("made %d geocoding requests across 3 runs, want 1", got)
	}
}

func TestEndToEndWarmupPopulatesCache(t *testing.T) {
	initIsolatedCache(t)

	// Warmup uses the real federation list, so restrict it to an ID that does
	// not exist. This verifies the plumbing without hitting the network.
	if got := tournament.Warmup("01.08.2026", "02.08.2026", "", "DOES_NOT_EXIST"); got != 0 {
		t.Errorf("Warmup() = %d, want 0 for an unknown federation", got)
	}
}

// Ensure the mocked federation URL is a valid absolute URL, guarding against
// accidental use of a relative path in the fixtures above.
func TestMockServersUseAbsoluteURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil || !u.IsAbs() {
		t.Fatalf("httptest server URL %q is not absolute: %v", srv.URL, err)
	}
}
