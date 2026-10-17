package openstreetmap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/timoknapp/tennis-tournament-finder/pkg/cache"
	"github.com/timoknapp/tennis-tournament-finder/pkg/clublocations"
	"github.com/timoknapp/tennis-tournament-finder/pkg/httpclient"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
	"github.com/timoknapp/tennis-tournament-finder/pkg/placename"
	"github.com/timoknapp/tennis-tournament-finder/pkg/ratelimit"
)

var CachedGeocoordinates map[string]models.Geocoordinates
var LocationCache map[string]models.Geocoordinates
var OrganizerCache map[string]models.Geocoordinates

var cacheStore cache.Store
var useMemoryCache bool

// cacheMu guards the in-memory cache maps above. Geocoding runs concurrently
// for every selected federation, so unsynchronized map access would be a data
// race (and can crash the process with "concurrent map writes").
var cacheMu sync.RWMutex

// defaultNominatimBaseURL is the public Nominatim search endpoint.
const defaultNominatimBaseURL = "https://nominatim.openstreetmap.org/search.php"

// maxGeocodingResponseBytes bounds how much of an upstream response is read.
const maxGeocodingResponseBytes = 1 << 20 // 1 MiB

// defaultNominatimInterval honours the Nominatim usage policy of at most one
// request per second for the shared public instance.
const defaultNominatimInterval = time.Second

// maxGeocodeCandidates bounds how many place-name guesses are derived per
// tournament.
const maxGeocodeCandidates = 4

// maxGeocodeRequests bounds the total upstream requests per tournament across
// both lookup passes. Each request is rate limited, so this keeps the cost of
// one tournament predictable.
const maxGeocodeRequests = 6

var (
	geocodingLimiterOnce sync.Once
	geocodingLimiter     *ratelimit.Limiter
)

// nominatimBaseURL returns the geocoding endpoint. It is configurable so the
// service can be pointed at a self-hosted Nominatim instance, and so tests can
// direct it at a local mock server.
func nominatimBaseURL() string {
	if raw := os.Getenv("TTF_NOMINATIM_URL"); raw != "" {
		return raw
	}
	return defaultNominatimBaseURL
}

// geocodingRateLimiter returns the process-wide limiter for uncached geocoding
// requests. The interval can be lowered for a self-hosted instance via
// TTF_NOMINATIM_INTERVAL_MS.
func geocodingRateLimiter() *ratelimit.Limiter {
	geocodingLimiterOnce.Do(func() {
		interval := defaultNominatimInterval
		if raw := os.Getenv("TTF_NOMINATIM_INTERVAL_MS"); raw != "" {
			if ms, err := strconv.Atoi(raw); err == nil && ms >= 0 {
				interval = time.Duration(ms) * time.Millisecond
			}
		}
		geocodingLimiter = ratelimit.New(interval)
	})
	return geocodingLimiter
}

