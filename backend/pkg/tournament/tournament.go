package tournament

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/timoknapp/tennis-tournament-finder/pkg/federation"
	"github.com/timoknapp/tennis-tournament-finder/pkg/httpclient"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
	"github.com/timoknapp/tennis-tournament-finder/pkg/openstreetmap"
	"github.com/timoknapp/tennis-tournament-finder/pkg/util"
)

// Debug flag to control debug output - set to true to enable debug logs
const debugEnabled = false

const (
	// pageSize is the number of results requested per page from the new API.
	pageSize = 100
	// maxPages bounds pagination so a malformed or endlessly repeating
	// upstream response can never loop forever.
	maxPages = 20
	// maxResponseBytes bounds how much HTML is read from an upstream response.
	maxResponseBytes = 16 << 20 // 16 MiB
)

// geocoder resolves tournament coordinates. The indirection lets tests supply a
// deterministic implementation instead of performing network lookups.
type geocoder func(fed models.Federation, tournament models.Tournament) models.Geocoordinates

// defaultGeocoder delegates to the OpenStreetMap cache/lookup.
func defaultGeocoder(fed models.Federation, tournament models.Tournament) models.Geocoordinates {
	return openstreetmap.GetGeocoordinatesForFederation(fed, tournament)
}

// FederationResult carries a single federation's outcome.
type FederationResult struct {
	Federation  models.Federation
	Tournaments []models.Tournament
	Err         error
}

func GetTournaments(w http.ResponseWriter, r *http.Request) {
	federations := federation.GetFederations()

	util.EnableCors(&w)

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

	filteredFederations := FilterFederations(federations, selectedFederations)

	tournaments, _ := CollectTournaments(r.Context(), filteredFederations, dateFrom, dateTo, compType)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(tournaments); err != nil {
		logger.Error("Failed to encode tournaments response: %v", err)
	}
}

// FilterFederations returns the federations selected by a comma-separated list
// of IDs. An empty selection means "all federations".
func FilterFederations(federations []models.Federation, selectedFederations string) []models.Federation {
	if strings.TrimSpace(selectedFederations) == "" {
		return federations
	}

	selected := make(map[string]struct{})
	for _, id := range strings.Split(selectedFederations, ",") {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			selected[trimmed] = struct{}{}
		}
	}

	var filtered []models.Federation
	for _, fed := range federations {
		if _, ok := selected[fed.Id]; ok {
			filtered = append(filtered, fed)
		}
	}

	return filtered
}

// CollectTournaments fetches all federations concurrently and merges the
// results deterministically.
//
// Every goroutine writes only to its own result slot, so no shared slice is
// mutated concurrently. Results are ordered by the federation's input position,
// which keeps responses stable regardless of goroutine scheduling. A failure in
// one federation never discards results from the others.
func CollectTournaments(ctx context.Context, federations []models.Federation, dateFrom, dateTo, compType string) ([]models.Tournament, []FederationResult) {
	results := make([]FederationResult, len(federations))

	var wg sync.WaitGroup
	for i, fed := range federations {
		wg.Add(1)
		go func(idx int, fed models.Federation) {
			defer wg.Done()

			results[idx].Federation = fed

			defer func() {
				// A panic in one federation parser must not take down the
				// whole request.
				if rec := recover(); rec != nil {
					logger.Error("Recovered from panic while processing federation %s: %v", fed.Id, rec)
					results[idx].Err = fmt.Errorf("panic while processing federation %s: %v", fed.Id, rec)
				}
			}()

			tournaments, err := fetchFederation(ctx, fed, dateFrom, dateTo, compType)
			if err != nil {
				logger.Error("Federation %s failed: %v", fed.Id, err)
			}

			results[idx].Tournaments = tournaments
			results[idx].Err = err
		}(i, fed)
	}
	wg.Wait()

	tournaments := []models.Tournament{}
	for _, res := range results {
		tournaments = append(tournaments, res.Tournaments...)
	}

	return tournaments, results
}

// fetchFederation dispatches to the correct API implementation.
func fetchFederation(ctx context.Context, fed models.Federation, dateFrom, dateTo, compType string) ([]models.Tournament, error) {
	switch fed.ApiVersion {
	case "old":
		return getTournamentsFromFederationOldApi(ctx, fed, dateFrom, dateTo, compType)
	case "new":
		return getTournamentsFromFederationNewApi(ctx, fed, dateFrom, dateTo, compType)
	default:
		return nil, fmt.Errorf("unknown API version %q for federation %s", fed.ApiVersion, fed.Id)
	}
}

