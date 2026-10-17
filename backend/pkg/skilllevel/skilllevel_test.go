package skilllevel

import (
	"math"
	"testing"
)

func eq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestParseLiveFormats covers every skill level format observed across all 15
// federations. These strings are real; a change here breaks LK filtering
// silently, so they are pinned.
func TestParseLiveFormats(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantFrom float64
		wantTo   float64
	}{
		// Most common format: en dash with decimal comma.
		{"en dash decimal comma", "1,0–25,0", 1.0, 25.0},
		{"restricted lower bound", "5,0–25,0", 5.0, 25.0},
		{"fractional lower bound", "1,6–25,0", 1.6, 25.0},
		{"fractional both bounds", "12,6–25,0", 12.6, 25.0},
		{"restricted upper bound", "1,0–14,9", 1.0, 14.9},
		{"narrow range", "10,0–19,9", 10.0, 19.9},

		// "LK" prefix with hyphen and integers.
		{"lk prefix integers", "LK 1-25", 1.0, 25.0},
		{"lk prefix restricted", "LK 15-25", 15.0, 25.0},
		{"lk prefix narrow", "LK 1-12", 1.0, 12.0},
		{"lk prefix both restricted", "LK 3-21", 3.0, 21.0},

		// Decimal point instead of comma.
		{"decimal point lower", "LK 1.5-25", 1.5, 25.0},
		{"decimal point upper", "LK 4-24.9", 4.0, 24.9},

		// Whitespace and separator tolerance.
		{"spaces around dash", "LK 5 - 20", 5.0, 20.0},
		{"plain hyphen no prefix", "5-20", 5.0, 20.0},
		{"em dash", "1,0—25,0", 1.0, 25.0},
		{"leading trailing space", "  LK 1-25  ", 1.0, 25.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Parse(tt.in)
			if !ok {
				t.Fatalf("Parse(%q) returned ok=false", tt.in)
			}
			if !eq(got.From, tt.wantFrom) || !eq(got.To, tt.wantTo) {
				t.Errorf("Parse(%q) = %v–%v, want %v–%v",
					tt.in, got.From, got.To, tt.wantFrom, tt.wantTo)
			}
		})
	}
}

func TestParseSingleValue(t *testing.T) {
	got, ok := Parse("LK 12")
	if !ok {
		t.Fatal("Parse(\"LK 12\") returned ok=false")
	}
	if !eq(got.From, 12) || !eq(got.To, 12) {
		t.Errorf("got %v–%v, want 12–12", got.From, got.To)
	}
}

func TestParseSwapsReversedBounds(t *testing.T) {
	got, ok := Parse("25,0–1,0")
	if !ok {
		t.Fatal("returned ok=false")
	}
	if !eq(got.From, 1) || !eq(got.To, 25) {
		t.Errorf("got %v–%v, want the bounds normalized to 1–25", got.From, got.To)
	}
}

func TestParseRejectsUnusableInput(t *testing.T) {
	// An unparseable value must report ok=false so the caller can treat the
	// competition as unrestricted instead of hiding it.
	for _, in := range []string{"", "   ", "&nbsp;", "offen", "k.A.", "-", "–"} {
		if _, ok := Parse(in); ok {
			t.Errorf("Parse(%q) returned ok=true, want false", in)
		}
	}
}

func TestParseRejectsOutOfScaleValues(t *testing.T) {
	// The LK scale is 1..25; anything else is a misparse, e.g. a year.
	for _, in := range []string{"0,0–25,0", "1,0–99,0", "30-40", "1971"} {
		if r, ok := Parse(in); ok {
			t.Errorf("Parse(%q) = %v, want it rejected as out of scale", in, r)
		}
	}
}

func TestIncludes(t *testing.T) {
	r := Range{From: 5, To: 15}

	tests := []struct {
		lk   float64
		want bool
	}{
		{5, true},  // lower bound is inclusive
		{15, true}, // upper bound is inclusive
		{10, true},
		{4.9, false},
		{15.1, false},
		{1, false},
		{25, false},
	}

	for _, tt := range tests {
		if got := r.Includes(tt.lk); got != tt.want {
			t.Errorf("Range{5,15}.Includes(%v) = %v, want %v", tt.lk, got, tt.want)
		}
	}
}

// TestZeroRangeIncludesEverything is the deliberate choice not to hide
// tournaments that publish no LK range.
func TestZeroRangeIncludesEverything(t *testing.T) {
	var r Range
	for _, lk := range []float64{1, 12.5, 25} {
		if !r.Includes(lk) {
			t.Errorf("zero range excluded LK %v; unrestricted competitions must stay visible", lk)
		}
	}
}

func TestOverlaps(t *testing.T) {
	tests := []struct {
		name string
		a, b Range
		want bool
	}{
		{"identical", Range{1, 25}, Range{1, 25}, true},
		{"partial", Range{1, 12}, Range{10, 25}, true},
		{"touching at a point", Range{1, 12}, Range{12, 25}, true},
		{"disjoint", Range{1, 11}, Range{12, 25}, false},
		{"contained", Range{5, 10}, Range{1, 25}, true},
		{"zero range always overlaps", Range{}, Range{5, 10}, true},
		{"other zero range", Range{5, 10}, Range{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Overlaps(tt.b); got != tt.want {
				t.Errorf("%v.Overlaps(%v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestString(t *testing.T) {
	tests := []struct {
		r    Range
		want string
	}{
		{Range{1, 25}, "LK 1,0–25,0"},
		{Range{12.6, 25}, "LK 12,6–25,0"},
		{Range{}, ""},
	}

	for _, tt := range tests {
		if got := tt.r.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestParsePlayerLK(t *testing.T) {
	tests := []struct {
		in     string
		want   float64
		wantOK bool
	}{
		{"12", 12, true},
		{"12,5", 12.5, true},
		{"12.5", 12.5, true},
		{"LK 12,5", 12.5, true},
		{"  LK 7  ", 7, true},
		{"1", 1, true},
		{"25", 25, true},
		{"", 0, false},
		{"abc", 0, false},
		{"0", 0, false},  // below the scale
		{"26", 0, false}, // above the scale
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := ParsePlayerLK(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ParsePlayerLK(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}
			if ok && !eq(got, tt.want) {
				t.Errorf("ParsePlayerLK(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestRoundTrip ensures the canonical rendering parses back to the same range.
func TestRoundTrip(t *testing.T) {
	for _, r := range []Range{{1, 25}, {5, 15}, {12.6, 24.9}, {1.5, 1.5}} {
		parsed, ok := Parse(r.String())
		if !ok {
			t.Fatalf("Parse(%q) returned ok=false", r.String())
		}
		if !eq(parsed.From, r.From) || !eq(parsed.To, r.To) {
			t.Errorf("round trip of %v gave %v", r, parsed)
		}
	}
}