func InitCache() {
	// Initialize environment-based configuration
	useMemoryCache = os.Getenv("TTF_CACHE_MEMORY") != "false" // default true
	cachePath := os.Getenv("TTF_CACHE_PATH")
	if cachePath == "" {
		cachePath = "./data/cache.bolt" // default path
	}

	// Initialize BoltDB store
	var err error
	cacheStore, err = cache.NewBoltStore(cachePath)
	if err != nil {
		logger.Error("Failed to initialize BoltDB cache store: %v", err)
		// Fallback to memory-only mode
		useMemoryCache = true
		cacheStore = nil
	}

	// Initialize in-memory maps
	cacheMu.Lock()
	CachedGeocoordinates = make(map[string]models.Geocoordinates)
	LocationCache = make(map[string]models.Geocoordinates)
	OrganizerCache = make(map[string]models.Geocoordinates)
	cacheMu.Unlock()

	if useMemoryCache && cacheStore != nil {
		// Preload BoltDB data into memory when memory cache is enabled
		logger.Info("Loading existing cache data from BoltDB into memory...")
		cacheMu.Lock()
		err := cacheStore.ForEach(func(key string, value models.Geocoordinates) error {
			// Determine which cache map to populate based on key prefix
			if strings.HasPrefix(key, "loc:") {
				LocationCache[key] = value
			} else if strings.HasPrefix(key, "org:") {
				OrganizerCache[key] = value
			} else {
				// Legacy tournament-specific cache (no prefix)
				CachedGeocoordinates[key] = value
			}
			return nil
		})
		locations, organizers, tournaments := len(LocationCache), len(OrganizerCache), len(CachedGeocoordinates)
		cacheMu.Unlock()

		if err != nil {
			logger.Error("Failed to preload cache data: %v", err)
		} else {
			logger.Info("Preloaded %d tournament, %d location, %d organizer entries from BoltDB",
				tournaments, locations, organizers)
		}
	}

	logger.Info("Cache initialized: memory=%v, persistent=%v", useMemoryCache, cacheStore != nil)
}

// CloseCache properly closes the cache resources
func CloseCache() {
	if cacheStore != nil {
		if err := cacheStore.Close(); err != nil {
			logger.Error("Failed to close cache store: %v", err)
		}
	}
}

// getFromCache retrieves a geocoordinates entry from the appropriate cache
func getFromCache(key string) (models.Geocoordinates, bool) {
	if useMemoryCache {
		cacheMu.RLock()
		defer cacheMu.RUnlock()

		// Determine which memory cache to check based on key prefix
		var memCache map[string]models.Geocoordinates
		if strings.HasPrefix(key, "loc:") {
			memCache = LocationCache
		} else if strings.HasPrefix(key, "org:") {
			memCache = OrganizerCache
		} else {
			memCache = CachedGeocoordinates
		}

		if cachedGeo, exists := memCache[key]; exists {
			return cachedGeo, true
		}
		return models.Geocoordinates{}, false
	}

	// Use BoltDB directly when memory cache is disabled
	if cacheStore != nil {
		geo, found, err := cacheStore.Get(key)
		if err != nil {
			logger.Error("Failed to get key %s from BoltDB: %v", key, err)
			return models.Geocoordinates{}, false
		}
		return geo, found
	}

	return models.Geocoordinates{}, false
}

// setInCache stores a geocoordinates entry in the appropriate cache
func setInCache(key string, value models.Geocoordinates) {
	// Always persist to BoltDB if available
	if cacheStore != nil {
		if err := cacheStore.Set(key, value); err != nil {
			logger.Error("Failed to persist key %s to BoltDB: %v", key, err)
		}
	}

	// Also store in memory if memory cache is enabled
	if useMemoryCache {
		cacheMu.Lock()
		defer cacheMu.Unlock()

		// Determine which memory cache to update based on key prefix
		if strings.HasPrefix(key, "loc:") {
			LocationCache[key] = value
		} else if strings.HasPrefix(key, "org:") {
			OrganizerCache[key] = value
		} else {
			CachedGeocoordinates[key] = value
		}
	}
}

// geocodeCacheKeys returns the cache keys describing a tournament's location,
// in lookup priority order.
//
// Successful and failed lookups must use the same keys, otherwise the retry
// backoff can never observe an earlier failure. Keys are derived from the
// location/organizer (which are stable and shared between tournaments) rather
// than the volatile tournament ID.
func geocodeCacheKeys(state string, tournament models.Tournament) []string {
	var keys []string

	if len(strings.TrimSpace(tournament.Location)) > 0 {
		keys = append(keys, generateLocationCacheKey(tournament.Location, state))
	}
	if len(strings.TrimSpace(tournament.Organizer)) > 0 {
		keys = append(keys, generateOrganizerCacheKey(tournament.Organizer, state))
	}

	return keys
}
func generateLocationCacheKey(location, state string) string {
	// Normalize the location string for better cache hits
	normalized := strings.ToLower(strings.TrimSpace(location))
	return fmt.Sprintf("loc:%s:%s", normalized, state)
}

