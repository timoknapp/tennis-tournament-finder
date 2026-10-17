package openstreetmap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
)

// TestMain keeps test output readable; assertions never depend on log lines.
func TestMain(m *testing.M) {
	if os.Getenv("TTF_LOG_LEVEL") == "" {
		os.Setenv("TTF_LOG_LEVEL", "ERROR")
		logger.SetLogLevel(logger.ErrorLevel)
	}

	os.Exit(m.Run())
}

// initTestCache points the cache at a throwaway BoltDB file and resets the
// package-level state between tests.
func initTestCache(t *testing.T) {
	t.Helper()

	t.Setenv("TTF_CACHE_PATH", t.TempDir()+"/cache.bolt")
	t.Setenv("TTF_NOMINATIM_INTERVAL_MS", "0")

	InitCache()
	t.Cleanup(CloseCache)
}

// mockGeocodingServer serves a canned response and records the requests it saw.
type mockGeocodingServer struct {
	server    *httptest.Server
	calls     int64
	mu        sync.Mutex
	userAgent string
	queries   []string
	times     []time.Time
}

func newMockGeocodingServer(t *testing.T, handler http.HandlerFunc) *mockGeocodingServer {
	t.Helper()

	m := &mockGeocodingServer{}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&m.calls, 1)

		m.mu.Lock()
		m.userAgent = r.Header.Get("User-Agent")
		m.queries = append(m.queries, r.URL.Query().Get("q"))
		m.times = append(m.times, time.Now())
		m.mu.Unlock()

		handler(w, r)
	}))
	t.Cleanup(m.server.Close)

	t.Setenv("TTF_NOMINATIM_URL", m.server.URL)

	return m
}

func (m *mockGeocodingServer) callCount() int64 { return atomic.LoadInt64(&m.calls) }

func (m *mockGeocodingServer) lastUserAgent() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userAgent
}

func (m *mockGeocodingServer) recordedQueries() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.queries))
	copy(out, m.queries)
	return out
}

// respondWith writes a Nominatim-style JSON array.
func respondWith(coords ...models.Geocoordinates) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coords)
	}
}

func TestGeocodingSendsIdentifyingUserAgent(t *testing.T) {
	initTestCache(t)

	mock := newMockGeocodingServer(t, respondWith(models.Geocoordinates{
		Lat: "49.0069", Lon: "8.4037", DisplayName: "Karlsruhe, Baden-Württemberg, Deutschland",
	}))

	got := GetGeocoordinatesFromCache("Baden-Württemberg", models.Tournament{
		Id: "1", Location: "Karlsruhe", Organizer: "TC Karlsruhe",
	})

	if got.Lat != "49.0069" {
		t.Errorf("Lat = %q, want 49.0069", got.Lat)
	}

	ua := mock.lastUserAgent()
	if ua == "" {
		t.Fatal("no User-Agent sent")
	}
	// The Nominatim policy rejects generic/default agents.
	if strings.HasPrefix(ua, "Go-http-client") {
		t.Errorf("User-Agent = %q, want an application-specific agent", ua)
	}
	if !strings.Contains(ua, "TennisTournamentFinder") {
		t.Errorf("User-Agent = %q, want it to identify the application", ua)
	}
	// A contact/project URL lets upstream operators reach the maintainer.
	if !strings.Contains(ua, "http") {
		t.Errorf("User-Agent = %q, want it to include a contact URL", ua)
	}
}

func TestGeocodingUserAgentIsConfigurable(t *testing.T) {
	initTestCache(t)
	t.Setenv("TTF_USER_AGENT", "MyFork/2.0 (+https://example.test/contact)")

	mock := newMockGeocodingServer(t, respondWith(models.Geocoordinates{
		Lat: "1", Lon: "2", DisplayName: "Hessen",
	}))

	GetGeocoordinatesFromCache("Hessen", models.Tournament{Id: "1", Location: "Frankfurt"})

	if got := mock.lastUserAgent(); got != "MyFork/2.0 (+https://example.test/contact)" {
		t.Errorf("User-Agent = %q, want the configured override", got)
	}
}

