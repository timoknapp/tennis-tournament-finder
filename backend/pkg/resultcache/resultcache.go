// Package resultcache stores tournament results per federation so user
// requests do not have to scrape federation websites live.
//
// Without it, every request fans out to each selected federation, which is
// slow, amplifies upstream failures and is the reason the frontend limits
// users to a handful of federations at a time.
//
// The cache is:
//
//   - keyed per federation and query, so one slow federation cannot hold up
//     the others and a refresh only redoes the work that expired
//   - persistent, so a restart does not throw away work the scheduler did
//   - stale-tolerant: expired data is still served when the upstream is
//     unavailable, with its age reported so callers can surface it
//   - stampede-safe: concurrent misses for the same key trigger one refresh
package resultcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
)

// Default cache lifetimes. Tournament calendars change slowly: entries are
// added days or weeks in advance, so an hour-old list is materially the same
// list.
const (
	DefaultTTL = 2 * time.Hour
	// DefaultStaleTTL is how long expired data may still be served when the
	// upstream cannot be reached. Stale results beat an empty map.
	DefaultStaleTTL = 24 * time.Hour
)

// Entry is one cached federation result.
type Entry struct {
	FederationID string              `json:"federation_id"`
	Query        string              `json:"query"`
	Tournaments  []models.Tournament `json:"tournaments"`
	StoredAt     time.Time           `json:"stored_at"`
	// Err records why the last refresh failed, if it did. An entry can hold
	// both tournaments and an error: the data is from an earlier successful
	// run and the error explains why it was not refreshed.
	Err string `json:"err,omitempty"`
}

// Age reports how long ago the entry was stored.
func (e Entry) Age(now time.Time) time.Duration {
	return now.Sub(e.StoredAt)
}

// Key identifies a cached query for one federation.
type Key struct {
	FederationID string
	DateFrom     string
	DateTo       string
	CompType     string
}

// String renders the key for storage and logging.
func (k Key) String() string {
	return strings.Join([]string{k.FederationID, k.DateFrom, k.DateTo, k.CompType}, "|")
}

// Store persists cache entries.
type Store interface {
	Get(key string) (Entry, bool, error)
	Set(key string, entry Entry) error
	Delete(key string) error
	ForEach(fn func(key string, entry Entry) error) error
	Close() error
}

// Loader fetches fresh results for a key. It is the expensive operation the
// cache exists to avoid.
type Loader func(ctx context.Context, key Key) ([]models.Tournament, error)

// Result is what the cache returns to a caller.
type Result struct {
	Tournaments []models.Tournament
	// Age is zero for a freshly loaded result.
	Age time.Duration
	// Stale reports that the TTL had expired and the upstream could not be
	// refreshed, so this is older data.
	Stale bool
	// Cached reports that no upstream request was made.
	Cached bool
	// Err is the refresh error when stale data is returned, or the load error
	// when nothing could be served.
	Err error
}

// Cache serves tournament results, refreshing them on demand.
type Cache struct {
	store Store
	ttl   time.Duration
	stale time.Duration

	// now is injectable so tests do not depend on wall-clock time.
	now func() time.Time

	// inflight collapses concurrent refreshes of the same key.
	mu       sync.Mutex
	inflight map[string]*call
}

type call struct {
	done chan struct{}
	res  Result
}

// Options configures a Cache.
type Options struct {
	TTL      time.Duration
	StaleTTL time.Duration
	Now      func() time.Time
}

// New creates a cache backed by store.
func New(store Store, opts Options) *Cache {
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	stale := opts.StaleTTL
	if stale <= 0 {
		stale = DefaultStaleTTL
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	return &Cache{
		store:    store,
		ttl:      ttl,
		stale:    stale,
		now:      now,
		inflight: make(map[string]*call),
	}
}

// OptionsFromEnv reads cache tuning from the environment.
func OptionsFromEnv() Options {
	return Options{
		TTL:      durationFromEnv("TTF_CACHE_TTL_MINUTES", DefaultTTL),
		StaleTTL: durationFromEnv("TTF_CACHE_STALE_MINUTES", DefaultStaleTTL),
	}
}

// Enabled reports whether result caching is switched on. It defaults to true;
// set TTF_RESULT_CACHE=false to bypass the cache entirely.
func Enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("TTF_RESULT_CACHE")))
	return v != "false" && v != "0" && v != "off"
}

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	minutes, err := strconv.Atoi(raw)
	if err != nil || minutes < 0 {
		return fallback
	}
	return time.Duration(minutes) * time.Minute
}

