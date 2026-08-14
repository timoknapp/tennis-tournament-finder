package tournament

import (
	"context"
	"time"

	"github.com/timoknapp/tennis-tournament-finder/pkg/federation"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
)

// Warmup preloads tournaments for the given date range and optional filters.
// It reuses the same code paths as the HTTP handler, so it fills both the
// result cache and the geocoding cache.
// dateFrom/dateTo format: "02.01.2006". Empty values default to today..today+14d.
// compType and selectedFederations are optional (comma-separated IDs).
func Warmup(dateFrom, dateTo, compType, selectedFederations string) int {
	return WarmupContext(context.Background(), dateFrom, dateTo, compType, selectedFederations)
}

// WarmupContext behaves like Warmup but honours cancellation via ctx.
//
// Cached entries are invalidated first: the point of a scheduled run is to
// fetch current data, so serving the cache back to itself would make the job a
// no-op and let entries age out into staleness.
func WarmupContext(ctx context.Context, dateFrom, dateTo, compType, selectedFederations string) int {
	// Defaults
	today := time.Now()
	if dateFrom == "" {
		dateFrom = today.Format("02.01.2006")
	}
	if dateTo == "" {
		dateTo = today.Add(14 * 24 * time.Hour).Format("02.01.2006")
	}

	filteredFederations := FilterFederations(federation.GetFederations(), selectedFederations)

	logger.Info("Warmup: from %s to %s, compType: %s, federations: %s",
		dateFrom, dateTo, compType, selectedFederations)

	// Drop cached entries first: reading back the cache would make the
	// scheduled refresh a no-op and let entries age into staleness.
	InvalidateCache(filteredFederations, dateFrom, dateTo, compType)

	tournaments, results := CollectTournaments(ctx, filteredFederations, dateFrom, dateTo, compType)

	var failed int
	for _, res := range results {
		if res.Err != nil {
			failed++
			logger.Warn("Warmup: federation %s reported an error: %v", res.Federation.Id, res.Err)
		}
	}

	logger.Info("Warmup finished. Tournaments fetched: %d (%d/%d federations failed)",
		len(tournaments), failed, len(filteredFederations))

	return len(tournaments)
}
