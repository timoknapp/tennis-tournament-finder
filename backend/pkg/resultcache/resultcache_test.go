package resultcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
)

// fakeClock gives tests deterministic control over cache expiry.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func testKey() Key {
	return Key{FederationID: "BAD", DateFrom: "01.08.2026", DateTo: "15.08.2026"}
}

func tournaments(ids ...string) []models.Tournament {
	out := make([]models.Tournament, 0, len(ids))
	for _, id := range ids {
		out = append(out, models.Tournament{Id: id, Title: "Turnier " + id})
	}
	return out
}

// countingLoader records how often the expensive path was taken.
func countingLoader(calls *int64, result []models.Tournament, err error) Loader {
	return func(ctx context.Context, key Key) ([]models.Tournament, error) {
		atomic.AddInt64(calls, 1)
		return result, err
	}
}

func newTestCache(clock *fakeClock, ttl, stale time.Duration) *Cache {
	return New(NewMemoryStore(), Options{TTL: ttl, StaleTTL: stale, Now: clock.Now})
}

func TestFreshEntryIsServedFromCache(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(clock, time.Hour, 24*time.Hour)

	var calls int64
	load := countingLoader(&calls, tournaments("1", "2"), nil)

	// First call populates the cache.
	first := c.Get(context.Background(), testKey(), load)
	if first.Err != nil {
		t.Fatalf("first Get() error = %v", first.Err)
	}
	if first.Cached {
		t.Error("first call reported Cached, want a fresh load")
	}
	if len(first.Tournaments) != 2 {
		t.Fatalf("got %d tournaments, want 2", len(first.Tournaments))
	}

	// Subsequent calls within the TTL must not hit the loader.
	for i := 0; i < 5; i++ {
		res := c.Get(context.Background(), testKey(), load)
		if !res.Cached {
			t.Errorf("call %d was not served from cache", i)
		}
		if res.Stale {
			t.Errorf("call %d reported stale data inside the TTL", i)
		}
		if len(res.Tournaments) != 2 {
			t.Errorf("call %d returned %d tournaments, want 2", i, len(res.Tournaments))
		}
	}

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("loader ran %d times, want 1", got)
	}
}

func TestExpiredEntryTriggersRefresh(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(clock, time.Hour, 24*time.Hour)

	var calls int64
	load := countingLoader(&calls, tournaments("1"), nil)

	c.Get(context.Background(), testKey(), load)

	// Still fresh.
	clock.Advance(59 * time.Minute)
	if res := c.Get(context.Background(), testKey(), load); !res.Cached {
		t.Error("entry expired too early")
	}

	// Past the TTL.
	clock.Advance(2 * time.Minute)
	res := c.Get(context.Background(), testKey(), load)
	if res.Cached {
		t.Error("expired entry was served from cache")
	}

	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Errorf("loader ran %d times, want 2", got)
	}
}

func TestAgeIsReported(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(clock, time.Hour, 24*time.Hour)

	var calls int64
	load := countingLoader(&calls, tournaments("1"), nil)

	c.Get(context.Background(), testKey(), load)
	clock.Advance(20 * time.Minute)

	res := c.Get(context.Background(), testKey(), load)
	if res.Age != 20*time.Minute {
		t.Errorf("Age = %v, want 20m", res.Age)
	}
}

// TestStaleDataServedWhenUpstreamFails is the property that keeps the map
// usable during a federation outage.
func TestStaleDataServedWhenUpstreamFails(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(clock, time.Hour, 24*time.Hour)

	// Populate with a successful load.
	var okCalls int64
	c.Get(context.Background(), testKey(), countingLoader(&okCalls, tournaments("1", "2"), nil))

	// Expire it, then fail every refresh.
	clock.Advance(2 * time.Hour)
	boom := errors.New("federation unreachable")
	var failCalls int64
	res := c.Get(context.Background(), testKey(), countingLoader(&failCalls, nil, boom))

	if len(res.Tournaments) != 2 {
		t.Fatalf("got %d tournaments, want the stale copy of 2", len(res.Tournaments))
	}
	if !res.Stale {
		t.Error("result was not marked stale")
	}
	if !res.Cached {
		t.Error("result was not marked cached")
	}
	if res.Age != 2*time.Hour {
		t.Errorf("Age = %v, want 2h", res.Age)
	}
	// The refresh error must remain visible so callers can surface it.
	if !errors.Is(res.Err, boom) {
		t.Errorf("Err = %v, want the refresh failure", res.Err)
	}
}