func TestGeocodingRespectsRateLimit(t *testing.T) {
	initTestCache(t)
	// Restore the policy-compliant interval for this test.
	t.Setenv("TTF_NOMINATIM_INTERVAL_MS", "120")
	resetRateLimiterForTest()

	mock := newMockGeocodingServer(t, respondWith(models.Geocoordinates{
		Lat: "1", Lon: "2", DisplayName: "Bayern",
	}))

	const requests = 4
	start := time.Now()
	for i := 0; i < requests; i++ {
		// Distinct locations so every call misses the cache.
		GetGeocoordinatesFromCache("Bayern", models.Tournament{
			Id: fmt.Sprint(i), Location: fmt.Sprintf("Stadt-%d", i),
		})
	}
	elapsed := time.Since(start)

	// Each tournament may need several candidate lookups, so assert on the
	// enforced spacing rather than an exact request count.
	calls := mock.callCount()
	if calls < requests {
		t.Fatalf("made %d requests, want at least %d", calls, requests)
	}

	minExpected := time.Duration(calls-1) * 120 * time.Millisecond
	if elapsed < minExpected {
		t.Errorf("elapsed %v for %d requests, want at least %v (rate limit not enforced)",
			elapsed, calls, minExpected)
	}
}

func TestGeocodingCacheHitsSkipUpstreamAndRateLimit(t *testing.T) {
	initTestCache(t)

	mock := newMockGeocodingServer(t, respondWith(models.Geocoordinates{
		Lat: "49.0069", Lon: "8.4037", DisplayName: "Karlsruhe, Baden-Württemberg",
	}))

	tournamentA := models.Tournament{Id: "1", Location: "Karlsruhe", Organizer: "TC Karlsruhe"}
	// A different tournament at the same location must reuse the cached entry.
	tournamentB := models.Tournament{Id: "2", Location: "Karlsruhe", Organizer: "TC Karlsruhe"}

	GetGeocoordinatesFromCache("Baden-Württemberg", tournamentA)
	afterFirst := mock.callCount()
	if afterFirst == 0 {
		t.Fatal("no upstream request was made for the first lookup")
	}

	for i := 0; i < 5; i++ {
		GetGeocoordinatesFromCache("Baden-Württemberg", tournamentA)
		GetGeocoordinatesFromCache("Baden-Württemberg", tournamentB)
	}

	if got := mock.callCount(); got != afterFirst {
		t.Errorf("made %d upstream requests, want %d (rest served from cache)", got, afterFirst)
	}
}

// TestFailedLookupsUseTheSameCacheKeys is the regression test for the bug where
// failures were stored under the tournament ID while lookups used
// location/organizer keys, so the backoff never took effect.
func TestFailedLookupsUseTheSameCacheKeys(t *testing.T) {
	initTestCache(t)

	// Always responds with a location in the wrong state -> no usable match.
	mock := newMockGeocodingServer(t, respondWith(models.Geocoordinates{
		Lat: "52.5", Lon: "13.4", DisplayName: "Berlin, Berlin, Deutschland",
	}))

	tournament := models.Tournament{Id: "1", Location: "Unbekanntstadt", Organizer: "TC Unbekannt"}

	// First attempt performs the lookup and records the failure. Several
	// place-name candidates are derived from the location and organizer, so
	// more than one upstream request is expected here; what matters is that
	// the attempt is recorded under the shared cache keys.
	if got := GetGeocoordinatesFromCache("Baden-Württemberg", tournament); got.Lat != "" {
		t.Errorf("expected no coordinates, got %+v", got)
	}
	firstRound := mock.callCount()
	if firstRound == 0 {
		t.Fatal("no geocoding request was made on the first attempt")
	}

	// The failure must be visible under the location key.
	locKey := generateLocationCacheKey(tournament.Location, "Baden-Württemberg")
	cached, found := getFromCache(locKey)
	if !found {
		t.Fatalf("no cache entry stored under location key %q", locKey)
	}
	if !cached.IsFailed || cached.FailCount != 1 {
		t.Errorf("cached entry = %+v, want IsFailed with FailCount 1", cached)
	}

	// Subsequent attempts must be suppressed entirely by the backoff.
	for i := 0; i < 3; i++ {
		GetGeocoordinatesFromCache("Baden-Württemberg", tournament)
	}
	if got := mock.callCount(); got != firstRound {
		t.Errorf("made %d requests total, want %d (backoff not honoured)", got, firstRound)
	}

	// A different tournament at the same location must also be suppressed.
	other := models.Tournament{Id: "999", Location: "Unbekanntstadt", Organizer: "TC Unbekannt"}
	GetGeocoordinatesFromCache("Baden-Württemberg", other)
	if got := mock.callCount(); got != firstRound {
		t.Errorf("made %d requests, want %d (per-location backoff not shared)", got, firstRound)
	}
}

