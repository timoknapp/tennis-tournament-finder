package tournament

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
	"github.com/timoknapp/tennis-tournament-finder/pkg/openstreetmap"
	"github.com/timoknapp/tennis-tournament-finder/pkg/util"
)

func GetTournaments(w http.ResponseWriter, r *http.Request) {
	util.EnableCors(&w)
	Tournaments := []models.Tournament{}

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

	tournamentsHTV := getTournamentsFromFederationOldApi("HTV", dateFrom, dateTo)
	tournamentsBAD := getTournamentsFromFederationOldApi("BAD", dateFrom, dateTo)
	tournamentsRLP := getTournamentsFromFederationNewApi("RLP", dateFrom, dateTo)
	tournamentsWTB := getTournamentsFromFederationNewApi("WTB", dateFrom, dateTo)

	Tournaments = util.ConcatMultipleSlices([][]models.Tournament{tournamentsBAD, tournamentsHTV, tournamentsRLP, tournamentsWTB})

	json.NewEncoder(w).Encode(Tournaments)
}

func getTournamentsFromFederationNewApi(federation string, dateFrom string, dateTo string) []models.Tournament {
	fmt.Printf("Get Tournaments in: %s from: %s to: %s\n", federation, dateFrom, dateTo)
	var tournaments []models.Tournament

	var url = ""
	var trustedProperties = ""
	var defaultGeocoords models.Geocoordinates
	var state = ""
	const urlRLP string = "https://www.rlp-tennis.de/spielbetrieb/turniere/appTournament.html"
	const urlWTB string = "https://www.wtb-tennis.de/turniere/turnierkalender/app/nuTournaments.html"
	const trustedPropertiesRLP string = "{\"tournamentsFilter\":{\"ageCategory\":1,\"ageGroupJuniors\":1,\"ageGroupSeniors\":1,\"circuit\":1,\"region\":1,\"fedRankValuation\":1,\"nationalValuation\":1,\"fedRank\":1,\"name\":1,\"city\":1,\"startDate\":1,\"endDate\":1,\"firstResult\":1,\"maxResults\":1}}8732571a008a8bee386504005773291f579958de"
	const trustedPropertiesWTB string = "a:1:{s:17:\"tournamentsFilter\";a:15:{s:11:\"ageCategory\";i:1;s:15:\"ageGroupJuniors\";i:1;s:15:\"ageGroupSeniors\";i:1;s:7:\"circuit\";i:1;s:16:\"fedRankValuation\";i:1;s:17:\"nationalValuation\";i:1;s:4:\"type\";i:1;s:7:\"fedRank\";i:1;s:6:\"region\";i:1;s:4:\"name\";i:1;s:4:\"city\";i:1;s:9:\"startDate\";i:1;s:7:\"endDate\";i:1;s:11:\"firstResult\";i:1;s:10:\"maxResults\";i:1;}}0084e646e91ed3b7e155957c5d3b286f2602eebc"
	var geoCoordsRLP = models.Geocoordinates{Lat: "49.8335079", Lon: "8.0138431"}
	var geoCoordsWTB = models.Geocoordinates{Lat: "48.853488", Lon: "9.1373019"}
	var stateRLP = "Rheinland-Pfalz"
	var stateWTB = "Württemberg"

	switch federation {
	case "RLP":
		url = urlRLP
		trustedProperties = trustedPropertiesRLP
		defaultGeocoords = geoCoordsRLP
		state = stateRLP
	case "WTB":
		url = urlWTB
		trustedProperties = trustedPropertiesWTB
		defaultGeocoords = geoCoordsWTB
		state = stateWTB
	default:
		federation = "RLP"
		url = urlRLP
		trustedProperties = trustedPropertiesRLP
		defaultGeocoords = geoCoordsRLP
		state = stateRLP
	}

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

func getTournamentsFromFederationOldApi(federation string, dateFrom string, dateTo string) []models.Tournament {
	fmt.Printf("Get Tournaments in: %s from: %s to: %s\n", federation, dateFrom, dateTo)
	var tournaments []models.Tournament

	var url = ""
	var defaultGeocoords models.Geocoordinates
	var state = ""
	const urlBAD string = "https://baden.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar"
	const urlHTV string = "https://htv.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar"
	var geoCoordsBAD = models.Geocoordinates{Lat: "49.34003", Lon: "8.68514"}
	var geoCoordsHTV = models.Geocoordinates{Lat: "50.0770372", Lon: "8.7553832"}
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
