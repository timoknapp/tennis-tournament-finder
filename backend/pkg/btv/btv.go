// Package btv fetches tournaments from the Bayerischer Tennis-Verband.
//
// Bavaria left the shared nuLiga platform, so btv.de embeds its own tournament
// search: a ZK (zkoss) application hosted at btv-prod.burdadigitalsystems.de.
// It needs no authentication, unlike the tennis.de detail views (see #55).
//
// The protocol is small enough to drive directly:
//
//  1. GET the widget page. It returns a JSESSIONID cookie and bootstraps ZK
//     with a desktop id (`dt`) and an update URI (`uu`).
//  2. POST one `onClientInfo` command to the update URI. ZK replies with the
//     rendered result grid as a JSON-ish payload.
//
// The payload is a ZK widget tree rather than an API response, so it is parsed
// defensively: any field that cannot be read is skipped instead of failing the
// whole federation.
package btv

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/timoknapp/tennis-tournament-finder/pkg/httpclient"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
	"github.com/timoknapp/tennis-tournament-finder/pkg/util"
)

// maxResponseBytes bounds how much of the widget response is read.
const maxResponseBytes = 8 << 20 // 8 MiB

// maxPages bounds pagination so a malformed or endlessly repeating response
// can never loop forever. The widget renders 10 rows per page.
const maxPages = 40

var (
	// bootstrapPattern extracts the ZK desktop id and update URI from the
	// widget page, e.g. zkmx([0,'uuid',{dt:'z_abc',...,uu:'/btvtrnsearch/zkau'...
	bootstrapPattern = regexp.MustCompile(`zkmx\(\s*\[0,'([^']+)',\{dt:'([^']+)'.*?uu:'([^']+)'`)

	// pagingPattern finds the paging widget's uuid and its page count, which
	// are needed to request the remaining pages.
	pagingPattern = regexp.MustCompile(`\['zul\.mesh\.Paging','([^']+)',\{[^}]*pageCount:(\d+)`)

	// zkEscapePattern matches the \xNN escapes ZK uses for non-ASCII output.
	zkEscapePattern = regexp.MustCompile(`\\x([0-9A-Fa-f]{2})`)

	// rowPattern splits the payload into individual result rows.
	rowPattern = regexp.MustCompile(`\['zul\.grid\.Row'`)

	// datePattern matches "10.08. - 16.08.2026 in Bad Füssing".
	datePattern = regexp.MustCompile(`value:'(\d{2}\.\d{2}\.\s*-\s*\d{2}\.\d{2}\.\d{4})\s+in\s+([^']+)'`)

	// linkPattern matches the tennis.de detail link and the tournament title.
	linkPattern = regexp.MustCompile(`href=\\?'(https://[^'\\]+)\\?'[^>]*>([^<]+)<`)

	// labelPattern matches any ZK label value, used for the competition list.
	labelPattern = regexp.MustCompile(`value:'([^']*)'`)

	// totalPattern reports how many tournaments the widget found.
	totalPattern = regexp.MustCompile(`_totalSize:(\d+)`)

	// idPattern extracts the tournament id from a tennis.de detail URL.
	idPattern = regexp.MustCompile(`detail/(\d+)`)
)

// Client talks to the BTV tournament widget.
type Client struct {
	BaseURL string
	// HTTPClient defaults to the shared federation client.
	HTTPClient *http.Client
}

// New returns a client for the given widget base URL.
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return httpclient.Federation()
}

// session holds what one ZK conversation needs.
type session struct {
	desktopID string
	updateURL string
	cookies   []*http.Cookie
	// sequence backs the ZK-SID header. ZK expects a monotonically
	// increasing sequence number per desktop; sending a constant value makes
	// it treat later requests as duplicates and replay the first response,
	// which silently returns page 0 forever.
	sequence int
}

// bootstrap performs the initial GET and extracts the ZK session details.
func (c *Client) bootstrap(ctx context.Context) (*session, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create bootstrap request: %w", err)
	}
	httpclient.ApplyDefaultHeaders(req)
	// The widget is embedded on btv.de and expects that origin.
	req.Header.Set("Referer", "https://www.btv.de/")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("bootstrap request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bootstrap returned status %d", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read bootstrap response: %w", err)
	}

	m := bootstrapPattern.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("no ZK bootstrap found; the widget markup may have changed")
	}

	updateURI := unescapeZK(string(m[3]))
	updateURL, err := resolveURL(c.BaseURL, updateURI)
	if err != nil {
		return nil, err
	}

	return &session{
		desktopID: unescapeZK(string(m[2])),
		updateURL: updateURL,
		cookies:   res.Cookies(),
	}, nil
}