func TestStaleDataExpiresEventually(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(clock, time.Hour, 6*time.Hour)

	var calls int64
	c.Get(context.Background(), testKey(), countingLoader(&calls, tournaments("1"), nil))

	// Beyond the stale window the data is too old to be useful.
	clock.Advance(7 * time.Hour)

	boom := errors.New("federation unreachable")
	res := c.Get(context.Background(), testKey(), countingLoader(&calls, nil, boom))

	if len(res.Tournaments) != 0 {
		t.Errorf("got %d tournaments, want none beyond the stale window", len(res.Tournaments))
	}
	if !errors.Is(res.Err, boom) {
		t.Errorf("Err = %v, want the refresh failure", res.Err)
	}
}

// TestConcurrentMissesLoadOnce covers the cache stampede guard: 50 users
// hitting a cold cache must produce one upstream fetch, not 50.
func TestConcurrentMissesLoadOnce(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(clock, time.Hour, 24*time.Hour)

	var calls int64
	release := make(chan struct{})
	load := func(ctx context.Context, key Key) ([]models.Tournament, error) {
		atomic.AddInt64(&calls, 1)
		<-release // hold the load open so every caller piles up
		return tournaments("1", "2", "3"), nil
	}

	const callers = 50
	var wg sync.WaitGroup
	results := make([]Result, callers)

	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = c.Get(context.Background(), testKey(), load)
		}(i)
	}

	// Give the goroutines time to arrive at the cache.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("loader ran %d times for %d concurrent callers, want 1", got, callers)
	}

	for i, res := range results {
		if res.Err != nil {
			t.Errorf("caller %d error = %v", i, res.Err)
		}
		if len(res.Tournaments) != 3 {
			t.Errorf("caller %d got %d tournaments, want 3", i, len(res.Tournaments))
		}
	}
}

func TestDifferentKeysDoNotShareEntries(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(clock, time.Hour, 24*time.Hour)

	keyA := Key{FederationID: "BAD", DateFrom: "01.08.2026", DateTo: "15.08.2026"}
	keyB := Key{FederationID: "HTV", DateFrom: "01.08.2026", DateTo: "15.08.2026"}
	keyC := Key{FederationID: "BAD", DateFrom: "01.09.2026", DateTo: "15.09.2026"}
	keyD := Key{FederationID: "BAD", DateFrom: "01.08.2026", DateTo: "15.08.2026", CompType: "Damen+Einzel"}

	var calls int64
	load := func(ctx context.Context, key Key) ([]models.Tournament, error) {
		atomic.AddInt64(&calls, 1)
		return tournaments(key.String()), nil
	}

	for _, k := range []Key{keyA, keyB, keyC, keyD} {
		c.Get(context.Background(), k, load)
	}

	if got := atomic.LoadInt64(&calls); got != 4 {
		t.Errorf("loader ran %d times, want 4 (one per distinct key)", got)
	}

	// Each key must return its own data.
	res := c.Get(context.Background(), keyA, load)
	if len(res.Tournaments) != 1 || res.Tournaments[0].Id != keyA.String() {
		t.Errorf("keyA returned %+v, want its own entry", res.Tournaments)
	}
}