func TestFailureCountIncrementsAcrossRetries(t *testing.T) {
	initTestCache(t)

	mock := newMockGeocodingServer(t, respondWith(models.Geocoordinates{
		Lat: "52.5", Lon: "13.4", DisplayName: "Berlin, Berlin, Deutschland",
	}))

	tournament := models.Tournament{Id: "1", Location: "Nirgendwo", Organizer: "TC Nirgendwo"}
	locKey := generateLocationCacheKey(tournament.Location, "Baden-Württemberg")

	var perRound int64
	for attempt := 1; attempt <= 3; attempt++ {
		before := mock.callCount()
		GetGeocoordinatesFromCache("Baden-Württemberg", tournament)
		if attempt == 1 {
			perRound = mock.callCount() - before
		}

		cached, found := getFromCache(locKey)
		if !found {
			t.Fatalf("attempt %d: no cache entry", attempt)
		}
		if cached.FailCount != attempt {
			t.Errorf("attempt %d: FailCount = %d, want %d", attempt, cached.FailCount, attempt)
		}

		// Expire the backoff so the next attempt is allowed through.
		cached.LastAttempt = time.Now().Add(-30 * 24 * time.Hour).Unix()
		setInCache(locKey, cached)
		if orgKey := generateOrganizerCacheKey(tournament.Organizer, "Baden-Württemberg"); orgKey != "" {
			if orgCached, ok := getFromCache(orgKey); ok {
				orgCached.LastAttempt = cached.LastAttempt
				setInCache(orgKey, orgCached)
			}
		}
	}

	if perRound == 0 {
		t.Fatal("first attempt made no upstream request")
	}
	if got, want := mock.callCount(), 3*perRound; got != want {
		t.Errorf("made %d requests, want %d (%d rounds x %d)", got, want, 3, perRound)
	}
}

