package addressprovider

import (
	"context"

	"github.com/timoknapp/tennis-tournament-finder/pkg/clublocations"
	"github.com/timoknapp/tennis-tournament-finder/pkg/placename"
)

// OverrideProvider resolves venues from the curated club-locations file.
// It is the highest-precedence source: an entry there is a deliberate manual
// correction and must win over any heuristic.
type OverrideProvider struct {
	// Table defaults to the process-wide table when nil.
	Table *clublocations.Table
}

func (p OverrideProvider) Name() string { return "overrides" }

func (p OverrideProvider) Resolve(_ context.Context, req Request) (Address, error) {
	table := p.Table
	if table == nil {
		t, err := clublocations.Default()
		if err != nil {
			return Address{}, err
		}
		table = t
	}

	if req.Organizer == "" {
		return Address{}, ErrNotFound
	}

	o, ok := table.Lookup(req.Organizer)
	if !ok {
		return Address{}, ErrNotFound
	}

	return Address{
		Place:  o.City,
		State:  o.State,
		Lat:    o.Lat,
		Lon:    o.Lon,
		Source: p.Name(),
	}, nil
}

// HeuristicProvider derives a settlement name from the published location or
// the club name. It is the default source and never performs I/O.
//
// It returns the single best candidate; callers that want to try alternatives
// should use placename.Candidates directly.
type HeuristicProvider struct{}

func (p HeuristicProvider) Name() string { return "heuristic" }

func (p HeuristicProvider) Resolve(_ context.Context, req Request) (Address, error) {
	if req.Location != "" {
		return Address{Place: req.Location, Source: p.Name()}, nil
	}

	candidates := placename.Candidates(req.Organizer)
	if len(candidates) == 0 {
		return Address{}, ErrNotFound
	}

	return Address{Place: candidates[0], Source: p.Name()}, nil
}

// Default returns the provider chain used in production: curated overrides
// first, then the name heuristic.
//
// The upstream detail lookup (issue #55) is deliberately absent. It depends on
// an undocumented widget protocol, so it stays opt-in until it can be driven
// reliably and cached per club.
func Default() Chain {
	return Chain{
		Providers: []Provider{
			OverrideProvider{},
			HeuristicProvider{},
		},
	}
}