// TTL returns the configured freshness window.
func (c *Cache) TTL() time.Duration { return c.ttl }

// Get returns results for key, loading them when the cached copy is missing or
// expired.
//
// Behaviour on a failed refresh is deliberate: usable stale data is preferred
// over an error, because an out-of-date tournament list is far more useful to
// a player than an empty map.
func (c *Cache) Get(ctx context.Context, key Key, load Loader) Result {
	if c == nil {
		tournaments, err := load(ctx, key)
		return Result{Tournaments: tournaments, Err: err}
	}

	k := key.String()
	now := c.now()

	cached, found, err := c.store.Get(k)
	if err != nil {
		// A broken cache must never take down the request path.
		cached, found = Entry{}, false
	}

	if found && cached.Age(now) < c.ttl && cached.Tournaments != nil {
		return Result{
			Tournaments: cached.Tournaments,
			Age:         cached.Age(now),
			Cached:      true,
		}
	}

	// Miss or expired: refresh, collapsing concurrent callers.
	res := c.refresh(ctx, key, load)

	// Refresh failed: fall back to stale data when it is still within the
	// stale window.
	if res.Err != nil && found && cached.Tournaments != nil {
		age := cached.Age(now)
		if age < c.stale {
			return Result{
				Tournaments: cached.Tournaments,
				Age:         age,
				Stale:       true,
				Cached:      true,
				Err:         res.Err,
			}
		}
	}

	return res
}

// refresh loads fresh data, ensuring only one load per key runs at a time.
func (c *Cache) refresh(ctx context.Context, key Key, load Loader) Result {
	k := key.String()

	c.mu.Lock()
	if existing, ok := c.inflight[k]; ok {
		c.mu.Unlock()
		// Wait for the in-flight refresh instead of starting a second one.
		select {
		case <-existing.done:
			return existing.res
		case <-ctx.Done():
			return Result{Err: ctx.Err()}
		}
	}

	cl := &call{done: make(chan struct{})}
	c.inflight[k] = cl
	c.mu.Unlock()

	tournaments, err := load(ctx, key)

	if err == nil {
		entry := Entry{
			FederationID: key.FederationID,
			Query:        k,
			Tournaments:  tournaments,
			StoredAt:     c.now(),
		}
		if storeErr := c.store.Set(k, entry); storeErr != nil {
			// Failing to persist is not fatal; the data is still returned.
			err = nil
		}
	}

	cl.res = Result{Tournaments: tournaments, Err: err}

	c.mu.Lock()
	delete(c.inflight, k)
	c.mu.Unlock()
	close(cl.done)

	return cl.res
}

// Invalidate removes a cached entry.
func (c *Cache) Invalidate(key Key) error {
	if c == nil {
		return nil
	}
	return c.store.Delete(key.String())
}

// Stats summarises cache contents for diagnostics.
type Stats struct {
	Entries       int            `json:"entries"`
	Fresh         int            `json:"fresh"`
	Stale         int            `json:"stale"`
	Expired       int            `json:"expired"`
	Tournaments   int            `json:"tournaments"`
	OldestSecond  int64          `json:"oldest_entry_seconds"`
	PerFederation map[string]int `json:"per_federation"`
}

// Stats inspects the cache.
func (c *Cache) Stats() (Stats, error) {
	stats := Stats{PerFederation: make(map[string]int)}
	if c == nil {
		return stats, nil
	}

	now := c.now()
	err := c.store.ForEach(func(_ string, entry Entry) error {
		stats.Entries++
		stats.Tournaments += len(entry.Tournaments)
		stats.PerFederation[entry.FederationID] += len(entry.Tournaments)

		age := entry.Age(now)
		switch {
		case age < c.ttl:
			stats.Fresh++
		case age < c.stale:
			stats.Stale++
		default:
			stats.Expired++
		}

		if secs := int64(age.Seconds()); secs > stats.OldestSecond {
			stats.OldestSecond = secs
		}
		return nil
	})

	return stats, err
}

// Close releases the underlying store.
func (c *Cache) Close() error {
	if c == nil {
		return nil
	}
	return c.store.Close()
}

// ErrNotFound is returned by stores for a missing key.
var ErrNotFound = errors.New("cache entry not found")

// encode/decode are shared by store implementations.
func encode(entry Entry) ([]byte, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("failed to encode cache entry: %w", err)
	}
	return data, nil
}

func decode(data []byte) (Entry, error) {
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, fmt.Errorf("failed to decode cache entry: %w", err)
	}
	return entry, nil
}
