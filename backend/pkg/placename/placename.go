// Package placename derives candidate settlement names from German tennis club
// names, so they can be geocoded.
//
// Federations only publish a club name ("TC Rot-Weiß Karlsruhe e.V."), never a
// postal address. Turning that into a place a geocoder understands is the main
// source of wrong map pins, so the logic lives here where it can be tested
// exhaustively without network access.
package placename

import (
	"regexp"
	"strings"
	"unicode"
)

// noiseTokens never form part of a settlement name. Compared lowercased and
// stripped of trailing dots.
var noiseTokens = map[string]bool{
	// Club type abbreviations
	"tc": true, "tk": true, "tg": true, "tv": true, "sg": true, "sv": true,
	"skv": true, "fc": true, "atv": true, "sus": true, "tsg": true, "sc": true,
	"sf": true, "tsc": true, "tus": true, "djk": true, "vfl": true, "vfb": true,
	"mtv": true, "tsv": true, "rsv": true, "esv": true, "psv": true, "bsv": true,
	"tec": true, "ttc": true, "utc": true, "etuf": true,

	// Spelled-out club types
	"tennis": true, "tennisclub": true, "tennisverein": true, "tennisklub": true,
	"club": true, "klub": true, "verein": true, "sportverein": true,
	"turnverein": true, "sportgemeinschaft": true, "tennisgemeinschaft": true,
	"sportvereinigung": true, "sportfreunde": true, "turnerschaft": true,
	"spielvereinigung": true, "ballspielverein": true, "gemeinschaft": true,
	"sport": true, "turn": true, "abteilung": true, "abt": true,

	// Legal form
	"ev": true, "e": true, "v": true,

	// Colours and common club epithets
	"blau": true, "weiss": true, "weiß": true, "rot": true, "grün": true,
	"gruen": true, "gelb": true, "schwarz": true, "blau-weiß": true,
	"gw": true, "bw": true, "rw": true, "sw": true,
	"germania": true, "olympia": true, "optimus": true, "nicolai": true,
	"borussia": true, "alemannia": true, "teutonia": true, "concordia": true,
	"eintracht": true, "viktoria": true, "fortuna": true, "union": true,
	"post": true, "tura": true, "bezirk": true, "polizei": true,

	// Filler words
	"und": true, "u": true, "der": true, "die": true, "das": true, "von": true,
	"zu": true, "am": true, "im": true, "an": true, "in": true, "bei": true,
	"der/die": true, "für": true,
}

// placePrefixes may legitimately begin a settlement name and must stay attached
// to the following token ("Bad Schönborn", "Sankt Augustin").
var placePrefixes = map[string]bool{
	"bad": true, "sankt": true, "st": true, "neu": true, "alt": true,
	"gross": true, "groß": true, "klein": true, "ober": true, "unter": true,
	"nieder": true, "hohen": true,
}

var (
	// Legal form, with or without spaces/dots.
	reLegalForm = regexp.MustCompile(`(?i)\be\.?\s*v\.?\b`)
	// "vor der Höhe", "an der Ruhr", "a.d. Donau", ...
	reGeoQualifier = regexp.MustCompile(`(?i)\b[avi]\.?\s*d\.?\s*[a-zäöüß]*\.?`)
	// Founding years: 1890, 1920/75, 08/29
	reYear = regexp.MustCompile(`\b\d{2,4}(/\d{2,4})?\b`)
	// Roman numerals used as club suffixes
	reRoman = regexp.MustCompile(`(?i)\b[ivx]{1,4}\b`)
)

