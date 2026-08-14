// Package clublocations loads manually curated location overrides for tennis
// clubs whose venue cannot be derived from their name.
//
// Federations publish only a club name, and some of those ("Lohausener
// Sport-Verein" for a club in Düsseldorf) simply do not contain the city.
// Rather than growing special cases in code, those live in a JSON file that
// can be corrected without a rebuild.
package clublocations

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode"
)

// defaultOverrides is compiled into the binary so the service works without
// any external file. TTF_CLUB_LOCATIONS can point at a different file.
//
//go:embed data/club-locations.json
var defaultOverrides []byte

// Override is a single manual mapping.
type Override struct {
	// Match compares against the whole normalized organizer string.
	Match string `json:"match,omitempty"`
	// Contains matches when the normalized organizer contains it.
	Contains string `json:"contains,omitempty"`

	// City is geocoded like any other place name (preferred).
	City string `json:"city,omitempty"`
	// State optionally restricts the geocoding result.
	State string `json:"state,omitempty"`
	// Lat/Lon pin the location explicitly when even the city is ambiguous.
	Lat string `json:"lat,omitempty"`
	Lon string `json:"lon,omitempty"`

	Note string `json:"note,omitempty"`
}

// HasCoordinates reports whether the override pins exact coordinates.
func (o Override) HasCoordinates() bool {
	return o.Lat != "" && o.Lon != ""
}

type file struct {
	Overrides []Override `json:"overrides"`
}

// Table holds the loaded overrides in lookup-friendly form.
type Table struct {
	exact     map[string]Override
	exactFlat map[string]Override // same keys with spaces removed
	contains  []Override          // sorted by descending pattern length
}

var (
	once      sync.Once
	loaded    *Table
	loadedErr error
)

// Default returns the process-wide override table, loading it on first use.
// A malformed or missing file is logged by the caller and treated as empty, so
// bad data can never take the service down.
func Default() (*Table, error) {
	once.Do(load)
	return loaded, loadedErr
}

// ResetForTest clears the memoized table so tests can load a different file.
// It is not safe for concurrent use and is intended for tests only.
func ResetForTest() {
	once = sync.Once{}
	loaded = nil
	loadedErr = nil
}

func load() {
	raw := defaultOverrides

	if path := os.Getenv("TTF_CLUB_LOCATIONS"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			loadedErr = fmt.Errorf("failed to read %s: %w", path, err)
			loaded = &Table{}
			return
		}
		raw = data
	}

	table, err := Parse(raw)
	if err != nil {
		loadedErr = err
		loaded = &Table{}
		return
	}
	loaded = table
}

// Parse builds a lookup table from JSON.
func Parse(raw []byte) (*Table, error) {
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("failed to parse club location overrides: %w", err)
	}

	t := &Table{
		exact:     make(map[string]Override),
		exactFlat: make(map[string]Override),
	}

	for i, o := range f.Overrides {
		if o.Match == "" && o.Contains == "" {
			return nil, fmt.Errorf("override %d has neither 'match' nor 'contains'", i)
		}
		if o.City == "" && !o.HasCoordinates() {
			return nil, fmt.Errorf("override %d (%q) has neither 'city' nor lat/lon",
				i, o.Match+o.Contains)
		}
		if (o.Lat == "") != (o.Lon == "") {
			return nil, fmt.Errorf("override %d (%q) must set both lat and lon or neither",
				i, o.Match+o.Contains)
		}

		if o.Match != "" {
			norm := Normalize(o.Match)
			t.exact[norm] = o
			t.exactFlat[collapse(norm)] = o
		} else {
			t.contains = append(t.contains, o)
		}
	}

	// Longest pattern first so a specific rule beats a generic one.
	for i := 1; i < len(t.contains); i++ {
		for j := i; j > 0 &&
			len(t.contains[j].Contains) > len(t.contains[j-1].Contains); j-- {
			t.contains[j], t.contains[j-1] = t.contains[j-1], t.contains[j]
		}
	}

	return t, nil
}

// Lookup returns the override for an organizer, if any.
func (t *Table) Lookup(organizer string) (Override, bool) {
	if t == nil {
		return Override{}, false
	}

	norm := Normalize(organizer)
	if norm == "" {
		return Override{}, false
	}

	// Compare without spaces as well, so a name written "Rot-Weiß" still
	// matches one written "Rot Weiß".
	flat := collapse(norm)

	if o, ok := t.exact[norm]; ok {
		return o, true
	}
	if o, ok := t.exactFlat[flat]; ok {
		return o, true
	}

	for _, o := range t.contains {
		if strings.Contains(flat, collapse(Normalize(o.Contains))) {
			return o, true
		}
	}

	return Override{}, false
}

// Len reports how many overrides are loaded.
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	return len(t.exact) + len(t.contains)
}

// Normalize prepares a club name for comparison: lowercased, umlauts folded,
// punctuation removed and whitespace collapsed, so "TC Rot-Weiß Karlsruhe e.V."
// and "TC Rot Weiss  Karlsruhe eV" compare equal.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	prevSpace := true // trims leading space implicitly
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// Fold German umlauts so both spellings match.
			switch r {
			case 'ß':
				b.WriteString("ss")
			case 'ä':
				b.WriteString("a")
			case 'ö':
				b.WriteString("o")
			case 'ü':
				b.WriteString("u")
			default:
				b.WriteRune(r)
			}
			prevSpace = false
		default:
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		}
	}

	return strings.TrimSpace(b.String())
}

// collapse removes all spaces, so comparisons ignore where a name was split
// into separate words ("Rot-Weiß" vs "Rot Weiß").
func collapse(s string) string {
	return strings.ReplaceAll(s, " ", "")
}
