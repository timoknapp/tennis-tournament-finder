package tournament

import (
	"strings"
	"sync"
	"time"

	"github.com/timoknapp/tennis-tournament-finder/pkg/federation"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
)

// Warmup preloads tournaments for the given date range and optional filters.
// It reuses the same code paths as the HTTP handler so geocoding caches are filled.
// dateFrom/dateTo format: "02.01.2006". Empty values default to today..today+14d.
// compType and selectedFederations are optional (comma-separated IDs).
func Warmup(dateFrom, dateTo, compType, selectedFederations string) int {
	federations := federation.GetFederations()

	// Defaults
	today := time.Now()
	if dateFrom == "" {
		dateFrom = today.Format("02.01.2006")
	}
	if dateTo == "" {
		dateTo = today.Add(14 * 24 * time.Hour).Format("02.01.2006")
	}

	// Federation filtering (same behavior as in GetTournaments)
	var filteredFederations []models.Federation
	if selectedFederations != "" {
		selectedFedIds := strings.Split(selectedFederations, ",")
		for _, fed := range federations {
			for _, sel := range selectedFedIds {
				if fed.Id == strings.TrimSpace(sel) {
					filteredFederations = append(filteredFederations, fed)
					break
				}
			}
		}
	} else {
		filteredFederations = federations
	}

	logger.Info("Warmup: from %s to %s, compType: %s, federations: %s",
		dateFrom, dateTo, compType, selectedFederations)

	var total int
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < len(filteredFederations); i++ {
		wg.Add(1)
		go func(fed models.Federation) {
			defer wg.Done()

			var tournaments []models.Tournament
			if fed.ApiVersion == "old" {
				tournaments = getTournamentsFromFederationOldApi(fed, dateFrom, dateTo, compType)
			} else if fed.ApiVersion == "new" {
				tournaments = getTournamentsFromFederationNewApi(fed, dateFrom, dateTo, compType)
			}

			if len(tournaments) > 0 {
				mu.Lock()
				total += len(tournaments)
				mu.Unlock()
			}
		}(filteredFederations[i])
	}
	wg.Wait()

	logger.Info("Warmup finished. Tournaments fetched: %d", total)
	return total
}