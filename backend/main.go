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

func getTournaments(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Endpoint Hit: returnAllTournaments")
	enableCors(&w)
	Tournaments := []Tournament{}

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

	tournamentsHTV := getTournamentsFromFederation("HTV", dateFrom, dateTo)
	tournamentsBAD := getTournamentsFromFederation("BAD", dateFrom, dateTo)

	Tournaments = append(tournamentsBAD, tournamentsHTV...)

	json.NewEncoder(w).Encode(Tournaments)
}

func getTournamentsFromFederation(federation string, dateFrom string, dateTo string) []Tournament {
	var tournaments []Tournament

	var url = ""
	var defaultGeocoords Geocoordinates
	const urlBAD string = "https://baden.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar"
	const urlHTV string = "https://htv.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar"
	var geoCoordsBAD = Geocoordinates{Lat: "49.34003", Lon: "8.68514"}
	var geoCoordsHTV = Geocoordinates{Lat: "50.0770372", Lon: "8.7553832"}

	switch federation {
	case "BAD":
		url = urlBAD
		defaultGeocoords = geoCoordsBAD
	case "HTV":
		url = urlHTV
		defaultGeocoords = geoCoordsHTV
	default:
		federation = "BAD"
		url = urlBAD
		defaultGeocoords = geoCoordsBAD
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

	// body, err := ioutil.ReadAll(res.Body)
	// if err != nil {
	// 	fmt.Println(err)
	// 	return
	// }
	// fmt.Println(string(body))

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

				if len(tournament.Title) > 0 {
					array := strings.Split(columnTournament.Text(), "\n\t\n\n\n")
					fmt.Println("Title Array: " + "'" + strings.Join(array, `','`) + `'`)
					var extractedAddress = ""
					if len(array) > 1 {
						extractedAddress = array[1]
					} else {
						fmt.Printf("Tournament address missing: %s ; Date: %s\n", removeFormatFromString(columnTournament.Find("a").Text()), tournament.Date)
					}
					address := removeFormatFromString(extractedAddress)
					// address := removeFormatFromString(columnTournament.Contents().Get(6).Data)
					fmt.Printf("Extracted Address: %s\n", address)
					tournament.Address = address

					geoCoords := getGeocoordinates(tournament.Address)
					if geoCoords.Lat == "" || geoCoords.Lon == "" {
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

func getGeocoordinates(query string) Geocoordinates {
	// https://nominatim.openstreetmap.org/search.php?q=MTV+Karlsruhe&limit=1&format=jsonv2
	const urlOSM string = "https://nominatim.openstreetmap.org/search.php?limit=1&format=jsonv2&q="
	fmt.Printf("Get Geocoordinates for %s\n", query)
	reformattedQuery := strings.ReplaceAll(query, " ", "+")

	res, err := http.Get(urlOSM + reformattedQuery)
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
		result = geoCoords[0]
		fmt.Printf("Lat: %s, Lon: %s, Name: %s\n", geoCoords[0].Lat, geoCoords[0].Lon, geoCoords[0].DisplayName)
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
	http.HandleFunc("/", getTournaments)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