// generateOrganizerCacheKey creates a standardized cache key for organizer-based caching
func generateOrganizerCacheKey(organizer, state string) string {
	// Normalize the organizer string for better cache hits
	normalized := strings.ToLower(strings.TrimSpace(organizer))
	return fmt.Sprintf("org:%s:%s", normalized, state)
}

func GetGeocoordinatesFromCache(state string, tournament models.Tournament) models.Geocoordinates {
	return GetGeocoordinatesForFederation(models.Federation{State: state}, tournament)
}

// GetGeocoordinatesForFederation resolves a tournament's coordinates, accepting
// results from any state the federation covers.
func GetGeocoordinatesForFederation(fed models.Federation, tournament models.Tournament) models.Geocoordinates {
	acceptedStates := fed.AcceptedStates()
	primaryState := ""
	if len(acceptedStates) > 0 {
		primaryState = acceptedStates[0]
	}

	keys := geocodeCacheKeys(primaryState, tournament)

	// Check every candidate key. A successful hit wins immediately.
	//
	// A retry is suppressed only when every known key is still inside its
	// backoff window. If any candidate is unknown or its backoff has expired,
	// the lookup is allowed to proceed.
	var knownEntries, blockedEntries int
	for _, key := range keys {
		cachedGeo, exists := getFromCache(key)
		if !exists {
			continue
		}

		if cachedGeo.Lat != "" && cachedGeo.Lon != "" {
			logger.Debug("Cache HIT: %s for tournament %s", key, tournament.Id)
			return cachedGeo
		}

		knownEntries++
		if cachedGeo.IsFailed && !shouldRetryGeocodingRequest(cachedGeo) {
			logger.Debug("Skipping geocoding retry for %s (tournament %s, failed %d times, last attempt %v)",
				key, tournament.Id, cachedGeo.FailCount, time.Unix(cachedGeo.LastAttempt, 0))
			blockedEntries++
		}
	}

	// Every known key is in backoff: do not hit the upstream service again.
	// The caller falls back to the federation's default coordinates.
	if knownEntries > 0 && blockedEntries == knownEntries && blockedEntries == len(keys) {
		return models.Geocoordinates{}
	}

	logger.Debug("No Geocoordinate Cache entry found for (%s): '%s' at '%s'. Fetching data from server.",
		tournament.Id, tournament.Organizer, tournament.Location)

	return getGeocoordinatesForStates(acceptedStates, tournament)
}

// shouldRetryGeocodingRequest determines if a failed geocoding request should be retried
func shouldRetryGeocodingRequest(cachedGeo models.Geocoordinates) bool {
	now := time.Now().Unix()

	// Progressive backoff strategy:
	// 1st failure: retry after 1 day
	// 2nd failure: retry after 3 days
	// 3rd failure: retry after 1 week
	// 4th+ failure: retry after 2 weeks

	var retryInterval int64
	switch cachedGeo.FailCount {
	case 1:
		retryInterval = 86400 // 1 day
	case 2:
		retryInterval = 259200 // 3 days
	case 3:
		retryInterval = 604800 // 1 week
	default:
		retryInterval = 1209600 // 2 weeks
	}

	return (now - cachedGeo.LastAttempt) >= retryInterval
}

func saveGeocoordinatesInCache(tournament models.Tournament, state string, geoCoordinates models.Geocoordinates) {
	for _, key := range geocodeCacheKeys(state, tournament) {
		setInCache(key, geoCoordinates)
		logger.Debug("Cached geocoordinates for key: %s", key)
	}
}

