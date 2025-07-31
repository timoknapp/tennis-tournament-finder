package openstreetmap

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
)

var CachedGeocoordinates map[string]models.Geocoordinates

func InitCache() {
	CachedGeocoordinates = make(map[string]models.Geocoordinates)
}

func GetGeocoordinatesFromCache(state string, tournament models.Tournament) models.Geocoordinates {
	geoCoordinates := CachedGeocoordinates[tournament.Id]
	if geoCoordinates.Lat == "" && geoCoordinates.Lon == "" && geoCoordinates.DisplayName == "" {
		fmt.Printf("No Geocoordinate Cache entry found for (%s): '%s'. Fetching data from server.\n", tournament.Id, tournament.Organizer)
		geoCoordinates = getGeocoordinates(state, tournament)
	}
	return geoCoordinates
}

func saveGeocoordinatesInCache(tournamentId string, geoCoordinates models.Geocoordinates) {
	CachedGeocoordinates[tournamentId] = geoCoordinates
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
	// fmt.Printf("Get Geocoordinates for %s (%s)\n", query, tournamentOrganizer)
	urlFormattedQuery := strings.ReplaceAll(query, " ", "+")

	res, err := http.Get(urlOSM + urlFormattedQuery)
	if err != nil {
		fmt.Println(err)
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		fmt.Println(err)
	}
	// fmt.Println(string(body))

	var geoCoords []models.Geocoordinates
	var result models.Geocoordinates
	json.Unmarshal([]byte(string(body)), &geoCoords)
	if len(geoCoords) > 0 {
		// Check if coordinates belong to the correct states (region).
		for i := 0; i < len(geoCoords); i++ {
			if strings.Contains(geoCoords[i].DisplayName, state) {
				result = geoCoords[i]
				saveGeocoordinatesInCache(tournamentId, result)
			}
		}
		// fmt.Printf("Lat: %s, Lon: %s, Name: %s\n", result.Lat, result.Lon, result.DisplayName)
	}
	return result
}
