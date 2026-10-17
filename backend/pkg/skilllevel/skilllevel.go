// Package skilllevel parses the German tennis "Leistungsklasse" (LK) ranges
// that federations publish alongside each competition.
//
// A player's LK is a number from 1.0 (best) to 25.0 (beginner), and a
// competition is open to a range of them. Federations publish that range as
// free text in several formats, so filtering by LK needs a tolerant parser
// rather than string matching.
//
// Formats observed in live data across all 15 federations:
//
//	"1,0–25,0"     en dash, decimal comma  (most common, ~4600 occurrences)
//	"LK 1-25"      prefix, hyphen, integers
//	"LK 1.5-25"    decimal point
//	"LK 4-24.9"    decimal point on the upper bound
//	"12,6–25,0"    fractional bounds
//	""             no restriction given
package skilllevel

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Bounds of the official LK scale.
const (
	Min = 1.0
	Max = 25.0
)

// Range is an inclusive LK range. A competition with From=1, To=25 is open to
// everyone.
type Range struct {
	From float64
	To   float64
}

// IsZero reports whether the range carries no information.
func (r Range) IsZero() bool {
	return r.From == 0 && r.To == 0
}

// Includes reports whether a player's LK may enter this competition.
func (r Range) Includes(lk float64) bool {
	if r.IsZero() {
		// Nothing published: treat as open rather than hiding the tournament.
		return true
	}
	return lk >= r.From && lk <= r.To
}

// Overlaps reports whether two ranges share any value.
func (r Range) Overlaps(other Range) bool {
	if r.IsZero() || other.IsZero() {
		return true
	}
	return r.From <= other.To && other.From <= r.To
}

// String renders the range in the canonical "LK 1,0–25,0" form.
func (r Range) String() string {
	if r.IsZero() {
		return ""
	}
	return fmt.Sprintf("LK %s–%s", formatLK(r.From), formatLK(r.To))
}

func formatLK(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	return strings.Replace(s, ".", ",", 1)
}

// rangePattern matches an LK range with either decimal separator. The dash
// class covers hyphen, en dash and em dash, all of which appear in live data.
//
// The bounds are anchored so a longer number cannot match a substring of
// itself: without that, a founding year like "1971" would parse as LK 19.
var rangePattern = regexp.MustCompile(`(?:^|[^\d.,])(\d{1,2}(?:[.,]\d)?)\s*[-–—]\s*(\d{1,2}(?:[.,]\d)?)(?:[^\d.,]|$)`)

// singlePattern matches a lone value, e.g. "LK 12", with the same anchoring.
var singlePattern = regexp.MustCompile(`(?:^|[^\d.,])(\d{1,2}(?:[.,]\d)?)(?:[^\d.,]|$)`)

// Parse extracts the LK range from a federation's free-text skill level.
//
// ok is false when the text carries no usable range; callers should treat that
// as "no restriction" rather than as an error, since a competition without a
// published LK range is open.
func Parse(text string) (Range, bool) {
	cleaned := strings.TrimSpace(text)
	if cleaned == "" || cleaned == "&nbsp;" {
		return Range{}, false
	}

	if m := rangePattern.FindStringSubmatch(cleaned); m != nil {
		from, okFrom := parseValue(m[1])
		to, okTo := parseValue(m[2])
		if !okFrom || !okTo {
			return Range{}, false
		}
		// Some sources publish the bounds the other way round.
		if from > to {
			from, to = to, from
		}
		return Range{From: from, To: to}, true
	}

	// A single value means that exact class only.
	if m := singlePattern.FindStringSubmatch(cleaned); m != nil {
		v, ok := parseValue(m[1])
		if !ok {
			return Range{}, false
		}
		return Range{From: v, To: v}, true
	}

	return Range{}, false
}

func parseValue(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
	if err != nil {
		return 0, false
	}
	if v < Min || v > Max {
		return 0, false
	}
	return v, true
}

// ParsePlayerLK parses a player's own LK, accepting both decimal separators.
func ParsePlayerLK(text string) (float64, bool) {
	cleaned := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "LK"))
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return 0, false
	}
	return parseValue(cleaned)
}
