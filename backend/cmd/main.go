package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type Tournament struct {
	Id      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Date    string `json:"date"`
	Address string `json:"address"`
	Lat     string `json:"lat"`
	Lon     string `json:"lon"`
}

type Geocoordinates struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
}

var cachedGeocoordinates map[string]Geocoordinates

func getTournaments(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	Tournaments := []Tournament{}

	fmt.Printf("Cache consists currenty of: %d geocoordinates.\n", len(cachedGeocoordinates))

	today := time.Now()
	dateFrom := r.URL.Query().Get("dateFrom")
	if dateFrom == "" {
		dateFrom = today.Format("02.01.2006")
	}
	dateTo := r.URL.Query().Get("dateTo")
	if dateTo == "" {
		dateIn14Days := today.Add(time.Hour * 336)
		dateTo = dateIn14Days.Format("02.01.2006")
	}
	fmt.Printf("Get Tournaments from: %s to: %s\n", dateFrom, dateTo)

	tournamentsHTV := getTournamentsFromFederation("HTV", dateFrom, dateTo)
	tournamentsBAD := getTournamentsFromFederation("BAD", dateFrom, dateTo)

	Tournaments = append(tournamentsBAD, tournamentsHTV...)

	json.NewEncoder(w).Encode(Tournaments)
}

