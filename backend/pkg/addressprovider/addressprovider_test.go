package addressprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/timoknapp/tennis-tournament-finder/pkg/clublocations"
)

// stubProvider is a configurable Provider for exercising the chain.
type stubProvider struct {
	name    string
	addr    Address
	err     error
	calls   int
	blockCh chan struct{}
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Resolve(ctx context.Context, req Request) (Address, error) {
	s.calls++
	if s.blockCh != nil {
		select {
		case <-s.blockCh:
		case <-ctx.Done():
			return Address{}, ctx.Err()
		}
	}
	return s.addr, s.err
}

func TestChainReturnsFirstSuccess(t *testing.T) {
	first := &stubProvider{name: "overrides", err: ErrNotFound}
	second := &stubProvider{name: "heuristic", addr: Address{Place: "Karlsruhe"}}
	third := &stubProvider{name: "detail", addr: Address{Place: "Never reached"}}

	chain := Chain{Providers: []Provider{first, second, third}}

	got, err := chain.Resolve(context.Background(), Request{Organizer: "TC Karlsruhe"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Place != "Karlsruhe" {
		t.Errorf("Place = %q, want Karlsruhe", got.Place)
	}
	// The source is filled in automatically for diagnostics.
	if got.Source != "heuristic" {
		t.Errorf("Source = %q, want heuristic", got.Source)
	}
	if third.calls != 0 {
		t.Errorf("later provider was called %d times, want 0", third.calls)
	}
}

// TestChainContinuesAfterProviderError is the important resilience property:
// the optional upstream lookup is the most likely to break, and it must never
// stop a cheaper provider from answering.
func TestChainContinuesAfterProviderError(t *testing.T) {
	boom := errors.New("upstream changed its markup")

	broken := &stubProvider{name: "detail", err: boom}
	working := &stubProvider{name: "heuristic", addr: Address{Place: "Karlsruhe"}}

	var reported []string
	chain := Chain{
		Providers: []Provider{broken, working},
		OnError: func(provider string, err error) {
			reported = append(reported, provider)
		},
	}

	got, err := chain.Resolve(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Resolve() error = %v, want the working provider to answer", err)
	}
	if got.Place != "Karlsruhe" {
		t.Errorf("Place = %q, want Karlsruhe", got.Place)
	}

	// The failure must still be visible rather than silently swallowed.
	if len(reported) != 1 || reported[0] != "detail" {
		t.Errorf("reported errors = %v, want [detail]", reported)
	}
}

func TestChainReturnsNotFoundWhenNoProviderAnswers(t *testing.T) {
	chain := Chain{Providers: []Provider{
		&stubProvider{name: "a", err: ErrNotFound},
		&stubProvider{name: "b", err: ErrNotFound},
	}}

	_, err := chain.Resolve(context.Background(), Request{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestChainSurfacesLastRealError(t *testing.T) {
	boom := errors.New("timeout")

	chain := Chain{Providers: []Provider{
		&stubProvider{name: "a", err: ErrNotFound},
		&stubProvider{name: "b", err: boom},
	}}

	_, err := chain.Resolve(context.Background(), Request{})
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the underlying failure", err)
	}
}

func TestEmptyChainReturnsNotFound(t *testing.T) {
	_, err := Chain{}.Resolve(context.Background(), Request{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestChainRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	provider := &stubProvider{name: "a", addr: Address{Place: "Karlsruhe"}}
	chain := Chain{Providers: []Provider{provider}}

	if _, err := chain.Resolve(ctx, Request{}); err == nil {
		t.Error("Resolve() with a cancelled context returned nil error")
	}
	if provider.calls != 0 {
		t.Errorf("provider was called %d times despite cancellation", provider.calls)
	}
}

func TestAddressHasCoordinates(t *testing.T) {
	tests := []struct {
		name string
		addr Address
		want bool
	}{
		{"both set", Address{Lat: "49.0", Lon: "8.4"}, true},
		{"only lat", Address{Lat: "49.0"}, false},
		{"only lon", Address{Lon: "8.4"}, false},
		{"neither", Address{Place: "Karlsruhe"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.addr.HasCoordinates(); got != tt.want {
				t.Errorf("HasCoordinates() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOverrideProviderResolvesCuratedEntry(t *testing.T) {
	table, err := clublocations.Parse([]byte(`{"overrides":[
		{"match":"Lohausener Sport-Verein","city":"Düsseldorf","state":"Nordrhein-Westfalen","note":"district"}
	]}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	p := OverrideProvider{Table: table}

	got, err := p.Resolve(context.Background(), Request{Organizer: "Lohausener Sport-Verein"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Place != "Düsseldorf" || got.State != "Nordrhein-Westfalen" {
		t.Errorf("got %+v, want the curated entry", got)
	}
	if got.Source != "overrides" {
		t.Errorf("Source = %q, want overrides", got.Source)
	}
}

func TestOverrideProviderReportsNotFound(t *testing.T) {
	table, err := clublocations.Parse([]byte(`{"overrides":[]}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	p := OverrideProvider{Table: table}

	for _, organizer := range []string{"", "TC Unbekannt"} {
		if _, err := p.Resolve(context.Background(), Request{Organizer: organizer}); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) error = %v, want ErrNotFound", organizer, err)
		}
	}
}

func TestHeuristicProviderPrefersPublishedLocation(t *testing.T) {
	p := HeuristicProvider{}

	got, err := p.Resolve(context.Background(), Request{
		Location:  "Karlsruhe",
		Organizer: "TC Irgendwas Anderes",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Place != "Karlsruhe" {
		t.Errorf("Place = %q, want the published location to win", got.Place)
	}
}

func TestHeuristicProviderDerivesFromClubName(t *testing.T) {
	p := HeuristicProvider{}

	got, err := p.Resolve(context.Background(), Request{Organizer: "TC Blau-Weiß Neuss"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Place != "Neuss" {
		t.Errorf("Place = %q, want Neuss", got.Place)
	}
}

func TestHeuristicProviderReportsNotFoundForUnusableInput(t *testing.T) {
	p := HeuristicProvider{}

	for _, organizer := range []string{"", "e.V.", "TC", "1890"} {
		if _, err := p.Resolve(context.Background(), Request{Organizer: organizer}); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) error = %v, want ErrNotFound", organizer, err)
		}
	}
}

// TestDefaultChainPrefersOverrides documents the production precedence order.
func TestDefaultChainPrefersOverrides(t *testing.T) {
	chain := Default()

	// "Lohausener Sport-Verein" is in the shipped override file and cannot be
	// derived from its name, so the override must win.
	got, err := chain.Resolve(context.Background(), Request{
		Organizer: "Lohausener Sport-Verein",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != "overrides" || got.Place != "Düsseldorf" {
		t.Errorf("got %+v, want the curated override to win", got)
	}
}