// ageCategoryForCompType maps a competition type to the new API's age category.
func ageCategoryForCompType(compType string) string {
	switch compType {
	case "":
		// No filter for all competitions
		return ""
	case "Herren+Einzel", "Herren+Doppel", "Damen+Einzel", "Damen+Doppel":
		return "general"
	case "Senioren+Einzel", "Senioren+Doppel":
		return "seniors"
	case "Jugend+Einzel", "Jugend+Doppel":
		return "juniors"
	default:
		logger.Warn("Unknown competition type: %s. Using default age category", compType)
		return ""
	}
}

// buildNewApiURL assembles a request URL for one page of the new API.
func buildNewApiURL(fed models.Federation, dateFrom, dateTo, compType string, firstResult int) (string, error) {
	reqURL, err := url.Parse(fed.Url)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %w", err)
	}

	// Use different parameter prefixes based on federation
	paramPrefix := "tx_nuportalrs_tournaments"
	if fed.Id == "RLP" {
		paramPrefix = "tx_nuportalrs_nuportalrs"
	}

	q := reqURL.Query()
	q.Set(fmt.Sprintf("%s[__trustedProperties]", paramPrefix), fed.TrustedProperties)
	q.Set(fmt.Sprintf("%s[tournamentsFilter][ageCategory]", paramPrefix), ageCategoryForCompType(compType))
	q.Set(fmt.Sprintf("%s[tournamentsFilter][fedRankValuation]", paramPrefix), "true")
	q.Set(fmt.Sprintf("%s[tournamentsFilter][startDate]", paramPrefix), dateFrom)
	q.Set(fmt.Sprintf("%s[tournamentsFilter][endDate]", paramPrefix), dateTo)
	q.Set(fmt.Sprintf("%s[tournamentsFilter][firstResult]", paramPrefix), strconv.Itoa(firstResult))
	q.Set(fmt.Sprintf("%s[tournamentsFilter][maxResults]", paramPrefix), strconv.Itoa(pageSize))
	reqURL.RawQuery = q.Encode()

	return reqURL.String(), nil
}

// getTournamentsFromFederationNewApi fetches every result page for a federation
// using the new (TYPO3/nuPortal) API.
//
// The upstream API caps a single response at maxResults entries, so pagination
// is required to avoid silently truncating larger result sets.
func getTournamentsFromFederationNewApi(ctx context.Context, fed models.Federation, dateFrom string, dateTo string, compType string) ([]models.Tournament, error) {
	logger.Info("Get Tournaments in: %s from: %s to: %s, compType: %s", fed.Id, dateFrom, dateTo, compType)

	var all []models.Tournament
	seen := make(map[string]struct{})
	var firstErr error

	for page := 0; page < maxPages; page++ {
		reqURL, err := buildNewApiURL(fed, dateFrom, dateTo, compType, page*pageSize)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}

		body, err := doRequest(ctx, http.MethodGet, reqURL, nil, "")
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			// Keep the pages fetched so far instead of discarding everything.
			break
		}

		pageTournaments, parseErr := ParseNewApiDocument(body, fed, defaultGeocoder)
		body.Close()

		if parseErr != nil {
			if firstErr == nil {
				firstErr = parseErr
			}
			break
		}

		newOnPage := 0
		for _, t := range pageTournaments {
			key := t.Id
			if key == "" {
				// Fall back to a composite key when no ID could be parsed.
				key = t.Title + "|" + t.Date
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			all = append(all, t)
			newOnPage++
		}

		logger.Debug("Federation %s page %d: %d parsed, %d new", fed.Id, page, len(pageTournaments), newOnPage)

		// Stop on a short page (the last one) or when a page adds nothing new.
		// The latter guards against upstreams that ignore the offset parameter.
		if len(pageTournaments) < pageSize || newOnPage == 0 {
			break
		}
	}

	logger.Info("Federation %s: Found %d tournaments total", fed.Id, len(all))

	return all, firstErr
}

// getTournamentsFromFederationOldApi fetches tournaments using the old
// (liga.nu) form-post API.
func getTournamentsFromFederationOldApi(ctx context.Context, fed models.Federation, dateFrom string, dateTo string, compType string) ([]models.Tournament, error) {
	logger.Info("Get Tournaments in: %s from: %s to: %s, compType: %s", fed.Id, dateFrom, dateTo, compType)

	body, err := doRequest(ctx, http.MethodPost, fed.Url,
		strings.NewReader(buildOldApiPayload(fed, dateFrom, dateTo, compType)),
		"application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	defer body.Close()

	tournaments, err := ParseOldApiDocument(body, fed, defaultGeocoder)
	if err != nil {
		return nil, err
	}

	logger.Info("Federation %s: Found %d tournaments total", fed.Id, len(tournaments))

	return tournaments, nil
}

// buildOldApiPayload builds the form-encoded request body for the old API.
func buildOldApiPayload(fed models.Federation, dateFrom, dateTo, compType string) string {
	const valuationState = "1" // 0=No-LK-Status, 1=LK-Status, 2=DTB-Status
	const region = "DE"

	form := url.Values{}
	form.Set("queryName", "")
	form.Set("queryDateFrom", dateFrom)
	form.Set("queryDateTo", dateTo)
	form.Set("valuationState", valuationState)
	form.Set("federation", fed.Id)
	form.Set("region", region)
	if compType != "" {
		// compType uses "+" to separate the two words (e.g. "Herren+Einzel").
		// url.Values encodes it safely as %2B.
		form.Set("compType", compType)
	}

	return form.Encode()
}

// doRequest performs a bounded HTTP request and returns the response body.
// The caller is responsible for closing the returned ReadCloser.
func doRequest(ctx context.Context, method, reqURL string, body io.Reader, contentType string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpclient.ApplyDefaultHeaders(req)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	res, err := httpclient.Federation().Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxResponseBytes))
		res.Body.Close()
		return nil, fmt.Errorf("unexpected HTTP status %d", res.StatusCode)
	}

	return newLimitedReadCloser(res.Body, maxResponseBytes), nil
}

