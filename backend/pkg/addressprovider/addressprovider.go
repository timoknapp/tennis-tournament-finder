// Package addressprovider defines how a tournament's venue location is
// resolved, and in which order the available sources are consulted.
//
// Federations publish only a club name, so the location has to be derived.
// Several sources are possible and they differ in cost and reliability, hence
// the explicit interface and precedence order:
//
//  1. Curated overrides   - free, exact, manually maintained
//  2. Name heuristics     - free, ~good, the default
//  3. Upstream detail page - authoritative but slow, rate limited and fragile
//
// Only the first two are enabled by default. The third is opt-in because it
// depends on an undocumented widget protocol that can change without notice;
// see issue #55.
package addressprovider

import (
	"context"
	"errors"
)

// ErrNotFound indicates a provider has no answer for this tournament. It is a
// normal outcome, not a failure, and simply moves resolution to the next
// provider.
var ErrNotFound = errors.New("address not found")

// Request describes what should be resolved.
type Request struct {
	TournamentID string
	// Organizer is the club name as published by the federation.
	Organizer string
	// Location is the venue string when the federation publishes one.
	Location string
	// AcceptedStates limits results to the federation's coverage area.
	AcceptedStates []string
}

// Address is a resolved venue location. Providers fill in what they know;
// Street and PostalCode are only available from the upstream detail source.
type Address struct {
	// Place is the settlement name, always set on success.
	Place string
	// Street and PostalCode are optional and may be empty.
	Street     string
	PostalCode string
	// State is the federal state the place belongs to.
	State string
	// Lat/Lon are set when the provider resolved coordinates itself. When
	// empty, the caller geocodes Place.
	Lat string
	Lon string
	// Source names the provider that produced this result, for diagnostics.
	Source string
}

// HasCoordinates reports whether the address already carries coordinates.
func (a Address) HasCoordinates() bool {
	return a.Lat != "" && a.Lon != ""
}

// Provider resolves a tournament's venue address.
//
// Implementations must respect ctx, must not block indefinitely, and must
// return ErrNotFound rather than a zero Address when they have no answer.
type Provider interface {
	// Name identifies the provider in logs and diagnostics.
	Name() string
	// Resolve returns the venue address, or ErrNotFound.
	Resolve(ctx context.Context, req Request) (Address, error)
}

// Chain queries providers in order and returns the first successful result.
//
// A provider returning an error other than ErrNotFound does not abort the
// chain: a broken optional source must never prevent a cheaper one from
// answering. The last non-ErrNotFound error is returned when nothing resolves,
// so failures stay visible.
type Chain struct {
	Providers []Provider
	// OnError is called for non-ErrNotFound failures, for logging/metrics.
	OnError func(provider string, err error)
}

// Resolve walks the chain in order.
func (c Chain) Resolve(ctx context.Context, req Request) (Address, error) {
	var lastErr error

	for _, p := range c.Providers {
		if err := ctx.Err(); err != nil {
			return Address{}, err
		}

		addr, err := p.Resolve(ctx, req)
		switch {
		case err == nil:
			if addr.Source == "" {
				addr.Source = p.Name()
			}
			return addr, nil
		case errors.Is(err, ErrNotFound):
			// Expected: try the next provider.
		default:
			lastErr = err
			if c.OnError != nil {
				c.OnError(p.Name(), err)
			}
		}
	}

	if lastErr != nil {
		return Address{}, lastErr
	}
	return Address{}, ErrNotFound
}