// GetCacheStatistics returns useful statistics about the geocoding cache
func GetCacheStatistics() map[string]int {
	stats := map[string]int{
		"total_entries":         0,
		"successful":            0,
		"failed":                0,
		"pending_retry":         0,
		"permanently_failed":    0,
		"location_cache_size":   0,
		"organizer_cache_size":  0,
		"tournament_cache_size": 0,
	}

	if useMemoryCache {
		cacheMu.RLock()
		defer cacheMu.RUnlock()

		// Use memory cache statistics
		stats["location_cache_size"] = len(LocationCache)
		stats["organizer_cache_size"] = len(OrganizerCache)
		stats["tournament_cache_size"] = len(CachedGeocoordinates)

		// Count statistics across every memory cache.
		for _, memCache := range []map[string]models.Geocoordinates{
			CachedGeocoordinates, LocationCache, OrganizerCache,
		} {
			for _, geo := range memCache {
				stats["total_entries"]++

				if geo.IsFailed {
					stats["failed"]++

					// Check if this failed entry should be retried
					if shouldRetryGeocodingRequest(geo) {
						stats["pending_retry"]++
					} else if geo.FailCount >= 4 {
						stats["permanently_failed"]++
					}
				} else if geo.Lat != "" && geo.Lon != "" {
					stats["successful"]++
				}
			}
		}
	} else if cacheStore != nil {
		// Use BoltDB statistics
		boltStats, err := cacheStore.GetCacheStatistics()
		if err != nil {
			logger.Error("Failed to get BoltDB cache statistics: %v", err)
		} else {
			for key, value := range boltStats {
				stats[key] = value
			}
		}

		// Count cache types from BoltDB
		err = cacheStore.ForEach(func(key string, value models.Geocoordinates) error {
			if strings.HasPrefix(key, "loc:") {
				stats["location_cache_size"]++
			} else if strings.HasPrefix(key, "org:") {
				stats["organizer_cache_size"]++
			} else {
				stats["tournament_cache_size"]++
			}
			return nil
		})
		if err != nil {
			logger.Error("Failed to count cache types: %v", err)
		}
	}

	return stats
}

// CleanupOldFailedEntries removes very old failed entries to prevent cache bloat
func CleanupOldFailedEntries() int {
	cleaned := 0
	cutoffTime := time.Now().Unix() - (30 * 24 * 3600) // 30 days

	if useMemoryCache {
		cacheMu.Lock()

		// Clean from every memory cache.
		for _, memCache := range []map[string]models.Geocoordinates{
			CachedGeocoordinates, LocationCache, OrganizerCache,
		} {
			for key, geo := range memCache {
				if geo.IsFailed && geo.FailCount >= 4 && geo.LastAttempt < cutoffTime {
					delete(memCache, key)
					// Also remove from BoltDB if available
					if cacheStore != nil {
						if err := cacheStore.Delete(key); err != nil {
							logger.Error("Failed to delete key %s during cleanup: %v", key, err)
						}
					}
					cleaned++
				}
			}
		}

		cacheMu.Unlock()
	} else if cacheStore != nil {
		// Clean from BoltDB directly
		var keysToDelete []string
		err := cacheStore.ForEach(func(key string, geo models.Geocoordinates) error {
			if geo.IsFailed && geo.FailCount >= 4 && geo.LastAttempt < cutoffTime {
				keysToDelete = append(keysToDelete, key)
			}
			return nil
		})
		if err != nil {
			logger.Error("Failed to iterate cache for cleanup: %v", err)
		} else {
			for _, key := range keysToDelete {
				if err := cacheStore.Delete(key); err != nil {
					logger.Error("Failed to delete key %s during cleanup: %v", key, err)
				} else {
					cleaned++
				}
			}
		}
	}

	logger.Info("Cleaned up %d old failed geocoding entries", cleaned)
	return cleaned
}

// geocodeQuery describes one attempt to resolve a place.
type geocodeQuery struct {
	value  string // what to send to the geocoder
	source string // where it came from, for logging
}