func getTournamentsFromFederation(federation string, dateFrom string, dateTo string) []Tournament {
	fmt.Printf("Get Tournaments in: %s from: %s to: %s\n", federation, dateFrom, dateTo)
	var tournaments []Tournament

	var url = ""
	var defaultGeocoords Geocoordinates
	var state = ""
	const urlBAD string = "https://baden.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar"
	const urlHTV string = "https://htv.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar"
	var geoCoordsBAD = Geocoordinates{Lat: "49.34003", Lon: "8.68514"}
	var geoCoordsHTV = Geocoordinates{Lat: "50.0770372", Lon: "8.7553832"}
	var stateBAD = "Baden-Württemberg"
	var stateHTV = "Hessen"

	switch federation {
	case "BAD":
		url = urlBAD
		defaultGeocoords = geoCoordsBAD
		state = stateBAD
	case "HTV":
		url = urlHTV
		defaultGeocoords = geoCoordsHTV
		state = stateHTV
	default:
		federation = "BAD"
		url = urlBAD
		defaultGeocoords = geoCoordsBAD
		state = stateBAD
	}

	var valuationState = "1"         // 0=No-LK-Status, 1=LK-Status, 2=DTB-Status
	var queryYoungOld = "2"          // 1=Youth, 2=Adult, 3=Senior
	var compType = "Herren%2BEinzel" // Tournament Type: Herren%2BEinzel, Herren%2BDoppel, Damen%2BEinzel, Damen%2BDoppel, Senioren%2BEinzel, Senioren%2BDoppel, Jugend%2BEinzel, Jugend%2BDoppel
	var fedRank = "21"               // LK Rank: 1-23

	payload := strings.NewReader("queryName=&queryDateFrom=" + dateFrom + "&queryDateTo=" + dateTo + "&valuationState=" + valuationState + "&queryYoungOld=" + queryYoungOld + "&compType=" + compType + "&fedRank=" + fedRank + "&federation=" + federation)

	client := &http.Client{}
	req, err := http.NewRequest("POST", url, payload)

	if err != nil {
		fmt.Println(err)
		return tournaments
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	res, err := client.Do(req)
	if err != nil {
		fmt.Println(err)
		return tournaments
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		fmt.Println(err)
	}

	// Find the tournament items
	doc.Find(".result-set tr").Each(func(idxRow int, rowTournament *goquery.Selection) {
		// For each item found, get the tournament data
		var tournament Tournament
		rowTournament.Find("td").Each(func(idxColumn int, columnTournament *goquery.Selection) {
			// Column 0: Date, Column 1: Title; Column 2: Type; Column 3: LK; Column 4: Open for Nation
			value := removeFormatFromString(columnTournament.Text())
			// fmt.Printf("Column No: %d ; Column Value: %s\n", i, value)
			if idxColumn == 0 {
				tournament.Date = value
			}
			if idxColumn == 1 {
				urlElement := columnTournament.Find("a")
				tournament.Title = removeFormatFromString(urlElement.Text())
				tournament.URL, _ = urlElement.Attr("href")
				//https://mybigpoint.tennis.de/web/guest/turniersuche?tournamentId=484582
				tournament.Id = getTournamentIdByUrl(tournament.URL)

				if len(tournament.Title) > 0 {
					array := strings.Split(columnTournament.Text(), "\n\t\n\n\n")
					// fmt.Println("Title Array: " + "'" + strings.Join(array, `','`) + `'`)
					var extractedAddress = ""
					if len(array) > 1 {
						extractedAddress = array[1]
					} else {
						fmt.Printf("Tournament address missing: %s ; Date: %s\n", removeFormatFromString(columnTournament.Find("a").Text()), tournament.Date)
					}
					address := removeFormatFromString(extractedAddress)
					// fmt.Printf("Extracted Address: %s\n", address)
					tournament.Address = address

					geoCoords := getGeocoordinatesFromCache(state, tournament)
					if geoCoords.Lat == "" || geoCoords.Lon == "" {
						fmt.Printf("No Geocoordinates could be found for (%s): '%s'. Falling back to default in '%s'.\n", tournament.Id, tournament.Address, state)
						geoCoords = defaultGeocoords
					}
					tournament.Lat = geoCoords.Lat
					tournament.Lon = geoCoords.Lon
				}
			}
		})

		// fmt.Println(tournament)
		if len(tournament.Title) > 0 {
			tournaments = append(tournaments, tournament)
		}
	})

	// fmt.Printf("%q\n", tournaments)
	return tournaments
}

func getGeocoordinatesFromCache(state string, tournament Tournament) Geocoordinates {
	geoCoordinates := cachedGeocoordinates[tournament.Id]
	if geoCoordinates.Lat == "" && geoCoordinates.Lon == "" && geoCoordinates.DisplayName == "" {
		fmt.Printf("No Geocoordinate Cache entry found for (%s): '%s'. Fetching data from server.\n", tournament.Id, tournament.Address)
		geoCoordinates = getGeocoordinates(state, tournament)
	}
	return geoCoordinates
}

func saveGeocoordinatesInCache(tournament Tournament, geoCoordinates Geocoordinates) {
	cachedGeocoordinates[tournament.Id] = geoCoordinates
}

func getTournamentIdByUrl(tournamentUrl string) string {
	array := strings.Split(tournamentUrl, "tournamentId=")
	if len(array) > 1 {
		return array[1]
	} else {
		return ""
	}
}

func getTournamentInfo(tournament Tournament) Tournament {
	// client := &http.Client{}
	fmt.Printf("Get Tournament Detail Info for %s from: %s\n", tournament.Title, tournament.URL)
	res, err := http.Get(tournament.URL)

	if err != nil {
		fmt.Println(err)
		return tournament
	}

	// res, err := client.Do(req)
	// if err != nil {
	// 	fmt.Println(err)
	// 	return tournament
	// }
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		fmt.Println(err)
		return tournament
	}

	os.WriteFile("output.html", body, 0644)
	if err != nil {
		log.Fatal(err)
	}

	// fmt.Println(string(body))
	bodyAsString := string(body)
	if strings.Contains(bodyAsString, "Meldebeginn") {
		fmt.Println("Meldebeginn")
		index := strings.Index(bodyAsString, "Meldebeginn")
		fmt.Println(bodyAsString[index : index+300])
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		fmt.Println(err)
	}

	doc.Find(".z-div span").Each(func(i int, s *goquery.Selection) {
		// For each item found, get the title
		// item := s.Find("span")
		fmt.Println(s.Text())
		fmt.Println(s.Next().Text())
		// if item.Text() == "Termin" {
		// }

	})

	return tournament
}

func getGeocoordinates(state string, tournament Tournament) Geocoordinates {
	// https://nominatim.openstreetmap.org/search.php?q=MTV+Karlsruhe&limit=1&format=jsonv2
	const urlOSM string = "https://nominatim.openstreetmap.org/search.php?limit=3&accept-language=de&format=jsonv2&q="
	query := tournament.Address
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
				saveGeocoordinatesInCache(tournament, result)
			}
		}
		// fmt.Printf("Lat: %s, Lon: %s, Name: %s\n", result.Lat, result.Lon, result.DisplayName)
	}
	return result
}

func removeFormatFromString(input string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(input, "  ", ""), "\n", ""), "\t", "")
}

func delete_empty(s []string) []string {
	var r []string
	for _, str := range s {
		if str != "" {
			r = append(r, str)
		}
	}
	return r
}

func enableCors(w *http.ResponseWriter) {
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
}

func main() {
	cachedGeocoordinates = make(map[string]Geocoordinates)
	http.HandleFunc("/", getTournaments)
	log.Fatal(http.ListenAndServe(":8080", nil))
}