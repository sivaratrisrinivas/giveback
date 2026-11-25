// Package match finds the notebook lines that may belong to a person named on
// an invitation card.
//
// This is the simple first version (step 2): it compares name words after
// normalising them, in each script separately. It knows nothing yet about
// transliteration, initials or relation words; that is step 3. It never hides
// doubt: a line that shares only one name word is returned as Weak, so the
// reply asks "Is this them?" instead of stating it.
package match

import (
	"sort"
	"strings"
	"unicode"

	"github.com/sivaratrisrinivas/giveback/internal/ledger"
)

// Person is who we are looking for.
type Person struct {
	Names []string // every way the card writes the name (English, Telugu script, ...)
	Place string
}

type Strength int

const (
	Weak   Strength = iota + 1 // one name word in common: ask before trusting it
	Strong                     // two or more name words in common
)

type Hit struct {
	Entry    ledger.Entry
	Shared   []string // the name words both sides have
	Strength Strength
}

// titles and honorifics carry no identity.
var titles = map[string]bool{
	"sri": true, "shri": true, "smt": true, "srimathi": true, "garu": true, "late": true,
	"mr": true, "mrs": true, "dr": true, "chi": true, "la": true, "sow": true,
	"శ్రీ": true, "శ్రీమతి": true, "గారు": true, "కీశే": true,
}

// Words splits a name into normalised words: lower case, punctuation dropped,
// titles dropped, and single letters (initials like "K.") dropped.
func Words(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r))
	})
	var out []string
	for _, w := range f {
		if titles[w] || len([]rune(w)) < 2 || isDigits(w) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Find returns the lines whose written name shares words with the person,
// best first. At most limit hits are returned.
func Find(p Person, entries []ledger.Entry, limit int) []Hit {
	want := map[string]bool{}
	for _, n := range p.Names {
		for _, w := range Words(n) {
			want[w] = true
		}
	}
	var hits []Hit
	for _, e := range entries {
		var shared []string
		seen := map[string]bool{}
		for _, w := range Words(e.Name) {
			if want[w] && !seen[w] {
				shared = append(shared, w)
				seen[w] = true
			}
		}
		if len(shared) == 0 {
			continue
		}
		h := Hit{Entry: e, Shared: shared, Strength: Weak}
		if len(shared) >= 2 {
			h.Strength = Strong
		}
		hits = append(hits, h)
	}
	samePlace := func(h Hit) bool {
		return p.Place != "" && strings.EqualFold(strings.TrimSpace(h.Entry.Place), strings.TrimSpace(p.Place))
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if len(a.Shared) != len(b.Shared) {
			return len(a.Shared) > len(b.Shared)
		}
		return samePlace(a) && !samePlace(b)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
