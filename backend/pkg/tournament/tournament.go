package tournament

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/timoknapp/tennis-tournament-finder/pkg/federation"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
	"github.com/timoknapp/tennis-tournament-finder/pkg/openstreetmap"
	"github.com/timoknapp/tennis-tournament-finder/pkg/util"
)

func GetTournaments(w http.ResponseWriter, r *http.Request) {
	federations := federation.GetFederations()

	util.EnableCors(&w)
	Tournaments := []models.Tournament{}

	fmt.Printf("Cache consists currently of: %d geocoordinates.\n", len(openstreetmap.CachedGeocoordinates))

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

	var wg sync.WaitGroup
	for i := 0; i < len(federations); i++ {
		wg.Add(1)
		go func(fed models.Federation) {
			defer wg.Done()
			if fed.ApiVersion == "old" {
				tournaments := getTournamentsFromFederationOldApi(fed, dateFrom, dateTo)
				Tournaments = append(Tournaments, tournaments...)
			} else if fed.ApiVersion == "new" {
				tournaments := getTournamentsFromFederationNewApi(fed, dateFrom, dateTo)
				Tournaments = append(Tournaments, tournaments...)
			}
		}(federations[i])
	}
	wg.Wait()

	json.NewEncoder(w).Encode(Tournaments)
}

func getTournamentsFromFederationNewApi(federation models.Federation, dateFrom string, dateTo string) []models.Tournament {
	fmt.Printf("Get Tournaments in: %s from: %s to: %s\n", federation.Id, dateFrom, dateTo)
	var tournaments []models.Tournament

	var url = federation.Url
	var trustedProperties = federation.TrustedProperties
	var defaultGeocoords = federation.Geocoordinates
	var state = federation.State

	var fedRankValuation = "true"
	var fedRank = "20"
	var firstResult = "0"
	var maxResults = "100"

	payload := strings.NewReader("tx_nuportalrs_nuportalrs[__trustedProperties]=" + trustedProperties + "&tx_nuportalrs_nuportalrs[tournamentsFilter][ageCategory]=general" + "&tx_nuportalrs_nuportalrs[tournamentsFilter][fedRankValuation]=" + fedRankValuation + "&tx_nuportalrs_nuportalrs[tournamentsFilter][fedRank]=" + fedRank + "&tx_nuportalrs_nuportalrs[tournamentsFilter][startDate]=" + dateFrom + "&tx_nuportalrs_nuportalrs[tournamentsFilter][endDate]=" + dateTo + "&tx_nuportalrs_nuportalrs[tournamentsFilter][firstResult]=" + firstResult + "&tx_nuportalrs_nuportalrs[tournamentsFilter][maxResults]=" + maxResults)
	// fmt.Println("payload:", payload)

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

	// fmt.Println("response Status:", res.Status)
	// fmt.Println("response Headers:", res.Header)

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		fmt.Println(err)
	}

	// Find the tournament items
	doc.Find(".responsive-individual tbody tr").Each(func(idxRow int, rowTournament *goquery.Selection) {
		// For each item found, get the tournament data
		var tournament models.Tournament
		rowTournament.Find("td").Each(func(idxColumn int, columnTournament *goquery.Selection) {
			// Column 0: Date, Column 1: Title; Column 2: Type
			value := util.RemoveFormatFromString(columnTournament.Text())
			// fmt.Printf("Column No: %d ; Column Value: %s\n", idxColumn, value)
			if idxColumn == 0 && columnTournament.HasClass("daterange") {
				tournament.Date = strings.TrimSpace(value)
			}
			if idxColumn == 1 && strings.Contains(columnTournament.Text(), "Veranstalter") {
				unformattedColumnText := util.RemoveFormatFromString(columnTournament.Text())

				urlElement := columnTournament.Find("a").First()
				tournament.Title = strings.TrimSpace(util.RemoveFormatFromString(urlElement.Text()))
				tournament.URL, _ = urlElement.Attr("href")
				//https://mybigpoint.tennis.de/web/guest/turniersuche?tournamentId=484582
				tournament.Id = getTournamentIdByUrl(tournament.URL)

				extractedOrganizer, organizerExists := util.GetStringInBetweenTwoString(unformattedColumnText, "Veranstalter: ", "Austragungsort")
				if organizerExists {
					tournament.Organizer = strings.TrimSpace(extractedOrganizer)
				}

				extractedLocation, locationExists := util.GetStringInBetweenTwoString(unformattedColumnText, "Austragungsort: ", "Meldeschluss")
				if locationExists {
					tournament.Location = strings.TrimSpace(extractedLocation)
					geoCoords := openstreetmap.GetGeocoordinatesFromCache(state, tournament)
					if geoCoords.Lat == "" || geoCoords.Lon == "" {
						fmt.Printf("No Geocoordinates could be found for (%s): '%s'. Falling back to default in '%s'.\n", tournament.Id, tournament.Location, state)
						geoCoords = defaultGeocoords
					}
					tournament.Lat = geoCoords.Lat
					tournament.Lon = geoCoords.Lon
				} else {
					fmt.Printf("Tournament location missing: %s ; Date: %s\n", util.RemoveFormatFromString(tournament.Title), tournament.Date)
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

func getTournamentsFromFederationOldApi(federation models.Federation, dateFrom string, dateTo string) []models.Tournament {
	fmt.Printf("Get Tournaments in: %s from: %s to: %s\n", federation.Id, dateFrom, dateTo)
	var tournaments []models.Tournament

	var url = federation.Url
	var defaultGeocoords = federation.Geocoordinates
	var state = federation.State

	var valuationState = "1"         // 0=No-LK-Status, 1=LK-Status, 2=DTB-Status
	var queryYoungOld = "2"          // 1=Youth, 2=Adult, 3=Senior
	var compType = "Herren%2BEinzel" // Tournament Type: Herren%2BEinzel, Herren%2BDoppel, Damen%2BEinzel, Damen%2BDoppel, Senioren%2BEinzel, Senioren%2BDoppel, Jugend%2BEinzel, Jugend%2BDoppel
	var fedRank = "21"               // LK Rank: 1-23

	payload := strings.NewReader("queryName=&queryDateFrom=" + dateFrom + "&queryDateTo=" + dateTo + "&valuationState=" + valuationState + "&queryYoungOld=" + queryYoungOld + "&compType=" + compType + "&fedRank=" + fedRank + "&federation=" + federation.Id)

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
		var tournament models.Tournament
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
					var extractedOrganizer = ""
					if len(array) > 1 {
						extractedOrganizer = array[1]
					} else {
						fmt.Printf("Tournament organizer missing: %s ; Date: %s\n", util.RemoveFormatFromString(columnTournament.Find("a").Text()), tournament.Date)
					}
					organizer := util.RemoveFormatFromString(extractedOrganizer)
					// fmt.Printf("Extracted Organizer: %s\n", organizer)
					tournament.Organizer = organizer

					geoCoords := openstreetmap.GetGeocoordinatesFromCache(state, tournament)
					if geoCoords.Lat == "" || geoCoords.Lon == "" {
						fmt.Printf("No Geocoordinates could be found for (%s): '%s'. Falling back to default in '%s'.\n", tournament.Id, tournament.Organizer, state)
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
