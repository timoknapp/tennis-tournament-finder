package openstreetmap

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

type Geocoordinates struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
}

var CachedGeocoordinates map[string]Geocoordinates

func InitCache() {
	CachedGeocoordinates = make(map[string]Geocoordinates)
}

func GetGeocoordinatesFromCache(state string, tournamentId string, tournamentAddress string) Geocoordinates {
	geoCoordinates := CachedGeocoordinates[tournamentId]
	if geoCoordinates.Lat == "" && geoCoordinates.Lon == "" && geoCoordinates.DisplayName == "" {
		fmt.Printf("No Geocoordinate Cache entry found for (%s): '%s'. Fetching data from server.\n", tournamentId, tournamentAddress)
		geoCoordinates = getGeocoordinates(state, tournamentId, tournamentAddress)
	}
	return geoCoordinates
}

func saveGeocoordinatesInCache(tournamentId string, geoCoordinates Geocoordinates) {
	CachedGeocoordinates[tournamentId] = geoCoordinates
}

func getGeocoordinates(state string, tournamentId string, tournamentAddress string) Geocoordinates {
	// https://nominatim.openstreetmap.org/search.php?q=MTV+Karlsruhe&limit=1&format=jsonv2
	const urlOSM string = "https://nominatim.openstreetmap.org/search.php?limit=3&accept-language=de&format=jsonv2&q="
	query := tournamentAddress
	// fmt.Printf("Get Geocoordinates for %s\n", query)
	// Replace association club abbreviations
	replaceableStrings := []string{"e.V.", "TC", "TK", "TG", "TV", "SG", "GW", "BW", "RW", "SW", "SF", "SC", "TSG", "SV", "Tennis-Club", "Tennisclub", "Tennisklub", "Rot-Weiß", "Blau-Weiß", "Grün-Weiß", "Grün-Gelb", "Grün-Weiß-Rot", "Schwarz-Weiß", "Turnverein", "Turn- u. Sportverein", "Sportvereine", "Sportverein", "Turnverein", "Tenniskreis", "Sportgemeinschaft", "Tennisgemeinschaft", "Tennisverein", "Tennis"}
	for i := 0; i < len(replaceableStrings); i++ {
		query = strings.ReplaceAll(query, replaceableStrings[i], "")
	}
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

	var geoCoords []Geocoordinates
	var result Geocoordinates
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