// limitedReadCloser bounds how much of a response body can be read while still
// closing the underlying connection correctly.
type limitedReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func newLimitedReadCloser(rc io.ReadCloser, limit int64) io.ReadCloser {
	return &limitedReadCloser{reader: io.LimitReader(rc, limit), closer: rc}
}

func (l *limitedReadCloser) Read(p []byte) (int, error) { return l.reader.Read(p) }
func (l *limitedReadCloser) Close() error               { return l.closer.Close() }

// resolveGeocoordinates looks up coordinates and falls back to the federation
// default when nothing suitable is found.
func resolveGeocoordinates(fed models.Federation, tournament models.Tournament, geocode geocoder, subject string) models.Geocoordinates {
	if geocode == nil {
		geocode = defaultGeocoder
	}

	geoCoords := geocode(fed, tournament)
	if geoCoords.Lat == "" || geoCoords.Lon == "" {
		logger.Warn("No Geocoordinates could be found for (%s): '%s'. Falling back to default in '%s'",
			tournament.Id, subject, fed.State)
		return fed.Geocoordinates
	}

	return geoCoords
}

// ParseNewApiDocument parses one page of the new API's HTML into tournaments.
func ParseNewApiDocument(r io.Reader, fed models.Federation, geocode geocoder) ([]models.Tournament, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML document: %w", err)
	}

	// Track tournaments by ID to group competition entries from multiple rows
	tournamentMap := make(map[string]*models.Tournament)
	var orderedTournaments []*models.Tournament

	doc.Find(".responsive-individual tbody tr").Each(func(idxRow int, rowTournament *goquery.Selection) {
		var currentTournament *models.Tournament
		var tournamentDate string

		// Check if this row has any actual content
		if strings.TrimSpace(rowTournament.Text()) == "" {
			return // Skip empty rows
		}

		rowTournament.Find("td").Each(func(idxColumn int, columnTournament *goquery.Selection) {
			// Column 0: Date, Column 1: Title; Column 2: Competition
			value := util.NormalizeWhitespace(columnTournament.Text())

			if idxColumn == 0 && columnTournament.HasClass("daterange") && value != "" {
				tournamentDate = value
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
					tournament.Title = util.NormalizeWhitespace(urlElement.Text())
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
							tournament.Organizer = util.NormalizeWhitespace(extractedOrganizer)
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
							tournament.Location = util.NormalizeWhitespace(extractedLocation)
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
					geoCoords := resolveGeocoordinates(fed, tournament, geocode, tournament.Location)
					tournament.Lat = geoCoords.Lat
					tournament.Lon = geoCoords.Lon
				} else {
					logger.Warn("Tournament location missing: %s ; Date: %s", tournament.Title, tournament.Date)
				}

				if len(tournament.Title) > 0 && tournament.Id != "" {
					if debugEnabled {
						logger.Debug("Created tournament: ID='%s', Title='%s', Date='%s'", tournament.Id, tournament.Title, tournament.Date)
					}

					stored := tournament
					// Store the same pointer in the map and the ordered slice so
					// competition entries appended later are visible in both.
					tournamentMap[stored.Id] = &stored
					orderedTournaments = append(orderedTournaments, &stored)
					currentTournament = &stored
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
					currentTournament = orderedTournaments[len(orderedTournaments)-1]
				}
			}

			// Look for competition data in the competitionAbbr column
			if idxColumn == 2 && columnTournament.HasClass("competitionAbbr") {
				// Extract competition information from nested table
				columnTournament.Find("table tbody tr").Each(func(competitionIdx int, competitionRow *goquery.Selection) {
					var competition models.CompetitionEntry

					competitionRow.Find("td").Each(func(colIdx int, competitionCell *goquery.Selection) {
						cellValue := util.NormalizeWhitespace(competitionCell.Text())

						switch colIdx {
						case 0: // Competition name (td.name or first column)
							if competitionCell.HasClass("name") {
								// Extract text from span inside td.name
								spanText := competitionCell.Find("span").Text()
								if spanText != "" {
									competition.Competition = util.NormalizeWhitespace(spanText)
								} else {
									competition.Competition = cellValue
								}
							} else if cellValue != "" {
								// Fallback: first column has content but no "name" class
								competition.Competition = cellValue
							}
						case 1: // Skill level (td.fedRank or second column)
							if cellValue != "" {
								competition.SkillLevel = cellValue
							}
						case 2: // Result (td.result) - can be ignored
						}
					})

					// Add the competition entry if we have valid data
					if competition.Competition != "" {
						target := currentTournament
						if target == nil && len(orderedTournaments) > 0 {
							target = orderedTournaments[len(orderedTournaments)-1]
						}
						if target != nil {
							target.Entries = append(target.Entries, competition)
						}
					}
				})
			}
		})
	})

	tournaments := make([]models.Tournament, 0, len(orderedTournaments))
	for _, t := range orderedTournaments {
		tournaments = append(tournaments, *t)
	}

	if debugEnabled {
		for i, t := range tournaments {
			logger.Debug("Tournament %d: ID='%s', Title='%s', Entries=%d", i, t.Id, t.Title, len(t.Entries))
		}
	}

	return tournaments, nil
}