// buildGeocodeQueries returns the ordered list of place names to try for a
// tournament.
//
// Order of precedence:
//  1. a manual override for the organizer (highest confidence)
//  2. the location published by the federation, when present
//  3. candidates derived from the organizer's club name
//
// The caller stops at the first candidate that resolves inside an accepted
// state, so more specific guesses must come first.
func buildGeocodeQueries(tournament models.Tournament) ([]geocodeQuery, *clublocations.Override) {
	var queries []geocodeQuery
	seen := make(map[string]bool)

	add := func(value, source string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[strings.ToLower(value)] {
			return
		}
		seen[strings.ToLower(value)] = true
		queries = append(queries, geocodeQuery{value: value, source: source})
	}

	var override *clublocations.Override
	if table, err := clublocations.Default(); err == nil && tournament.Organizer != "" {
		if o, ok := table.Lookup(tournament.Organizer); ok {
			override = &o
			if o.City != "" {
				add(o.City, "override")
			}
		}
	}

	// The published location is usually a plain city name and is trusted
	// before anything derived from the club name.
	if loc := strings.TrimSpace(tournament.Location); loc != "" {
		add(loc, "location")
		for _, c := range placename.Candidates(loc) {
			add(c, "location-derived")
		}
	}

	for _, c := range placename.Candidates(tournament.Organizer) {
		add(c, "organizer-derived")
	}

	if len(queries) > maxGeocodeCandidates {
		queries = queries[:maxGeocodeCandidates]
	}

	return queries, override
}

// matchesState reports whether a geocoding result belongs to one of the
// accepted states.
//
// The structured address field is authoritative. DisplayName is only consulted
// as a fallback for responses without address details.
func matchesState(geo models.Geocoordinates, acceptedStates []string) bool {
	if len(acceptedStates) == 0 {
		return true // no restriction configured
	}

	for _, state := range acceptedStates {
		if state == "" {
			continue
		}
		if geo.Address.State != "" {
			if strings.EqualFold(geo.Address.State, state) {
				return true
			}
			continue
		}
		if strings.Contains(geo.DisplayName, state) {
			return true
		}
	}

	return false
}

func getGeocoordinates(state string, tournament models.Tournament) models.Geocoordinates {
	return getGeocoordinatesForStates([]string{state}, tournament)
}

