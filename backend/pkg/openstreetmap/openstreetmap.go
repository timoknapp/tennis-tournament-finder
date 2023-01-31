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
		query = tournamentOrganizer
		// Replace association club abbreviations
		replaceableStrings := []string{"e.V.", "TC", "TK", "TG", "TV", "SG", "GW", "BW", "RW", "SW", "SF", "SC", "TSG", "SV", "Tennis-Club", "Tennisclub", "Tennisklub", "Rot-Weiß", "Blau-Weiß", "Grün-Weiß", "Grün-Gelb", "Grün-Weiß-Rot", "Schwarz-Weiß", "Turnverein", "Turn- u. Sportverein", "Sportvereine", "Sportverein", "Turnverein", "Tenniskreis", "Sportgemeinschaft", "Tennisgemeinschaft", "Tennisverein", "Tennis"}
		for i := 0; i < len(replaceableStrings); i++ {
			query = strings.ReplaceAll(query, replaceableStrings[i], "")
		}
	}
	// fmt.Printf("Get Geocoordinates for %s\n", query)
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