func TestEmptyResultIsCached(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(clock, time.Hour, 24*time.Hour)

	var calls int64
	// A federation legitimately having no tournaments must not be retried on
	// every request.
	load := countingLoader(&calls, []models.Tournament{}, nil)

	for i := 0; i < 3; i++ {
		res := c.Get(context.Background(), testKey(), load)
		if res.Err != nil {
			t.Fatalf("call %d error = %v", i, res.Err)
		}
		if len(res.Tournaments) != 0 {
			t.Errorf("call %d returned %d tournaments, want 0", i, len(res.Tournaments))
		}
	}

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("loader ran %d times, want 1 (empty results are cacheable)", got)
	}
}

func TestFailedFirstLoadIsNotCached(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(clock, time.Hour, 24*time.Hour)

	boom := errors.New("upstream down")
	var failCalls int64
	res := c.Get(context.Background(), testKey(), countingLoader(&failCalls, nil, boom))
	if !errors.Is(res.Err, boom) {
		t.Fatalf("Err = %v, want the load failure", res.Err)
	}

	// The next call must retry rather than serve a cached failure.
	var okCalls int64
	res = c.Get(context.Background(), testKey(), countingLoader(&okCalls, tournaments("1"), nil))
	if res.Err != nil {
		t.Fatalf("retry error = %v", res.Err)
	}
	if len(res.Tournaments) != 1 {
		t.Errorf("got %d tournaments, want 1", len(res.Tournaments))
	}
	if atomic.LoadInt64(&okCalls) != 1 {
		t.Error("retry did not reach the loader")
	}
}

func TestInvalidate(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(clock, time.Hour, 24*time.Hour)

	var calls int64
	load := countingLoader(&calls, tournaments("1"), nil)

	c.Get(context.Background(), testKey(), load)
	if err := c.Invalidate(testKey()); err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}
	c.Get(context.Background(), testKey(), load)

	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Errorf("loader ran %d times, want 2 after invalidation", got)
	}
}

func TestNilCacheLoadsDirectly(t *testing.T) {
	var c *Cache

	var calls int64
	res := c.Get(context.Background(), testKey(), countingLoader(&calls, tournaments("1"), nil))

	if len(res.Tournaments) != 1 {
		t.Errorf("got %d tournaments, want 1", len(res.Tournaments))
	}
	if atomic.LoadInt64(&calls) != 1 {
		t.Error("nil cache did not call the loader")
	}
}

func TestStats(t *testing.T) {
	clock := newFakeClock()
	c := newTestCache(clock, time.Hour, 6*time.Hour)

	load := func(ctx context.Context, key Key) ([]models.Tournament, error) {
		return tournaments("1", "2"), nil
	}

	c.Get(context.Background(), Key{FederationID: "BAD"}, load)
	c.Get(context.Background(), Key{FederationID: "HTV"}, load)

	clock.Advance(2 * time.Hour) // both entries now stale but not expired

	c.Get(context.Background(), Key{FederationID: "WTB"}, func(ctx context.Context, key Key) ([]models.Tournament, error) {
		return tournaments("9"), nil
	})

	stats, err := c.Stats()
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}

	if stats.Entries != 3 {
		t.Errorf("Entries = %d, want 3", stats.Entries)
	}
	if stats.Fresh != 1 {
		t.Errorf("Fresh = %d, want 1", stats.Fresh)
	}
	if stats.Stale != 2 {
		t.Errorf("Stale = %d, want 2", stats.Stale)
	}
	if stats.Tournaments != 5 {
		t.Errorf("Tournaments = %d, want 5", stats.Tournaments)
	}
	if stats.PerFederation["BAD"] != 2 {
		t.Errorf("PerFederation[BAD] = %d, want 2", stats.PerFederation["BAD"])
	}
}