// getGeocoordinatesForStates resolves a tournament's coordinates, trying each
// candidate place name until one resolves inside an accepted state.
func getGeocoordinatesForStates(acceptedStates []string, tournament models.Tournament) models.Geocoordinates {
	primaryState := ""
	if len(acceptedStates) > 0 {
		primaryState = acceptedStates[0]
	}

	queries, override := buildGeocodeQueries(tournament)

	// An override may pin coordinates directly, which skips the network.
	if override != nil && override.HasCoordinates() {
		result := models.Geocoordinates{
			Lat:         override.Lat,
			Lon:         override.Lon,
			DisplayName: override.City,
		}
		logger.Debug("Using pinned override coordinates for tournament %s (%s)",
			tournament.Id, tournament.Organizer)
		saveGeocoordinatesInCache(tournament, primaryState, result)
		return result
	}

	if override != nil && override.State != "" {
		// An override may also correct the expected state.
		acceptedStates = append([]string{override.State}, acceptedStates...)
	}

	if len(queries) == 0 {
		logger.Warn("No geocoding candidates for tournament %s (organizer %q, location %q)",
			tournament.Id, tournament.Organizer, tournament.Location)
		saveFailedGeocodingAttempt(primaryState, tournament)
		return models.Geocoordinates{}
	}

	previousFailCount := previousFailCountFor(primaryState, tournament)

	ctx, cancel := context.WithTimeout(context.Background(), httpclient.DefaultTimeout)
	defer cancel()

	var lastErr error
	// A result that matches the state but not the queried name exactly is kept
	// as a fallback while better candidates are tried. Without this, a query
	// like "Bremer" happily settles for the hamlet "Bremer Sand" instead of
	// reaching the city of Bremen via the de-adjectived candidate.
	var fallback *models.Geocoordinates
	var fallbackQuery geocodeQuery

	// Each request is rate limited, so the total number of upstream calls per
	// tournament is capped regardless of how many candidates or passes exist.
	budget := maxGeocodeRequests

	// Two passes. The first restricts results to settlements, which is what a
	// map pin should point at. Only if nothing resolves at all do we allow
	// arbitrary features, so a club in a tiny hamlet still gets a location
	// rather than the federation's default.
passes:
	for _, settlementsOnly := range []bool{true, false} {
		for _, q := range queries {
			if budget <= 0 {
				break passes
			}
			budget--

			results, err := queryNominatim(ctx, q.value, tournament.Id, settlementsOnly)
			if err != nil {
				lastErr = err
				// A transport-level failure affects every candidate equally.
				break passes
			}

			for _, candidate := range results {
				if !matchesState(candidate, acceptedStates) {
					continue
				}

				result := candidate
				result.IsFailed = false
				result.FailCount = 0
				result.LastAttempt = 0

				if placeMatchesQuery(result, q.value) {
					logger.Debug("Geocoded tournament %s via %s query %q (settlements=%v) -> %s",
						tournament.Id, q.source, q.value, settlementsOnly, result.DisplayName)
					saveGeocoordinatesInCache(tournament, primaryState, result)
					return result
				}

				if fallback == nil {
					kept := result
					fallback = &kept
					fallbackQuery = q
				}
			}
		}
	}

	// No exact name match anywhere: use the best inexact hit rather than
	// falling back to the federation's default coordinates.
	if fallback != nil {
		logger.Debug("Geocoded tournament %s via %s query %q (inexact) -> %s",
			tournament.Id, fallbackQuery.source, fallbackQuery.value, fallback.DisplayName)
		saveGeocoordinatesInCache(tournament, primaryState, *fallback)
		return *fallback
	}

	if lastErr != nil {
		logger.Error("Geocoding failed for tournament %s: %v", tournament.Id, lastErr)
	} else {
		logger.Warn("No suitable geocoordinates for tournament %s (organizer %q) in %v; tried %d candidates",
			tournament.Id, tournament.Organizer, acceptedStates, len(queries))
	}

	saveFailedGeocodingAttemptWithCount(primaryState, tournament, previousFailCount)
	return models.Geocoordinates{}
}

// placeMatchesQuery reports whether a geocoding result actually names the
// place that was asked for, rather than merely containing it.
//
// Nominatim readily returns "Bremer Sand" for "Bremer" or "Ratinger Straße"
// for "Ratinger". Accepting those produces a pin in the wrong town.
func placeMatchesQuery(geo models.Geocoordinates, query string) bool {
	place := geo.Address.Place()
	if place == "" {
		return false
	}

	normPlace := normalizePlaceName(place)
	normQuery := normalizePlaceName(query)
	if normPlace == "" || normQuery == "" {
		return false
	}

	if normPlace == normQuery {
		return true
	}

	// Accept official long forms of the same place: "Bad Homburg" ->
	// "Bad Homburg vor der Höhe", "Frankfurt" -> "Frankfurt am Main".
	//
	// Such suffixes are geographic qualifiers that begin with a lowercase
	// preposition. Requiring that is what keeps "Bremer" from matching the
	// hamlet "Bremer Sand", whose suffix is a capitalized noun.
	rest, ok := strings.CutPrefix(normPlace, normQuery+" ")
	if !ok {
		return false
	}

	first, _, _ := strings.Cut(rest, " ")
	return placeQualifiers[first]
}

