package tournament

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/timoknapp/tennis-tournament-finder/pkg/openstreetmap"
	"github.com/timoknapp/tennis-tournament-finder/pkg/util"
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

func GetTournaments(w http.ResponseWriter, r *http.Request) {
	util.EnableCors(&w)
	Tournaments := []Tournament{}

	fmt.Printf("Cache consists currenty of: %d geocoordinates.\n", len(openstreetmap.CachedGeocoordinates))

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
	var defaultGeocoords openstreetmap.Geocoordinates
	var state = ""
	const urlBAD string = "https://baden.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar"
	const urlHTV string = "https://htv.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar"
	var geoCoordsBAD = openstreetmap.Geocoordinates{Lat: "49.34003", Lon: "8.68514"}
	var geoCoordsHTV = openstreetmap.Geocoordinates{Lat: "50.0770372", Lon: "8.7553832"}
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
			value := util.RemoveFormatFromString(columnTournament.Text())
			// fmt.Printf("Column No: %d ; Column Value: %s\n", i, value)
			if idxColumn == 0 {
				tournament.Date = value
			}
			if idxColumn == 1 {
				urlElement := columnTournament.Find("a")
				tournament.Title = util.RemoveFormatFromString(urlElement.Text())
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
						fmt.Printf("Tournament address missing: %s ; Date: %s\n", util.RemoveFormatFromString(columnTournament.Find("a").Text()), tournament.Date)
					}
					address := util.RemoveFormatFromString(extractedAddress)
					// fmt.Printf("Extracted Address: %s\n", address)
					tournament.Address = address

					geoCoords := openstreetmap.GetGeocoordinatesFromCache(state, tournament.Id, tournament.Address)
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

func getTournamentIdByUrl(tournamentUrl string) string {
	array := strings.Split(tournamentUrl, "tournamentId=")
	if len(array) > 1 {
		return array[1]
	} else {
		return ""
	}
}
