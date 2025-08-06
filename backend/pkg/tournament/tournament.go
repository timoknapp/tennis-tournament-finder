package tournament

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/timoknapp/tennis-tournament-finder/pkg/federation"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
	"github.com/timoknapp/tennis-tournament-finder/pkg/openstreetmap"
	"github.com/timoknapp/tennis-tournament-finder/pkg/util"
)

// Debug flag to control debug output - set to true to enable debug logs
const debugEnabled = false

func GetTournaments(w http.ResponseWriter, r *http.Request) {
	federations := federation.GetFederations()

	util.EnableCors(&w)
	Tournaments := []models.Tournament{}

	// Print cache statistics
	cacheStats := openstreetmap.GetCacheStatistics()
	logger.Info("Cache stats - Total: %d, Successful: %d, Failed: %d, Pending retry: %d, Permanently failed: %d",
		cacheStats["total_entries"], cacheStats["successful"], cacheStats["failed"],
		cacheStats["pending_retry"], cacheStats["permanently_failed"])

	// Clean up old failed entries periodically (when cache has many entries)
	if cacheStats["total_entries"] > 1000 && cacheStats["permanently_failed"] > 100 {
		openstreetmap.CleanupOldFailedEntries()
	}

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
	compType := r.URL.Query().Get("compType")
	selectedFederations := r.URL.Query().Get("federations")

	logger.Info("Get Tournaments from: %s to: %s, compType: %s, federations: %s", dateFrom, dateTo, compType, selectedFederations)

	// Filter federations based on selection
	var filteredFederations []models.Federation
	if selectedFederations != "" {
		selectedFedIds := strings.Split(selectedFederations, ",")
		for _, federation := range federations {
			for _, selectedId := range selectedFedIds {
				if federation.Id == strings.TrimSpace(selectedId) {
					filteredFederations = append(filteredFederations, federation)
					break
				}
			}
		}
	} else {
		// If no specific federations selected, use all
		filteredFederations = federations
	}

	var wg sync.WaitGroup
	for i := 0; i < len(filteredFederations); i++ {
		wg.Add(1)
		go func(fed models.Federation) {
			defer wg.Done()
			if fed.ApiVersion == "old" {
				tournaments := getTournamentsFromFederationOldApi(fed, dateFrom, dateTo, compType)
				Tournaments = append(Tournaments, tournaments...)
			} else if fed.ApiVersion == "new" {
				tournaments := getTournamentsFromFederationNewApi(fed, dateFrom, dateTo, compType)
				Tournaments = append(Tournaments, tournaments...)
			}
		}(filteredFederations[i])
	}
	wg.Wait()

	json.NewEncoder(w).Encode(Tournaments)
}