// fetchGrid asks ZK to render the result grid.
func (c *Client) fetchGrid(ctx context.Context, s *session) (string, error) {
	form := url.Values{}
	form.Set("dtid", s.desktopID)
	form.Set("cmd_0", "onClientInfo")
	form.Set("opt_0", "i")
	// Client metrics the widget expects; the values only affect layout.
	form.Set("data_0", `{"":[0,1500,1200,24,1500,1200,0,0,"1.0","landscape"]}`)

	return c.postCommand(ctx, s, form)
}

// fetchPage requests one further page of the result grid.
func (c *Client) fetchPage(ctx context.Context, s *session, pagingUUID string, page int) (string, error) {
	form := url.Values{}
	form.Set("dtid", s.desktopID)
	form.Set("cmd_0", "onPaging")
	form.Set("uuid_0", pagingUUID)
	form.Set("data_0", fmt.Sprintf(`{"":%d}`, page))

	return c.postCommand(ctx, s, form)
}

// postCommand sends one ZK command and returns the decoded response.
func (c *Client) postCommand(ctx context.Context, s *session, form url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.updateURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create update request: %w", err)
	}
	httpclient.ApplyDefaultHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	s.sequence++
	req.Header.Set("ZK-SID", strconv.Itoa(s.sequence))
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", c.BaseURL)
	for _, cookie := range s.cookies {
		req.AddCookie(cookie)
	}

	res, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("update request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update returned status %d", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("failed to read update response: %w", err)
	}

	return unescapeZK(string(body)), nil
}

// GetTournaments fetches the current tournament list.
//
// The widget applies its own default date window, so dateFrom/dateTo are
// accepted for interface symmetry but cannot be pushed upstream; results are
// filtered locally by the caller if needed.
func (c *Client) GetTournaments(ctx context.Context, fed models.Federation) ([]models.Tournament, error) {
	s, err := c.bootstrap(ctx)
	if err != nil {
		return nil, err
	}

	payload, err := c.fetchGrid(ctx, s)
	if err != nil {
		return nil, err
	}

	tournaments := ParseGrid(payload, fed)
	seen := make(map[string]struct{}, len(tournaments))
	for _, t := range tournaments {
		seen[dedupKey(t)] = struct{}{}
	}

	// The grid renders 10 rows per page, so the remaining pages have to be
	// requested explicitly or most of Bavaria's tournaments would be missing.
	//
	// ZK re-renders the paging widget with a fresh uuid in every response, so
	// it must be re-read each time; reusing the first one silently returns
	// page 0 again.
	pagingUUID, pageCount := parsePaging(payload)
	if pagingUUID != "" && pageCount > 1 {
		if pageCount > maxPages {
			logger.Warn("BTV reports %d pages, capping at %d", pageCount, maxPages)
			pageCount = maxPages
		}

		for page := 1; page < pageCount; page++ {
			pagePayload, pageErr := c.fetchPage(ctx, s, pagingUUID, page)
			if pageErr != nil {
				// Keep what was fetched so far rather than failing outright.
				logger.Warn("BTV page %d failed: %v", page, pageErr)
				break
			}

			newOnPage := 0
			for _, t := range ParseGrid(pagePayload, fed) {
				key := dedupKey(t)
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				tournaments = append(tournaments, t)
				newOnPage++
			}

			// Defensive: a page that adds nothing means the widget stopped
			// honouring the offset.
			if newOnPage == 0 {
				break
			}

			// Pick up the regenerated paging uuid for the next request.
			if nextUUID, _ := parsePaging(pagePayload); nextUUID != "" {
				pagingUUID = nextUUID
			}
		}
	}

	if total := totalPattern.FindStringSubmatch(payload); total != nil {
		if n, convErr := strconv.Atoi(total[1]); convErr == nil && n != len(tournaments) {
			logger.Warn("BTV reports %d tournaments, parsed %d", n, len(tournaments))
		}
	}

	return tournaments, nil
}

// parsePaging returns the paging widget's uuid and page count.
func parsePaging(payload string) (string, int) {
	m := pagingPattern.FindStringSubmatch(payload)
	if m == nil {
		return "", 0
	}
	count, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0
	}
	return m[1], count
}