func TestSuccessAfterFailureClearsFailureState(t *testing.T) {
	initTestCache(t)

	var succeed atomic.Bool
	mock := newMockGeocodingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if succeed.Load() {
			_ = json.NewEncoder(w).Encode([]models.Geocoordinates{
				{Lat: "49.0069", Lon: "8.4037", DisplayName: "Karlsruhe, Baden-Württemberg"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode([]models.Geocoordinates{})
	})

	tournament := models.Tournament{Id: "1", Location: "Karlsruhe", Organizer: "TC Karlsruhe"}
	locKey := generateLocationCacheKey(tournament.Location, "Baden-Württemberg")

	// First attempt fails.
	GetGeocoordinatesFromCache("Baden-Württemberg", tournament)
	cached, _ := getFromCache(locKey)
	if !cached.IsFailed {
		t.Fatal("first attempt was not recorded as failed")
	}

	// Expire the backoff, then let the upstream succeed.
	cached.LastAttempt = time.Now().Add(-30 * 24 * time.Hour).Unix()
	setInCache(locKey, cached)
	succeed.Store(true)

	got := GetGeocoordinatesFromCache("Baden-Württemberg", tournament)
	if got.Lat != "49.0069" {
		t.Fatalf("retry returned %+v, want successful coordinates", got)
	}

	stored, found := getFromCache(locKey)
	if !found {
		t.Fatal("no cache entry after success")
	}
	if stored.IsFailed || stored.FailCount != 0 {
		t.Errorf("stored entry = %+v, want failure state cleared", stored)
	}

	if got := mock.callCount(); got < 2 {
		t.Errorf("made %d requests, want at least 2 (one failed round, one success)", got)
	}
}

func TestGeocodingHandlesUpstreamErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "http 500",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			},
		},
		{
			name: "rate limited with retry-after",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "30")
				http.Error(w, "slow down", http.StatusTooManyRequests)
			},
		},
		{
			name: "invalid json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, "{not json")
			},
		},
		{
			name: "empty array",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, "[]")
			},
		},
		{
			name: "html error page",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, "<html><body>error</body></html>")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initTestCache(t)
			newMockGeocodingServer(t, tt.handler)

			// Must not panic and must report "no coordinates".
			got := GetGeocoordinatesFromCache("Baden-Württemberg", models.Tournament{
				Id: "1", Location: "Karlsruhe", Organizer: "TC Karlsruhe",
			})

			if got.Lat != "" || got.Lon != "" {
				t.Errorf("got %+v, want empty coordinates", got)
			}

			// The failure must be recorded so the backoff can take effect.
			locKey := generateLocationCacheKey("Karlsruhe", "Baden-Württemberg")
			if cached, found := getFromCache(locKey); !found || !cached.IsFailed {
				t.Errorf("failure was not cached (found=%v, entry=%+v)", found, cached)
			}
		})
	}
}

func TestGeocodingRejectsWrongState(t *testing.T) {
	initTestCache(t)

	// Returns three candidates; only the last is in the requested state.
	newMockGeocodingServer(t, respondWith(
		models.Geocoordinates{Lat: "52.5", Lon: "13.4", DisplayName: "Berlin, Berlin, Deutschland"},
		models.Geocoordinates{Lat: "48.1", Lon: "11.5", DisplayName: "München, Bayern, Deutschland"},
		models.Geocoordinates{Lat: "49.0", Lon: "8.4", DisplayName: "Karlsruhe, Baden-Württemberg, Deutschland"},
	))

	got := GetGeocoordinatesFromCache("Baden-Württemberg", models.Tournament{
		Id: "1", Location: "Karlsruhe",
	})

	if got.Lat != "49.0" {
		t.Errorf("got %+v, want the Baden-Württemberg candidate", got)
	}
}

func TestGeocodingSkipsRequestWithoutQuery(t *testing.T) {
	initTestCache(t)

	mock := newMockGeocodingServer(t, respondWith())

	// Neither a location nor an organizer: nothing sensible to ask for.
	got := GetGeocoordinatesFromCache("Hessen", models.Tournament{Id: "1"})

	if got.Lat != "" {
		t.Errorf("got %+v, want empty", got)
	}
	if calls := mock.callCount(); calls != 0 {
		t.Errorf("made %d upstream requests, want 0", calls)
	}
}

func TestGeocodingFallsBackToOrganizerQuery(t *testing.T) {
	initTestCache(t)

	mock := newMockGeocodingServer(t, respondWith(models.Geocoordinates{
		Lat: "49.0", Lon: "8.4", DisplayName: "Karlsruhe, Baden-Württemberg",
	}))

	// No location, so the city is derived from the organizer name.
	GetGeocoordinatesFromCache("Baden-Württemberg", models.Tournament{
		Id: "1", Organizer: "TC Rot-Weiß Karlsruhe e.V.",
	})

	queries := mock.recordedQueries()
	if len(queries) == 0 {
		t.Fatal("no query was sent")
	}
	var found bool
	for _, q := range queries {
		if strings.Contains(q, "Karlsruhe") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("queries = %v, want one to contain the extracted city", queries)
	}
}