func getTournamentsFromFederationNewApi(federation models.Federation, dateFrom string, dateTo string, compType string) []models.Tournament {
	logger.Info("Get Tournaments in: %s from: %s to: %s, compType: %s", federation.Id, dateFrom, dateTo, compType)
	var tournaments []models.Tournament

	var baseURL = federation.Url
	var trustedProperties = federation.TrustedProperties
	var defaultGeocoords = federation.Geocoordinates
	var state = federation.State

	var fedRankValuation = "true"
	// var fedRank = "20"
	var firstResult = "0"
	var maxResults = "100"
	var ageCategory = "" // ""= Alle, "general" = Aktive, "juniors" = Jugend, "seniors" = Senioren
	if compType != "" {
		// Switch case
		switch compType {
		case "Herren+Einzel", "Herren+Doppel", "Damen+Einzel", "Damen+Doppel":
			ageCategory = "general"
		case "Senioren+Einzel", "Senioren+Doppel":
			ageCategory = "seniors"
		case "Jugend+Einzel", "Jugend+Doppel":
			ageCategory = "juniors"
		default:
			logger.Warn("Unknown competition type: %s. Using default age category", compType)
			ageCategory = ""
		}
	} else {
		ageCategory = "" // No filter for all competitions
	}

	// Parse the base URL
	reqURL, err := url.Parse(baseURL)
	if err != nil {
		logger.Error("Failed to parse URL: %v", err)
		return tournaments
	}

	// Add query parameters using proper URL encoding
	q := reqURL.Query()

	// Use different parameter prefixes based on federation
	var paramPrefix string
	if federation.Id == "RLP" {
		paramPrefix = "tx_nuportalrs_nuportalrs"
	} else {
		paramPrefix = "tx_nuportalrs_tournaments"
	}

	q.Set(fmt.Sprintf("%s[__trustedProperties]", paramPrefix), trustedProperties)
	q.Set(fmt.Sprintf("%s[tournamentsFilter][ageCategory]", paramPrefix), ageCategory)
	q.Set(fmt.Sprintf("%s[tournamentsFilter][fedRankValuation]", paramPrefix), fedRankValuation)
	q.Set(fmt.Sprintf("%s[tournamentsFilter][startDate]", paramPrefix), dateFrom)
	q.Set(fmt.Sprintf("%s[tournamentsFilter][endDate]", paramPrefix), dateTo)
	q.Set(fmt.Sprintf("%s[tournamentsFilter][firstResult]", paramPrefix), firstResult)
	q.Set(fmt.Sprintf("%s[tournamentsFilter][maxResults]", paramPrefix), maxResults)
	reqURL.RawQuery = q.Encode()

	// Create HTTP client and request
	client := &http.Client{}
	req, err := http.NewRequest("GET", reqURL.String(), nil)
	if err != nil {
		logger.Error("Failed to create request: %v", err)
		return tournaments
	}

	res, err := client.Do(req)
	if err != nil {
		logger.Error("HTTP request failed: %v", err)
		return tournaments
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		logger.Error("Failed to parse HTML document: %v", err)
	}

	// Track tournaments by ID to group competition entries from multiple rows
	tournamentMap := make(map[string]*models.Tournament)
	var orderedTournaments []models.Tournament

	// Find the tournament items
	doc.Find(".responsive-individual tbody tr").Each(func(idxRow int, rowTournament *goquery.Selection) {
		var currentTournament *models.Tournament
		var tournamentDate string

		// Check if this row has any actual content
		if strings.TrimSpace(rowTournament.Text()) == "" {
			return // Skip empty rows
		}

		rowTournament.Find("td").Each(func(idxColumn int, columnTournament *goquery.Selection) {
			// Column 0: Date, Column 1: Title; Column 2: Competition
			value := util.RemoveFormatFromString(columnTournament.Text())

			if idxColumn == 0 && columnTournament.HasClass("daterange") && strings.TrimSpace(value) != "" {
				tournamentDate = strings.TrimSpace(value)
			}

			if idxColumn == 1 && strings.Contains(columnTournament.Text(), "Veranstalter") {
				// This row contains complete tournament information
				var tournament models.Tournament
				tournament.Date = tournamentDate
				tournament.Entries = []models.CompetitionEntry{}

				// Handle both h2 (WTB) and h3 (RLP) title elements
				var urlElement *goquery.Selection
				if h2Element := columnTournament.Find("h2 a").First(); h2Element.Length() > 0 {
					urlElement = h2Element
				} else if h3Element := columnTournament.Find("h3 a").First(); h3Element.Length() > 0 {
					urlElement = h3Element
				} else {
					// Fallback: try to find any anchor tag
					urlElement = columnTournament.Find("a").First()
				}

				if urlElement.Length() > 0 {
					tournament.Title = strings.TrimSpace(util.RemoveFormatFromString(urlElement.Text()))
					tournament.URL, _ = urlElement.Attr("href")
					tournament.Id = getTournamentIdByUrl(tournament.URL)
				}

				// Extract organizer and location from the paragraph element
				paragraphElement := columnTournament.Find("p").First()
				if paragraphElement.Length() > 0 {
					paragraphText := paragraphElement.Text()

					// Direct text parsing approach - more reliable than HTML splitting
					if strings.Contains(paragraphText, "Veranstalter:") {
						// Extract organizer - everything between "Veranstalter: " and " Austragungsort"
						extractedOrganizer, organizerExists := util.GetStringInBetweenTwoString(paragraphText, "Veranstalter: ", " Austragungsort")
						if organizerExists {
							tournament.Organizer = strings.TrimSpace(extractedOrganizer)
						}
					}

					if strings.Contains(paragraphText, "Austragungsort:") {
						// Try different ending markers for different federations
						var extractedLocation string
						var locationExists bool

						// Try "Meldeschluss" first (WTB format)
						extractedLocation, locationExists = util.GetStringInBetweenTwoString(paragraphText, "Austragungsort: ", " Meldeschluss")
						if !locationExists {
							// Try "Offen für" (RLP format)
							extractedLocation, locationExists = util.GetStringInBetweenTwoString(paragraphText, "Austragungsort: ", " Offen für")
						}
						if locationExists {
							tournament.Location = strings.TrimSpace(extractedLocation)
						}
					}

					if debugEnabled {
						logger.Debug("Paragraph text: '%s'", paragraphText)
						logger.Debug("Extracted organizer: '%s'", tournament.Organizer)
						logger.Debug("Extracted location: '%s'", tournament.Location)
					}
				}

				// Get geocoordinates if we have a location
				if tournament.Location != "" {
					geoCoords := openstreetmap.GetGeocoordinatesFromCache(state, tournament)
					if geoCoords.Lat == "" || geoCoords.Lon == "" {
						logger.Warn("No Geocoordinates could be found for (%s): '%s'. Falling back to default in '%s'", tournament.Id, tournament.Location, state)
						geoCoords = defaultGeocoords
					}
					tournament.Lat = geoCoords.Lat
					tournament.Lon = geoCoords.Lon
				} else {
					logger.Warn("Tournament location missing: %s ; Date: %s", util.RemoveFormatFromString(tournament.Title), tournament.Date)
				}

				if len(tournament.Title) > 0 && tournament.Id != "" {
					if debugEnabled {
						logger.Debug("Created tournament: ID='%s', Title='%s', Date='%s'", tournament.Id, tournament.Title, tournament.Date)
					}
					// Store in map for grouping and in ordered slice for maintaining order
					tournamentMap[tournament.Id] = &tournament
					orderedTournaments = append(orderedTournaments, tournament)
					// Update the reference to point to the tournament in the ordered slice
					currentTournament = &orderedTournaments[len(orderedTournaments)-1]
				}
			} else if idxColumn == 1 && len(tournamentMap) > 0 {
				// This might be a continuation row - try to find tournament by URL
				urlElement := columnTournament.Find("a").First()
				if urlElement.Length() > 0 {
					tournamentURL, exists := urlElement.Attr("href")
					if exists {
						tournamentId := getTournamentIdByUrl(tournamentURL)
						if tournament, found := tournamentMap[tournamentId]; found {
							currentTournament = tournament
						}
					}
				}
				// If no URL found, use the most recent tournament
				if currentTournament == nil && len(orderedTournaments) > 0 {
					currentTournament = &orderedTournaments[len(orderedTournaments)-1]
				}
			}

			// Look for competition data in the competitionAbbr column
			if idxColumn == 2 && columnTournament.HasClass("competitionAbbr") {
				// Extract competition information from nested table
				columnTournament.Find("table tbody tr").Each(func(competitionIdx int, competitionRow *goquery.Selection) {
					var competition models.CompetitionEntry

					if debugEnabled {
						rowText := strings.TrimSpace(competitionRow.Text())
						logger.Debug("Processing competition row %d: '%s'", competitionIdx, rowText)
					}

					competitionRow.Find("td").Each(func(colIdx int, competitionCell *goquery.Selection) {
						cellValue := strings.TrimSpace(util.RemoveFormatFromString(competitionCell.Text()))

						if debugEnabled {
							cellClasses, _ := competitionCell.Attr("class")
							logger.Debug("Cell %d: value='%s', classes='%s'", colIdx, cellValue, cellClasses)
						}

						switch colIdx {
						case 0: // Competition name (td.name or first column)
							if competitionCell.HasClass("name") {
								// Extract text from span inside td.name
								spanText := competitionCell.Find("span").Text()
								if spanText != "" {
									competition.Competition = strings.TrimSpace(util.RemoveFormatFromString(spanText))
								} else {
									competition.Competition = cellValue
								}
							} else if colIdx == 0 && cellValue != "" {
								// Fallback: if first column has content but no "name" class
								competition.Competition = cellValue
							}
						case 1: // Skill level (td.fedRank or second column)
							// Check if this cell has fedRank class OR if it's the second column with content
							if competitionCell.HasClass("fedRank") || colIdx == 1 {
								if cellValue != "" {
									competition.SkillLevel = cellValue
								}
							}
						case 2: // Result (td.result) - can be ignored
							// Skip result column
						}
					})

					// Add the competition entry if we have valid data
					if competition.Competition != "" {
						// Clean up skill level - remove extra whitespace
						competition.SkillLevel = strings.TrimSpace(competition.SkillLevel)

						if debugEnabled {
							logger.Debug("Found competition: '%s' with skill level: '%s'", competition.Competition, competition.SkillLevel)
						}

						// Add to current tournament if we have one, otherwise try to find the most recent tournament
						if currentTournament != nil {
							currentTournament.Entries = append(currentTournament.Entries, competition)
							// Also update the tournament in the map
							if currentTournament.Id != "" {
								tournamentMap[currentTournament.Id] = currentTournament
							}
							if debugEnabled {
								logger.Debug("Added competition to current tournament ID='%s', now has %d entries", currentTournament.Id, len(currentTournament.Entries))
							}
						} else if len(orderedTournaments) > 0 {
							// Add to the most recent tournament
							lastTournament := &orderedTournaments[len(orderedTournaments)-1]
							lastTournament.Entries = append(lastTournament.Entries, competition)
							// Update in map as well
							if lastTournament.Id != "" {
								tournamentMap[lastTournament.Id] = lastTournament
							}
							if debugEnabled {
								logger.Debug("Added competition to last tournament ID='%s', now has %d entries", lastTournament.Id, len(lastTournament.Entries))
							}
						}
					}
				})
			}
		})

		// No need to add competition entry here anymore since we handle them directly in the competitionAbbr column
	})

	// Convert back to slice, ensuring we have the updated tournaments with all competition entries
	tournaments = []models.Tournament{}
	for _, tournament := range orderedTournaments {
		if updatedTournament, exists := tournamentMap[tournament.Id]; exists {
			tournaments = append(tournaments, *updatedTournament)
		} else {
			tournaments = append(tournaments, tournament)
		}
	}

	logger.Info("Federation %s: Found %d tournaments total", federation.Id, len(tournaments))

	if debugEnabled {
		for i, t := range tournaments {
			logger.Debug("Tournament %d: ID='%s', Title='%s', Entries=%d", i, t.Id, t.Title, len(t.Entries))
		}
	}

	return tournaments
}