// placeQualifiers introduce the geographic suffix of an official German place
// name ("Frankfurt am Main", "Bad Homburg vor der Höhe").
var placeQualifiers = map[string]bool{
	"am": true, "an": true, "im": true, "in": true, "vor": true,
	"bei": true, "auf": true, "ob": true, "unter": true, "a": true,
	"i": true, "v": true,
}

// normalizePlaceName lowercases and collapses whitespace for comparison.
func normalizePlaceName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// queryNominatim performs one geocoding request and returns the parsed results.
func queryNominatim(ctx context.Context, query, tournamentId string, settlementsOnly bool) ([]models.Geocoordinates, error) {
	// Honour the Nominatim usage policy: at most one request per second.
	// Cache hits never reach this point, so cached lookups do not consume
	// rate-limit capacity.
	if err := geocodingRateLimiter().Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter aborted: %w", err)
	}

	reqURL, err := buildNominatimURL(query, settlementsOnly)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	// Nominatim requires an identifying User-Agent.
	httpclient.ApplyDefaultHeaders(req)
	req.Header.Set("Accept", "application/json")

	res, err := httpclient.Geocoding().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		if retryAfter := res.Header.Get("Retry-After"); retryAfter != "" {
			logger.Warn("Geocoding throttled for tournament %s: status %d, Retry-After: %s",
				tournamentId, res.StatusCode, retryAfter)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxGeocodingResponseBytes))
		return nil, fmt.Errorf("unexpected status %d", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, maxGeocodingResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	var geoCoords []models.Geocoordinates
	if err := json.Unmarshal(body, &geoCoords); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return geoCoords, nil
}

// buildNominatimURL assembles the geocoding request URL with proper encoding.
//
// When settlementsOnly is set, Nominatim is restricted to cities, towns,
// villages and hamlets. Without that restriction a query like "Ratinger"
// happily matches "Ratinger Straße" in a completely different city, which is a
// major source of wrong map pins.
func buildNominatimURL(query string, settlementsOnly bool) (string, error) {
	u, err := url.Parse(nominatimBaseURL())
	if err != nil {
		return "", fmt.Errorf("invalid Nominatim base URL: %w", err)
	}

	q := u.Query()
	q.Set("limit", "5")
	q.Set("accept-language", "de")
	q.Set("format", "jsonv2")
	// Structured address details let us verify the state reliably instead of
	// substring-matching the display name.
	q.Set("addressdetails", "1")
	q.Set("countrycodes", "de")
	if settlementsOnly {
		q.Set("featureType", "settlement")
	}
	q.Set("q", query)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// previousFailCountFor returns the highest recorded failure count across the
// cache keys belonging to this tournament.
func previousFailCountFor(state string, tournament models.Tournament) int {
	var previous int
	for _, key := range geocodeCacheKeys(state, tournament) {
		if cachedGeo, exists := getFromCache(key); exists && cachedGeo.IsFailed {
			if cachedGeo.FailCount > previous {
				previous = cachedGeo.FailCount
			}
		}
	}
	return previous
}

// saveFailedGeocodingAttempt records a failure using the currently known count.
func saveFailedGeocodingAttempt(state string, tournament models.Tournament) {
	saveFailedGeocodingAttemptWithCount(state, tournament, previousFailCountFor(state, tournament))
}

// saveFailedGeocodingAttemptWithCount caches a failed geocoding attempt with
// retry metadata under the same keys used for successful lookups, so the
// progressive backoff actually takes effect on the next run.
func saveFailedGeocodingAttemptWithCount(state string, tournament models.Tournament, previousFailCount int) {
	failedEntry := models.Geocoordinates{
		Lat:         "",
		Lon:         "",
		DisplayName: "",
		LastAttempt: time.Now().Unix(),
		FailCount:   previousFailCount + 1,
		IsFailed:    true,
	}

	keys := geocodeCacheKeys(state, tournament)
	if len(keys) == 0 {
		return
	}

	for _, key := range keys {
		setInCache(key, failedEntry)
	}
}
