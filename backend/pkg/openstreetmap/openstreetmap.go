package openstreetmap

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/timoknapp/tennis-tournament-finder/pkg/cache"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
)

var CachedGeocoordinates map[string]models.Geocoordinates
var LocationCache map[string]models.Geocoordinates
var OrganizerCache map[string]models.Geocoordinates

var cacheStore cache.Store
var useMemoryCache bool

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
	CachedGeocoordinates = make(map[string]models.Geocoordinates)
	LocationCache = make(map[string]models.Geocoordinates)
	OrganizerCache = make(map[string]models.Geocoordinates)

	if useMemoryCache && cacheStore != nil {
		// Preload BoltDB data into memory when memory cache is enabled
		logger.Info("Loading existing cache data from BoltDB into memory...")
		err := cacheStore.ForEach(func(key string, value models.Geocoordinates) error {
			// Determine which cache map to populate based on key prefix
			if strings.HasPrefix(key, "loc:") {
				LocationCache[key] = value
			} else if strings.HasPrefix(key, "org:") {
				OrganizerCache[key] = value
			} else {
				// Tournament-specific cache (no prefix)
				CachedGeocoordinates[key] = value
			}
			return nil
		})
		if err != nil {
			logger.Error("Failed to preload cache data: %v", err)
		} else {
			logger.Info("Preloaded %d tournament, %d location, %d organizer entries from BoltDB",
				len(CachedGeocoordinates), len(LocationCache), len(OrganizerCache))
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
	// Priority 1: Check location-based cache if we have a specific location
	if len(tournament.Location) > 0 {
		locationKey := generateLocationCacheKey(tournament.Location, state)
		if cachedGeo, exists := getFromCache(locationKey); exists {
			if cachedGeo.Lat != "" && cachedGeo.Lon != "" {
				logger.Debug("Cache HIT (location): %s for tournament %s", locationKey, tournament.Id)
				return cachedGeo
			}
			// Handle failed location cache entries
			if cachedGeo.IsFailed && !shouldRetryGeocodingRequest(cachedGeo) {
				logger.Debug("Skipping geocoding retry for location (%s): '%s' (failed %d times)",
					tournament.Id, tournament.Location, cachedGeo.FailCount)
				// Don't return failed location cache, try organizer cache next
			}
		}
	}

	// Priority 2: Check organizer-based cache
	if len(tournament.Organizer) > 0 {
		organizerKey := generateOrganizerCacheKey(tournament.Organizer, state)
		if cachedGeo, exists := getFromCache(organizerKey); exists {
			if cachedGeo.Lat != "" && cachedGeo.Lon != "" {
				logger.Debug("Cache HIT (organizer): %s for tournament %s", organizerKey, tournament.Id)
				return cachedGeo
			}
			// Handle failed organizer cache entries
			if cachedGeo.IsFailed && !shouldRetryGeocodingRequest(cachedGeo) {
				logger.Debug("Skipping geocoding retry for organizer (%s): '%s' (failed %d times)",
					tournament.Id, tournament.Organizer, cachedGeo.FailCount)
				// Don't return failed organizer cache, try tournament cache next
			}
		}
	}

	// Priority 3: Check tournament-specific cache (existing behavior)
	if cachedGeo, exists := getFromCache(tournament.Id); exists {
		// If we have successful coordinates, return them
		if cachedGeo.Lat != "" && cachedGeo.Lon != "" {
			logger.Debug("Cache HIT (tournament): %s", tournament.Id)
			return cachedGeo
		}

		// If this is a failed attempt, check if we should retry
		if cachedGeo.IsFailed {
			shouldRetry := shouldRetryGeocodingRequest(cachedGeo)
			if !shouldRetry {
				logger.Debug("Skipping geocoding retry for tournament (%s): '%s' (failed %d times, last attempt: %v)",
					tournament.Id, tournament.Organizer, cachedGeo.FailCount, time.Unix(cachedGeo.LastAttempt, 0))
				return cachedGeo // Return the failed entry (will fallback to default coords in calling code)
			}
			logger.Debug("Retrying geocoding for tournament (%s): '%s' (retry attempt after %d failures)",
				tournament.Id, tournament.Organizer, cachedGeo.FailCount)
		}
	}

	logger.Debug("No Geocoordinate Cache entry found for (%s): '%s' at '%s'. Fetching data from server.",
		tournament.Id, tournament.Organizer, tournament.Location)
	geoCoordinates := getGeocoordinates(state, tournament)
	return geoCoordinates
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
	// Always save to tournament-specific cache (existing behavior)
	setInCache(tournament.Id, geoCoordinates)

	// Also save to location cache if we have a location
	if len(tournament.Location) > 0 {
		locationKey := generateLocationCacheKey(tournament.Location, state)
		setInCache(locationKey, geoCoordinates)
		logger.Debug("Cached geocoordinates for location key: %s", locationKey)
	}

	// Also save to organizer cache if we have an organizer
	if len(tournament.Organizer) > 0 {
		organizerKey := generateOrganizerCacheKey(tournament.Organizer, state)
		setInCache(organizerKey, geoCoordinates)
		logger.Debug("Cached geocoordinates for organizer key: %s", organizerKey)
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
		// Use memory cache statistics
		stats["location_cache_size"] = len(LocationCache)
		stats["organizer_cache_size"] = len(OrganizerCache)
		stats["tournament_cache_size"] = len(CachedGeocoordinates)

		// Count tournament cache statistics
		for _, geo := range CachedGeocoordinates {
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
		// Clean from memory cache
		for tournamentId, geo := range CachedGeocoordinates {
			if geo.IsFailed && geo.FailCount >= 4 && geo.LastAttempt < cutoffTime {
				delete(CachedGeocoordinates, tournamentId)
				// Also remove from BoltDB if available
				if cacheStore != nil {
					cacheStore.Delete(tournamentId)
				}
				cleaned++
			}
		}
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

// extractCityFromOrganizerName intelligently extracts the city name from the organizer string
func extractCityFromOrganizerName(organizer string) string {
	// Handle special cases first
	if cityFromSpecialCases := handleSpecialCases(organizer); cityFromSpecialCases != "" {
		return cityFromSpecialCases
	}

	// Remove common prefixes and suffixes first
	removeablePrefixesSuffixes := []string{
		"e.V.", "e. V.", "e.v.",
		", TA", " TA", "- TA", " , TA",
		"Abt. Tennis", "- Abt. Tennis", "Abt.",
		"von 1845", "von 1890", "1890", "1845", "1911", "1920", "1920/75", "1975", "1970", "1974", "1923", "1897", "1896", "08/29", "05", "50",
		"zu",
	}

	// Remove common club type abbreviations and full names
	removeableClubTypes := []string{
		"Turnverein", "Turn- u. Sportverein", "Tennisverein", "Tennis-Club", "Tennisclub", "Tennisklub",
		"Sportverein", "Sport-Verein", "Sportvereinigung", "Sportgemeinschaft", "Tennisgemeinschaft",
		"Sport-Verein",
		"TC", "TK", "TG", "TV", "SG", "SV", "SKV", "FC", "ATV", "SuS", "TSG", "SC", "SF", "TSC",
		"Tennis", "DJK", "Post", "Tura", "Germania", "Bezirk", "Optimus", "Olympia", "Nicolai", "Club",
	}

	// Remove color combinations and specific club names
	removeableColors := []string{
		"Rot-Weiß", "Blau-Weiß", "Grün-Weiß", "Grün-Weiss", "Grün-Gelb", "Grün-Weiß-Rot",
		"Blau-Gelb", "Schwarz-Weiß", "Grün Weiß", "Grün Weiss", "Weiss-Rot",
		"GW", "BW", "RW", "SW",
	}

	query := organizer

	// Remove all the unwanted parts
	allRemovables := append(append(removeablePrefixesSuffixes, removeableClubTypes...), removeableColors...)
	for _, removable := range allRemovables {
		query = strings.ReplaceAll(query, removable, "")
	}

	// Clean up extra spaces and trim
	query = strings.TrimSpace(strings.ReplaceAll(query, "  ", " "))

	// Handle special cases and extract meaningful city names
	cityName := extractCityFromPattern(query, organizer)

	// Final fallback
	if cityName == "" {
		return organizer
	}

	return cityName
}

// handleSpecialCases handles specific known problematic cases
func handleSpecialCases(organizer string) string {
	// Handle "Post Südstadt Karlsruhe" type cases - prefer the main city
	if strings.Contains(organizer, "Post Südstadt Karlsruhe") {
		return "Karlsruhe"
	}
	if strings.Contains(organizer, "Heidelberger Tennis-Club") {
		return "Heidelberg"
	}
	if strings.Contains(organizer, "Eppelheimer Tennis-Club") {
		return "Eppelheim"
	}
	if strings.Contains(organizer, "Karbener Sportverein") {
		return "Karben"
	}
	if strings.Contains(organizer, "Unterbarmer Tennisclub") {
		return "Wuppertal" // Unterbarmen is a district of Wuppertal
	}
	if strings.Contains(organizer, "Ratinger Tennisclub") {
		return "Ratingen"
	}
	if strings.Contains(organizer, "Lohausener Sport-Verein") {
		return "Düsseldorf" // Lohausen is a district of Düsseldorf
	}

	// Handle adjective forms ending in -er
	words := strings.Fields(organizer)
	for _, word := range words {
		if strings.HasSuffix(word, "er") && len(word) > 4 {
			// Try different transformations for German adjective → city name
			transformations := []string{
				strings.TrimSuffix(word, "er"),        // Heidelberger → Heidelberg
				strings.TrimSuffix(word, "er") + "en", // Ratinger → Ratingen
				strings.TrimSuffix(word, "er") + "m",  // Eppelheimer → Eppelheim
			}

			for _, candidate := range transformations {
				if isLikelyCityName(candidate) {
					return candidate
				}
			}
		}
	}

	return ""
}

// extractCityFromPattern uses various patterns to extract city names
func extractCityFromPattern(cleanedQuery string, originalOrganizer string) string {
	words := strings.Fields(cleanedQuery)
	if len(words) == 0 {
		return ""
	}

	// Pattern 1: Look for compound city names (German cities often have hyphens or are compound)
	for _, word := range words {
		if isLikelyCityName(word) {
			return word
		}
	}

	// Pattern 2: Extract from specific known patterns
	if cityFromSpecialPattern := extractFromSpecialPatterns(originalOrganizer); cityFromSpecialPattern != "" {
		return cityFromSpecialPattern
	}

	// Pattern 3: Look for the longest meaningful word that could be a city
	var bestCandidate string
	for _, word := range words {
		if len(word) >= 4 && isCapitalized(word) && !isCommonClubWord(word) && len(word) > len(bestCandidate) {
			bestCandidate = word
		}
	}

	if bestCandidate != "" {
		return bestCandidate
	}

	// Pattern 4: Take the first meaningful word
	for _, word := range words {
		if len(word) >= 3 && isCapitalized(word) && !isCommonClubWord(word) {
			return word
		}
	}

	return ""
}

// isLikelyCityName checks if a word looks like a German city name
func isLikelyCityName(word string) bool {
	if len(word) < 3 || !isCapitalized(word) {
		return false
	}

	// German city patterns
	cityPatterns := []string{
		"heim", "hausen", "feld", "berg", "burg", "furt", "stadt", "dorf", "bach", "tal", "au",
		"weiler", "kirchen", "ingen", "ungen", "stein", "bronn", "brunn", "baden", "bad",
	}

	wordLower := strings.ToLower(word)
	for _, pattern := range cityPatterns {
		if strings.HasSuffix(wordLower, pattern) {
			return true
		}
	}

	// Known city names that don't follow patterns
	knownCities := []string{
		"Leipzig", "Erfurt", "Pinnow", "Apolda", "Speyer", "Konstanz", "Lorsch", "Karlsruhe",
		"Duisburg", "Wesel", "Dümpten", "Eigen", "Büderich", "Wixhausen", "Neckarau", "Denzlingen",
		"Niederursel", "Blumberg", "Ratingen", "Büttelborn", "Ladenburg", "Offenthal", "Niefern",
		"Öschelbronn", "Buchen", "Mönchengladbach", "Unterfeldhaus", "Friedrichsfeld", "Bermatingen",
		"Mülheim", "Heißen", "Mörfelden", "Lußheim", "Großsachsen", "Wössingen", "Mühlhausen",
		"Dauchingen", "Schriesheim", "Eppelheim", "Durmersheim", "Wiesental", "Grenzach", "Malsch",
		"Eggenstein", "Mackenbach", "Dreieichenhain", "Mehrhoog", "Heidelberg", "Kassel", "Nordshausen",
		"Karben", "Wuppertal", "Düsseldorf",
	}

	for _, city := range knownCities {
		if strings.EqualFold(word, city) || strings.Contains(strings.ToLower(word), strings.ToLower(city)) {
			return true
		}
	}

	return false
}

// extractFromSpecialPatterns handles specific naming patterns
func extractFromSpecialPatterns(organizer string) string {
	// Pattern: "City-Suffix" format
	if strings.Contains(organizer, "-") {
		parts := strings.Split(organizer, "-")
		for _, part := range parts {
			cleaned := strings.TrimSpace(part)
			if isLikelyCityName(cleaned) {
				return cleaned
			}
		}
	}

	// Pattern: "Prefix City" format
	parts := strings.Fields(organizer)
	if len(parts) >= 2 {
		for i, part := range parts {
			if isLikelyCityName(part) {
				return part
			}
			// Check compound words
			if i < len(parts)-1 {
				compound := part + parts[i+1]
				if isLikelyCityName(compound) {
					return compound
				}
			}
		}
	}

	return ""
} // Helper functions
func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isCapitalized(s string) bool {
	if len(s) == 0 {
		return false
	}
	first := rune(s[0])
	return first >= 'A' && first <= 'Z'
}

func isCommonClubWord(word string) bool {
	commonWords := []string{
		"Tennis", "Club", "Verein", "Sport", "Turn", "Klub", "Gemeinschaft", "Sportverein",
		"Tennisverein", "Sportgemeinschaft", "Tennisgemeinschaft", "Turnverein", "Sportvereinigung",
		"Optimus", "Olympia", "Germania", "Nicolai", "Post", "Tura", "Bezirk", "Karbener",
		"Heidelberger", "Ratinger", "Lohausener", "Unterbarmer", "Eppelheimer",
		"Südstadt", // District names that aren't the main city
	}
	for _, common := range commonWords {
		if strings.EqualFold(word, common) {
			return true
		}
	}
	return false
}

func getGeocoordinates(state string, tournament models.Tournament) models.Geocoordinates {
	// 1. Get Coordinates by tournament.Location if Location does not exists
	// 2. Get Coordinates by tournament.Organizer if this does not work out
	// 3. Set Default Coords
	var tournamentId string = tournament.Id
	var tournamentOrganizer string = tournament.Organizer
	// https://nominatim.openstreetmap.org/search.php?q=MTV+Karlsruhe&limit=1&format=jsonv2
	const urlOSM string = "https://nominatim.openstreetmap.org/search.php?limit=3&accept-language=de&format=jsonv2&q="
	// query := tournamentOrganizer
	var query string = ""
	if len(tournament.Location) > 0 {
		query = tournament.Location
	} else {
		query = extractCityFromOrganizerName(tournamentOrganizer)
	}
	urlFormattedQuery := strings.ReplaceAll(query, " ", "+")

	// Get previous failure count for progressive backoff
	var previousFailCount int
	if cachedGeo, exists := getFromCache(tournamentId); exists && cachedGeo.IsFailed {
		previousFailCount = cachedGeo.FailCount
	}

	res, err := http.Get(urlOSM + urlFormattedQuery)
	if err != nil {
		logger.Error("HTTP error for tournament %s: %v", tournamentId, err)
		saveFailedGeocodingAttempt(tournamentId, previousFailCount)
		return models.Geocoordinates{}
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		logger.Error("Read error for tournament %s: %v", tournamentId, err)
		saveFailedGeocodingAttempt(tournamentId, previousFailCount)
		return models.Geocoordinates{}
	}

	var geoCoords []models.Geocoordinates
	var result models.Geocoordinates
	json.Unmarshal([]byte(string(body)), &geoCoords)
	if len(geoCoords) > 0 {
		// Check if coordinates belong to the correct states (region).
		for i := 0; i < len(geoCoords); i++ {
			if strings.Contains(geoCoords[i].DisplayName, state) {
				result = geoCoords[i]
				// Reset failure tracking on success
				result.IsFailed = false
				result.FailCount = 0
				result.LastAttempt = 0
				saveGeocoordinatesInCache(tournament, state, result)
				return result
			}
		}
	}

	// No suitable coordinates found - cache this as a failed attempt
	logger.Warn("No suitable geocoordinates found for tournament %s in state %s", tournamentId, state)
	saveFailedGeocodingAttempt(tournamentId, previousFailCount)
	return models.Geocoordinates{}
}

// saveFailedGeocodingAttempt caches a failed geocoding attempt with retry metadata
func saveFailedGeocodingAttempt(tournamentId string, previousFailCount int) {
	failedEntry := models.Geocoordinates{
		Lat:         "",
		Lon:         "",
		DisplayName: "",
		LastAttempt: time.Now().Unix(),
		FailCount:   previousFailCount + 1,
		IsFailed:    true,
	}
	setInCache(tournamentId, failedEntry)
}