func getTournamentsFromFederationOldApi(federation models.Federation, dateFrom string, dateTo string, compType string) []models.Tournament {
	logger.Info("Get Tournaments in: %s from: %s to: %s, compType: %s", federation.Id, dateFrom, dateTo, compType)
	var tournaments []models.Tournament

	var url = federation.Url
	var defaultGeocoords = federation.Geocoordinates
	var state = federation.State

	var valuationState = "1" // 0=No-LK-Status, 1=LK-Status, 2=DTB-Status
	// var queryYoungOld = "2"  			// 1=Youth, 2=Adult, 3=Senior
	// Tournament Type: Herren%2BEinzel, Herren%2BDoppel, Damen%2BEinzel, Damen%2BDoppel, Senioren%2BEinzel, Senioren%2BDoppel, Jugend%2BEinzel, Jugend%2BDoppel
	// var fedRank = "16"               	// LK Rank: 1-23
	var region = "DE" // Region: DE

	// Build payload with optional competition type filter
	payloadStr := "queryName=&queryDateFrom=" + dateFrom + "&queryDateTo=" + dateTo + "&valuationState=" + valuationState + "&federation=" + federation.Id + "&region=" + region
	if compType != "" {
		// URL encode the competition type (e.g., "Herren+Einzel" -> "Herren%2BEinzel")
		encodedCompType := strings.ReplaceAll(compType, "+", "%2B")
		payloadStr += "&compType=" + encodedCompType
	}
	payload := strings.NewReader(payloadStr)

	client := &http.Client{}
	req, err := http.NewRequest("POST", url, payload)

	if err != nil {
		logger.Error("Failed to create HTTP request: %v", err)
		return tournaments
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	res, err := client.Do(req)
	if err != nil {
		logger.Error("HTTP request failed: %v", err)
		return tournaments
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		logger.Error("Failed to parse HTML document: %v", err)
	}

	// Find the tournament items
	doc.Find(".result-set tr").Each(func(idxRow int, rowTournament *goquery.Selection) {
		// Skip header row
		if idxRow == 0 {
			return
		}

		// Check if this row starts a new tournament (has rowspan on first two columns)
		dateCell := rowTournament.Find("td").First()
		titleCell := rowTournament.Find("td").Eq(1)

		if dateCell.AttrOr("rowspan", "") != "" && titleCell.AttrOr("rowspan", "") != "" {
			// This is a new tournament
			var tournament models.Tournament

			// Initialize slice for competition entries
			tournament.Entries = []models.CompetitionEntry{}

			var currentEntry models.CompetitionEntry

			rowTournament.Find("td").Each(func(idxColumn int, columnTournament *goquery.Selection) {
				value := util.RemoveFormatFromString(columnTournament.Text())

				switch idxColumn {
				case 0: // Date
					tournament.Date = value
				case 1: // Title and Organizer
					urlElement := columnTournament.Find("a")
					tournament.Title = util.RemoveFormatFromString(urlElement.Text())
					tournament.URL, _ = urlElement.Attr("href")
					tournament.Id = getTournamentIdByUrl(tournament.URL)

					if len(tournament.Title) > 0 {
						array := strings.Split(columnTournament.Text(), "\n\t\n\n\n")
						var extractedOrganizer = ""
						if len(array) > 1 {
							extractedOrganizer = array[1]
						} else {
							logger.Warn("Tournament organizer missing: %s ; Date: %s", util.RemoveFormatFromString(columnTournament.Find("a").Text()), tournament.Date)
						}
						organizer := util.RemoveFormatFromString(extractedOrganizer)
						tournament.Organizer = organizer

						geoCoords := openstreetmap.GetGeocoordinatesFromCache(state, tournament)
						if geoCoords.Lat == "" || geoCoords.Lon == "" {
							logger.Warn("No Geocoordinates could be found for (%s): '%s'. Falling back to default in '%s'", tournament.Id, tournament.Organizer, state)
							geoCoords = defaultGeocoords
						}
						tournament.Lat = geoCoords.Lat
						tournament.Lon = geoCoords.Lon
					}
				case 2: // Competition (Konkurrenz)
					currentEntry.Competition = strings.TrimSpace(value)
				case 3: // Skill Level (LK)
					currentEntry.SkillLevel = strings.TrimSpace(value)
					if currentEntry.SkillLevel == "&nbsp;" {
						currentEntry.SkillLevel = ""
					}
				}
			})

			// Add the first entry if it has content
			if currentEntry.Competition != "" || currentEntry.SkillLevel != "" {
				tournament.Entries = append(tournament.Entries, currentEntry)
			}

			// Store the tournament
			if len(tournament.Title) > 0 {
				tournaments = append(tournaments, tournament)
			}
		} else {
			// This row belongs to the previous tournament (additional competition/skill level)
			if len(tournaments) > 0 {
				lastTournament := &tournaments[len(tournaments)-1]

				var additionalEntry models.CompetitionEntry

				rowTournament.Find("td").Each(func(idxColumn int, columnTournament *goquery.Selection) {
					value := util.RemoveFormatFromString(columnTournament.Text())

					switch idxColumn {
					case 0: // Competition (Konkurrenz) - first column in continuation rows
						additionalEntry.Competition = strings.TrimSpace(value)
					case 1: // Skill Level (LK) - second column in continuation rows
						additionalEntry.SkillLevel = strings.TrimSpace(value)
						if additionalEntry.SkillLevel == "&nbsp;" {
							additionalEntry.SkillLevel = ""
						}
					}
				})

				// Add the additional entry if it has content
				if additionalEntry.Competition != "" || additionalEntry.SkillLevel != "" {
					lastTournament.Entries = append(lastTournament.Entries, additionalEntry)
				}
			}
		}
	})

	logger.Info("Federation %s: Found %d tournaments total", federation.Id, len(tournaments))

	return tournaments
}

func getTournamentIdByUrl(tournamentUrl string) string {
	// Old Mybigpoint: https://mybigpoint.tennis.de/web/guest/turniersuche?tournamentId=484582
	// New Tennis.de: https://www.tennis.de/spielen/turniersuche.html#detail/699982
	array := strings.Split(tournamentUrl, "detail/")
	if len(array) > 1 {
		return array[1]
	} else {
		return ""
	}
}