// ParseOldApiDocument parses the old API's result table into tournaments.
func ParseOldApiDocument(r io.Reader, fed models.Federation, geocode geocoder) ([]models.Tournament, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML document: %w", err)
	}

	var tournaments []models.Tournament

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
			tournament.Entries = []models.CompetitionEntry{}

			var currentEntry models.CompetitionEntry

			rowTournament.Find("td").Each(func(idxColumn int, columnTournament *goquery.Selection) {
				value := util.NormalizeWhitespace(columnTournament.Text())

				switch idxColumn {
				case 0: // Date
					tournament.Date = value
				case 1: // Title and Organizer
					urlElement := columnTournament.Find("a")
					tournament.Title = util.NormalizeWhitespace(urlElement.Text())
					tournament.URL, _ = urlElement.Attr("href")
					tournament.Id = getTournamentIdByUrl(tournament.URL)

					if len(tournament.Title) > 0 {
						tournament.Organizer = extractOldApiOrganizer(columnTournament, tournament)

						geoCoords := resolveGeocoordinates(fed, tournament, geocode, tournament.Organizer)
						tournament.Lat = geoCoords.Lat
						tournament.Lon = geoCoords.Lon
					}
				case 2: // Competition (Konkurrenz)
					currentEntry.Competition = value
				case 3: // Skill Level (LK)
					currentEntry.SkillLevel = normalizeSkillLevel(value)
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
					value := util.NormalizeWhitespace(columnTournament.Text())

					switch idxColumn {
					case 0: // Competition (Konkurrenz) - first column in continuation rows
						additionalEntry.Competition = value
					case 1: // Skill Level (LK) - second column in continuation rows
						additionalEntry.SkillLevel = normalizeSkillLevel(value)
					}
				})

				// Add the additional entry if it has content
				if additionalEntry.Competition != "" || additionalEntry.SkillLevel != "" {
					lastTournament.Entries = append(lastTournament.Entries, additionalEntry)
				}
			}
		}
	})

	return tournaments, nil
}

// extractOldApiOrganizer pulls the organizer out of the title cell, which
// contains the tournament link followed by the organizing club.
func extractOldApiOrganizer(columnTournament *goquery.Selection, tournament models.Tournament) string {
	cellText := columnTournament.Text()
	linkText := columnTournament.Find("a").Text()

	// The organizer is the cell text with the link text removed.
	remainder := strings.Replace(cellText, linkText, "", 1)
	organizer := util.NormalizeWhitespace(remainder)

	if organizer == "" {
		logger.Warn("Tournament organizer missing: %s ; Date: %s",
			util.NormalizeWhitespace(linkText), tournament.Date)
	}

	return organizer
}

// normalizeSkillLevel cleans up an LK value, dropping non-breaking-space
// placeholders that some federations emit for "no LK".
func normalizeSkillLevel(value string) string {
	cleaned := util.NormalizeWhitespace(value)
	if cleaned == "&nbsp;" {
		return ""
	}
	return cleaned
}

func getTournamentIdByUrl(tournamentUrl string) string {
	// Old Mybigpoint: https://mybigpoint.tennis.de/web/guest/turniersuche?tournamentId=484582
	// New Tennis.de: https://www.tennis.de/spielen/turniersuche.html#detail/699982
	array := strings.Split(tournamentUrl, "detail/")
	if len(array) > 1 {
		return array[1]
	}
	return ""
}