// Candidates returns possible settlement names for a club or location string,
// most promising first. The caller is expected to try them in order and accept
// the first that a geocoder resolves inside an expected region.
//
// Returns nil when nothing usable can be derived.
func Candidates(name string) []string {
	cleaned := clean(name)
	tokens := meaningfulTokens(cleaned)
	if len(tokens) == 0 {
		return nil
	}

	var out []string
	seen := make(map[string]bool)

	add := func(s string) {
		s = strings.TrimSpace(s)
		if len([]rune(s)) < 3 || seen[strings.ToLower(s)] {
			return
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}

	// 1. Multi-word names introduced by a place prefix ("Bad Schönborn").
	for i, tok := range tokens {
		if placePrefixes[normalizeToken(tok)] && i+1 < len(tokens) {
			add(tok + " " + tokens[i+1])
		}
	}

	// 2. Hyphenated compounds ("Mannheim-Neckarau"): try the full compound
	//    first, then each part. The leading part is usually the main city.
	//    Compounds made purely of noise ("Rot-Weiß") are skipped entirely.
	for _, tok := range tokens {
		if !strings.Contains(tok, "-") {
			continue
		}

		parts := strings.Split(tok, "-")
		var meaningful []string
		for _, part := range parts {
			if part != "" && !noiseTokens[normalizeToken(part)] {
				meaningful = append(meaningful, part)
			}
		}
		if len(meaningful) == 0 {
			continue
		}

		// Only offer the whole compound when no part is noise, otherwise
		// "Grün-Weiß Mannheim" style leftovers reach the geocoder.
		if len(meaningful) == len(parts) {
			add(tok)
		}
		for _, part := range meaningful {
			add(part)
		}
	}

	// 3. Literal tokens, from the end: the city is usually the last word
	//    ("TC Blau-Weiß Neuss").
	for i := len(tokens) - 1; i >= 0; i-- {
		tok := tokens[i]
		if placePrefixes[normalizeToken(tok)] {
			continue // only meaningful together with its successor
		}
		// Hyphenated tokens were already expanded above.
		if strings.Contains(tok, "-") {
			continue
		}
		add(tok)
	}

	// 4. Reverse German adjectival derivations ("Heidelberger" -> Heidelberg).
	//    Placed last so literal names win when both are plausible.
	for _, tok := range tokens {
		for _, d := range Deadjective(tok) {
			add(d)
		}
	}

	return out
}

// Deadjective reverses the German "-er" derivation used in club names:
//
//	Heidelberger -> Heidelberg
//	Ratinger     -> Ratingen
//	Karbener     -> Karben
//	Eppelheimer  -> Eppelheim
//
// Several endings are plausible, so all are returned for the caller to try.
// Returns nil when the word is not an adjectival form.
func Deadjective(word string) []string {
	lower := strings.ToLower(word)
	if !strings.HasSuffix(lower, "er") || len([]rune(word)) <= 4 {
		return nil
	}
	if noiseTokens[normalizeToken(word)] {
		return nil
	}

	stem := word[:len(word)-2]
	if len([]rune(stem)) < 3 {
		return nil
	}

	// Order matters: the bare stem is by far the most common.
	return []string{stem, stem + "en", stem + "n", stem + "m"}
}

// clean removes markup noise, legal forms, years and qualifiers.
func clean(name string) string {
	s := strings.ReplaceAll(name, "\u00a0", " ")
	s = reLegalForm.ReplaceAllString(s, " ")
	s = reGeoQualifier.ReplaceAllString(s, " ")
	s = reYear.ReplaceAllString(s, " ")

	// Separators that are never part of a single token.
	s = strings.NewReplacer(",", " ", "/", " ", "(", " ", ")", " ",
		"&", " ", "+", " ", ".", " ").Replace(s)

	return strings.Join(strings.Fields(s), " ")
}

// meaningfulTokens drops noise words, numbers and stray single characters.
func meaningfulTokens(cleaned string) []string {
	var out []string

	for _, tok := range strings.Fields(cleaned) {
		norm := normalizeToken(tok)
		if norm == "" || noiseTokens[norm] {
			continue
		}
		if isNumeric(tok) || reRoman.MatchString(norm) && len(norm) <= 3 {
			continue
		}
		if len([]rune(tok)) < 2 {
			continue
		}
		// Club names are capitalized; lowercase leftovers are almost always
		// filler that survived the noise list.
		if !startsUpper(tok) {
			continue
		}
		out = append(out, tok)
	}

	return out
}

func normalizeToken(tok string) string {
	return strings.ToLower(strings.Trim(tok, ".-"))
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func startsUpper(s string) bool {
	for _, r := range s {
		return unicode.IsUpper(r)
	}
	return false
}
