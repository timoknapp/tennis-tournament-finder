package util

import (
	"net/http"
	"strings"
)

// NormalizeWhitespace collapses every run of Unicode whitespace (spaces, tabs,
// newlines, non-breaking spaces, ...) into a single ASCII space and trims the
// result.
//
// Scraped markup frequently contains indentation, hard line breaks and
// non-breaking spaces. Replacing separators with a single space (instead of
// deleting them) guarantees that words on different source lines never get
// concatenated into one token.
func NormalizeWhitespace(input string) string {
	// Non-breaking spaces are not covered by unicode.IsSpace, so translate the
	// common variants into ordinary spaces before splitting.
	replacer := strings.NewReplacer(
		"\u00a0", " ", // NO-BREAK SPACE (often decoded from &nbsp;)
		"\u2007", " ", // FIGURE SPACE
		"\u202f", " ", // NARROW NO-BREAK SPACE
		"\ufeff", " ", // ZERO WIDTH NO-BREAK SPACE / BOM
	)

	return strings.Join(strings.Fields(replacer.Replace(input)), " ")
}

// RemoveFormatFromString normalizes scraped text.
//
// Deprecated: use NormalizeWhitespace. Retained as a thin alias so existing
// call sites keep compiling.
func RemoveFormatFromString(input string) string {
	return NormalizeWhitespace(input)
}

// DeleteEmpty returns a copy of s without empty strings.
func DeleteEmpty(s []string) []string {
	var r []string
	for _, str := range s {
		if str != "" {
			r = append(r, str)
		}
	}
	return r
}

// Delete_empty returns a copy of s without empty strings.
//
// Deprecated: use DeleteEmpty.
func Delete_empty(s []string) []string {
	return DeleteEmpty(s)
}

func EnableCors(w *http.ResponseWriter) {
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
}

// GetStringInBetweenTwoString returns the substring between startS and endS.
// found reports whether both delimiters were present.
func GetStringInBetweenTwoString(str string, startS string, endS string) (result string, found bool) {
	s := strings.Index(str, startS)
	if s == -1 {
		return result, false
	}
	newS := str[s+len(startS):]
	e := strings.Index(newS, endS)
	if e == -1 {
		return result, false
	}
	result = newS[:e]
	return result, true
}

func ConcatMultipleSlices[T any](slices [][]T) []T {
	var totalLen int

	for _, s := range slices {
		totalLen += len(s)
	}

	result := make([]T, totalLen)

	var i int

	for _, s := range slices {
		i += copy(result[i:], s)
	}

	return result
}