// dedupKey identifies a tournament across pages.
func dedupKey(t models.Tournament) string {
	if t.Id != "" {
		return t.Id
	}
	return t.Title + "|" + t.Date
}

// ParseGrid extracts tournaments from a ZK grid payload.
//
// It is exported so tests can run against a recorded fixture without any
// network access.
func ParseGrid(payload string, fed models.Federation) []models.Tournament {
	var tournaments []models.Tournament

	// Rows are self-contained; splitting on the row marker keeps one
	// malformed row from corrupting its neighbours.
	chunks := rowPattern.Split(payload, -1)
	for _, chunk := range chunks[1:] {
		if t, ok := parseRow(chunk, fed); ok {
			tournaments = append(tournaments, t)
		}
	}

	return tournaments
}

func parseRow(chunk string, fed models.Federation) (models.Tournament, bool) {
	dateMatch := datePattern.FindStringSubmatch(chunk)
	if dateMatch == nil {
		return models.Tournament{}, false
	}

	linkMatch := linkPattern.FindStringSubmatch(chunk)
	if linkMatch == nil {
		return models.Tournament{}, false
	}

	tournament := models.Tournament{
		Date:     util.NormalizeWhitespace(dateMatch[1]),
		Location: util.NormalizeWhitespace(dateMatch[2]),
		URL:      linkMatch[1],
		Title:    util.NormalizeWhitespace(linkMatch[2]),
		Entries:  []models.CompetitionEntry{},
	}

	if id := idPattern.FindStringSubmatch(tournament.URL); id != nil {
		tournament.Id = id[1]
	}

	if tournament.Title == "" {
		return models.Tournament{}, false
	}

	// BTV publishes no organizer, so the venue city stands in for it. The map
	// popup and the Google Maps link both use this field.
	tournament.Organizer = tournament.Location

	tournament.Entries = parseCompetitions(chunk, dateMatch[0])

	return tournament, true
}

// parseCompetitions reads the abbreviated competition list, e.g.
// "W30/E, M40/D". The abbreviations are expanded so they read like the other
// federations' values.
func parseCompetitions(chunk, dateLabel string) []models.CompetitionEntry {
	var entries []models.CompetitionEntry

	for _, m := range labelPattern.FindAllStringSubmatch(chunk, -1) {
		value := strings.TrimSpace(m[1])
		// Skip the date label and the registration deadline.
		if m[0] == dateLabel || value == "" || !strings.Contains(value, "/") {
			continue
		}

		for _, part := range strings.Split(value, ",") {
			if comp := expandCompetition(strings.TrimSpace(part)); comp != "" {
				entries = append(entries, models.CompetitionEntry{Competition: comp})
			}
		}
		break // the competition list is a single label
	}

	return entries
}

// expandCompetition turns "W30/E" into "Damen 30 Einzel".
//
// The codes are: W = women, M = men, X = mixed; a two-digit age group where 00
// means open; E = singles, D = doubles.
func expandCompetition(code string) string {
	parts := strings.Split(code, "/")
	if len(parts) != 2 || len(parts[0]) < 2 {
		return ""
	}

	var gender string
	switch parts[0][0] {
	case 'W':
		gender = "Damen"
	case 'M':
		gender = "Herren"
	case 'X':
		gender = "Mixed"
	default:
		return ""
	}

	age := strings.TrimSpace(parts[0][1:])

	var discipline string
	switch strings.TrimSpace(parts[1]) {
	case "E":
		discipline = "Einzel"
	case "D":
		discipline = "Doppel"
	default:
		return ""
	}

	if age == "" || age == "00" {
		return gender + " " + discipline
	}
	return gender + " " + age + " " + discipline
}

// unescapeZK decodes the \xNN escapes ZK uses for non-ASCII characters.
func unescapeZK(s string) string {
	return zkEscapePattern.ReplaceAllStringFunc(s, func(m string) string {
		v, err := strconv.ParseUint(m[2:], 16, 8)
		if err != nil {
			return m
		}
		return string(rune(v))
	})
}

// resolveURL turns the ZK update URI into an absolute URL.
func resolveURL(base, ref string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", base, err)
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("invalid update URI %q: %w", ref, err)
	}
	return baseURL.ResolveReference(refURL).String(), nil
}
