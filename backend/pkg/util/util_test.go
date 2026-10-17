package util

import (
	"reflect"
	"testing"
)

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"only whitespace", " \t\n\r ", ""},
		{"single spaces preserved", "TC Blau Weiss", "TC Blau Weiss"},
		{"double space collapsed", "TC  Karlsruhe", "TC Karlsruhe"},
		// Regression: the previous implementation only replaced double spaces
		// once, so odd/long runs left residue behind.
		{"triple space collapsed", "TC   Karlsruhe", "TC Karlsruhe"},
		{"long run collapsed", "TC          Karlsruhe", "TC Karlsruhe"},
		{"five spaces collapsed", "a     b", "a b"},
		// Regression: newlines/tabs used to be deleted, which glued together
		// words that were only separated by a line break.
		{"newline becomes space", "Herren\nEinzel", "Herren Einzel"},
		{"tab becomes space", "Herren\tEinzel", "Herren Einzel"},
		{"crlf becomes single space", "Herren\r\nEinzel", "Herren Einzel"},
		{"mixed indentation", "\n\t  Damen   \n\t Doppel \n", "Damen Doppel"},
		{"leading and trailing trimmed", "   TC Karlsruhe   ", "TC Karlsruhe"},
		{"non breaking space", "LK\u00a012,3", "LK 12,3"},
		{"narrow non breaking space", "LK\u202f12,3", "LK 12,3"},
		{"figure space", "LK\u200712,3", "LK 12,3"},
		{"byte order mark", "\ufeffTC Karlsruhe", "TC Karlsruhe"},
		{"umlauts preserved", "  Tennisclub Grün-Weiß  Süd  ", "Tennisclub Grün-Weiß Süd"},
		{"unicode text preserved", "Württembergischer  Tennisbund", "Württembergischer Tennisbund"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeWhitespace(tt.input); got != tt.want {
				t.Errorf("NormalizeWhitespace(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeWhitespaceIsIdempotent(t *testing.T) {
	inputs := []string{
		"TC   Karlsruhe\n\tAbt. Tennis",
		"  Damen\u00a0Doppel  ",
		"plain",
		"",
	}

	for _, in := range inputs {
		once := NormalizeWhitespace(in)
		twice := NormalizeWhitespace(once)
		if once != twice {
			t.Errorf("NormalizeWhitespace not idempotent for %q: %q vs %q", in, once, twice)
		}
	}
}

func TestRemoveFormatFromStringAliasesNormalizeWhitespace(t *testing.T) {
	in := "TC   Karlsruhe\n\tTennis"
	if got, want := RemoveFormatFromString(in), NormalizeWhitespace(in); got != want {
		t.Errorf("RemoveFormatFromString(%q) = %q, want %q", in, got, want)
	}
}

func TestGetStringInBetweenTwoString(t *testing.T) {
	tests := []struct {
		name      string
		str       string
		start     string
		end       string
		want      string
		wantFound bool
	}{
		{
			name:      "extracts organizer",
			str:       "Veranstalter: TC Karlsruhe Austragungsort: Karlsruhe",
			start:     "Veranstalter: ",
			end:       " Austragungsort",
			want:      "TC Karlsruhe",
			wantFound: true,
		},
		{
			name:      "missing start delimiter",
			str:       "Austragungsort: Karlsruhe",
			start:     "Veranstalter: ",
			end:       " Austragungsort",
			wantFound: false,
		},
		{
			name:      "missing end delimiter",
			str:       "Veranstalter: TC Karlsruhe",
			start:     "Veranstalter: ",
			end:       " Austragungsort",
			wantFound: false,
		},
		{
			name:      "empty match between delimiters",
			str:       "Veranstalter:  Austragungsort",
			start:     "Veranstalter: ",
			end:       " Austragungsort",
			want:      "",
			wantFound: true,
		},
		{
			name:      "uses first occurrence",
			str:       "A:1 B: A:2 B:",
			start:     "A:",
			end:       " B:",
			want:      "1",
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := GetStringInBetweenTwoString(tt.str, tt.start, tt.end)
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if found && got != tt.want {
				t.Errorf("result = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeleteEmpty(t *testing.T) {
	got := DeleteEmpty([]string{"a", "", "b", "", ""})
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DeleteEmpty() = %#v, want %#v", got, want)
	}

	if got := DeleteEmpty([]string{"", ""}); len(got) != 0 {
		t.Errorf("DeleteEmpty(all empty) = %#v, want empty", got)
	}
}

func TestConcatMultipleSlices(t *testing.T) {
	got := ConcatMultipleSlices([][]int{{1, 2}, nil, {3}, {}})
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ConcatMultipleSlices() = %#v, want %#v", got, want)
	}

	if got := ConcatMultipleSlices[int](nil); len(got) != 0 {
		t.Errorf("ConcatMultipleSlices(nil) = %#v, want empty", got)
	}
}