func TestConcurrentGeocodingIsRaceFree(t *testing.T) {
	initTestCache(t)

	newMockGeocodingServer(t, respondWith(models.Geocoordinates{
		Lat: "49.0", Lon: "8.4", DisplayName: "Karlsruhe, Baden-Württemberg",
	}))

	// Hammer the cache from many goroutines, mixing reads, writes and stats.
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				GetGeocoordinatesFromCache("Baden-Württemberg", models.Tournament{
					Id:        fmt.Sprintf("%d-%d", i, j),
					Location:  fmt.Sprintf("Ort-%d", (i+j)%5),
					Organizer: fmt.Sprintf("TC %d", i%3),
				})
				GetCacheStatistics()
			}
		}(i)
	}
	wg.Wait()

	stats := GetCacheStatistics()
	if stats["total_entries"] == 0 {
		t.Error("expected cache entries after concurrent geocoding")
	}
}

func TestBuildNominatimURL(t *testing.T) {
	t.Setenv("TTF_NOMINATIM_URL", "https://nominatim.example.test/search.php")

	raw, err := buildNominatimURL("Bad Homburg vor der Höhe", true)
	if err != nil {
		t.Fatalf("buildNominatimURL() error = %v", err)
	}

	if !strings.HasPrefix(raw, "https://nominatim.example.test/search.php?") {
		t.Errorf("URL = %q, want the configured base", raw)
	}
	// Spaces and umlauts must be percent-encoded, not naively replaced.
	for _, want := range []string{"format=jsonv2", "limit=5", "addressdetails=1", "accept-language=de", "q=Bad+Homburg+vor+der+H%C3%B6he"} {
		if !strings.Contains(raw, want) {
			t.Errorf("URL %q does not contain %q", raw, want)
		}
	}

	if !strings.Contains(raw, "featureType=settlement") {
		t.Errorf("URL %q does not restrict results to settlements", raw)
	}

	permissive, err := buildNominatimURL("Kleinort", false)
	if err != nil {
		t.Fatalf("buildNominatimURL(permissive) error = %v", err)
	}
	if strings.Contains(permissive, "featureType") {
		t.Errorf("permissive URL %q should not restrict the feature type", permissive)
	}

	t.Setenv("TTF_NOMINATIM_URL", "://broken")
	if _, err := buildNominatimURL("x", true); err == nil {
		t.Error("expected an error for an invalid base URL")
	}
}

func TestShouldRetryGeocodingRequestBackoff(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name      string
		failCount int
		age       time.Duration
		want      bool
	}{
		{"first failure too recent", 1, time.Hour, false},
		{"first failure after a day", 1, 25 * time.Hour, true},
		{"second failure too recent", 2, 2 * 24 * time.Hour, false},
		{"second failure after three days", 2, 4 * 24 * time.Hour, true},
		{"third failure too recent", 3, 5 * 24 * time.Hour, false},
		{"third failure after a week", 3, 8 * 24 * time.Hour, true},
		{"fourth failure too recent", 4, 10 * 24 * time.Hour, false},
		{"fourth failure after two weeks", 4, 15 * 24 * time.Hour, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			geo := models.Geocoordinates{
				IsFailed:    true,
				FailCount:   tt.failCount,
				LastAttempt: now - int64(tt.age.Seconds()),
			}
			if got := shouldRetryGeocodingRequest(geo); got != tt.want {
				t.Errorf("shouldRetryGeocodingRequest(fail=%d, age=%v) = %v, want %v",
					tt.failCount, tt.age, got, tt.want)
			}
		})
	}
}