func TestKeyString(t *testing.T) {
	k := Key{FederationID: "BAD", DateFrom: "01.08.2026", DateTo: "15.08.2026", CompType: "Damen+Einzel"}
	want := "BAD|01.08.2026|15.08.2026|Damen+Einzel"
	if got := k.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestOptionDefaults(t *testing.T) {
	c := New(NewMemoryStore(), Options{})
	if c.ttl != DefaultTTL {
		t.Errorf("ttl = %v, want the default %v", c.ttl, DefaultTTL)
	}
	if c.stale != DefaultStaleTTL {
		t.Errorf("stale = %v, want the default %v", c.stale, DefaultStaleTTL)
	}
}

func TestEnabledDefaultsToTrue(t *testing.T) {
	t.Setenv("TTF_RESULT_CACHE", "")
	if !Enabled() {
		t.Error("Enabled() = false, want true by default")
	}

	for _, v := range []string{"false", "FALSE", "0", "off"} {
		t.Setenv("TTF_RESULT_CACHE", v)
		if Enabled() {
			t.Errorf("Enabled() = true for %q, want false", v)
		}
	}
}

func TestOptionsFromEnv(t *testing.T) {
	t.Setenv("TTF_CACHE_TTL_MINUTES", "30")
	t.Setenv("TTF_CACHE_STALE_MINUTES", "120")

	opts := OptionsFromEnv()
	if opts.TTL != 30*time.Minute {
		t.Errorf("TTL = %v, want 30m", opts.TTL)
	}
	if opts.StaleTTL != 120*time.Minute {
		t.Errorf("StaleTTL = %v, want 120m", opts.StaleTTL)
	}

	// Invalid values fall back to the defaults rather than disabling caching.
	t.Setenv("TTF_CACHE_TTL_MINUTES", "not-a-number")
	if got := OptionsFromEnv().TTL; got != DefaultTTL {
		t.Errorf("TTL = %v, want the default for invalid input", got)
	}
}

// TestBoltStoreRoundTrip verifies persistence, which is what lets a restart
// keep the work the scheduler already did.
func TestBoltStoreRoundTrip(t *testing.T) {
	path := t.TempDir() + "/results.bolt"

	store, err := NewBoltStore(path)
	if err != nil {
		t.Fatalf("NewBoltStore() error = %v", err)
	}

	entry := Entry{
		FederationID: "BAD",
		Query:        "BAD|01.08.2026|15.08.2026|",
		Tournaments:  tournaments("1", "2"),
		StoredAt:     time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
	}
	if err := store.Set(entry.Query, entry); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Reopen: the data must survive.
	reopened, err := NewBoltStore(path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer reopened.Close()

	got, found, err := reopened.Get(entry.Query)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !found {
		t.Fatal("entry did not survive a restart")
	}
	if len(got.Tournaments) != 2 || got.Tournaments[0].Id != "1" {
		t.Errorf("got %+v, want the stored tournaments", got.Tournaments)
	}
	if !got.StoredAt.Equal(entry.StoredAt) {
		t.Errorf("StoredAt = %v, want %v", got.StoredAt, entry.StoredAt)
	}
}

func TestBoltStoreDeleteAndForEach(t *testing.T) {
	store, err := NewBoltStore(t.TempDir() + "/results.bolt")
	if err != nil {
		t.Fatalf("NewBoltStore() error = %v", err)
	}
	defer store.Close()

	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("FED%d|a|b|", i)
		if err := store.Set(key, Entry{FederationID: key, Tournaments: tournaments("1")}); err != nil {
			t.Fatalf("Set() error = %v", err)
		}
	}

	count := 0
	if err := store.ForEach(func(string, Entry) error { count++; return nil }); err != nil {
		t.Fatalf("ForEach() error = %v", err)
	}
	if count != 3 {
		t.Errorf("ForEach visited %d entries, want 3", count)
	}

	if err := store.Delete("FED1|a|b|"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, found, _ := store.Get("FED1|a|b|"); found {
		t.Error("entry still present after Delete")
	}
}

func TestBoltStoreMissingKey(t *testing.T) {
	store, err := NewBoltStore(t.TempDir() + "/results.bolt")
	if err != nil {
		t.Fatalf("NewBoltStore() error = %v", err)
	}
	defer store.Close()

	if _, found, err := store.Get("does-not-exist"); err != nil || found {
		t.Errorf("Get(missing) = found %v, err %v; want not found, nil", found, err)
	}
}
