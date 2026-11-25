// Package match finds the notebook lines that may belong to a person named on
// an invitation card.
//
// Names are compared by sound, across scripts (Butham = భూతం), in any order
// (given name first or surname first). Initials support a match but never
// make one. A line that names only a relation (బాబాయి) is tied to a person
// through the family tree. Doubt is never hidden: anything short of two
// matching name words, or a family "Yes", is returned as Weak so the reply
// asks "Is this them?", and a "No" from the family is remembered.
package match

import (
	"sort"
	"strings"
	"unicode"

	"github.com/sivaratrisrinivas/giveback/internal/ledger"
)

// Person is who we are looking for.
type Person struct {
	Names   []string // every way the card writes the name (English, Telugu script, ...)
	Surname string   // if the card makes it clear, e.g. "Butham"
	Place   string
}

// Display is how the reply names the person.
func (p Person) Display() string {
	for _, n := range p.Names {
		if strings.TrimSpace(n) != "" {
			return strings.TrimSpace(n)
		}
	}
	return ""
}

// PersonKey identifies a person across messages, for remembering answers:
// the sound keys of their name words, sorted, so word order and script don't
// matter. It uses the first name given (the card's English spelling).
func PersonKey(name string) string {
	var ks []string
	for _, w := range Words(name) {
		if len([]rune(w)) > 1 {
			ks = append(ks, Key(w))
		}
	}
	sort.Strings(ks)
	return strings.Join(ks, " ")
}

type Strength int

const (
	Weak      Strength = iota + 1 // ask "Is this them?" before trusting it
	Strong                        // two or more name words in common
	Confirmed                     // the family said yes
)

type Hit struct {
	Entry    ledger.Entry
	Shared   []string  // the line's name words that match the person, as written in the notebook
	Relation *Relation // the relation word on the line, if any
	Why      string    // for Weak and Confirmed hits: why it might be them, in plain words
	Strength Strength
}

// Answers are the family's replies to "Is this them?": person key -> line ID -> yes.
type Answers map[string]map[int64]bool

// Matcher holds what the family has told us.
type Matcher struct {
	Family  Family
	Answers Answers
}

// titles and honorifics carry no identity.
var titles = map[string]bool{
	"sri": true, "shri": true, "smt": true, "srimathi": true, "garu": true, "late": true,
	"mr": true, "mrs": true, "dr": true, "chi": true, "la": true, "sow": true,
	"శ్రీ": true, "శ్రీమతి": true, "గారు": true, "కీశే": true, "rs": true,
}

// Words splits a name into words as written: punctuation, titles and numbers
// dropped. Initials ("K.") are kept as one letter. Compare them with SameName.
func Words(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r))
	})
	var out []string
	for _, w := range f {
		if titles[strings.ToLower(w)] || isDigits(w) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// isInitial: a single letter, or a single Telugu letter with its vowel sign (కె).
func isInitial(w string) bool {
	rs := []rune(w)
	return len(rs) == 1 || (len(rs) == 2 && unicode.IsMark(rs[1]))
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// splitInitials separates full name words from initials ("K.V." arrives as
// two words, "k" and "v"). Initials come back as the first letter of their key.
func splitInitials(words []string) (full []string, initials []byte) {
	for _, w := range words {
		if isInitial(w) {
			if k := Key(w); k != "" {
				initials = append(initials, k[0])
			}
			continue
		}
		full = append(full, w)
	}
	return full, initials
}

// Find returns the lines that may be this person, best first, at most limit.
// Lines the family said are not this person are left out; skipped counts them.
func (m Matcher) Find(p Person, entries []ledger.Entry, limit int) (hits []Hit, skipped int) {
	// The person's name words, all spellings together.
	var want []string
	for _, n := range p.Names {
		w, _ := splitInitials(Words(n))
		want = append(want, w...)
	}
	// A typed search may be a relation word ("babai", "Vimala atha").
	wantRel, rest, wantHasRel := relationIn(want)
	if wantHasRel {
		want = rest
	}
	surname := p.Surname
	if surname == "" && len(want) >= 2 {
		surname = want[0] // Telugu names put the surname first
	}
	if p.Surname != "" {
		want = append(want, p.Surname)
	}
	wantInitials := map[byte]bool{}
	for _, w := range want {
		if k := Key(w); k != "" {
			wantInitials[k[0]] = true
		}
	}
	matches := func(w string) bool {
		for _, x := range want {
			if SameName(w, x) {
				return true
			}
		}
		return false
	}
	pkey := PersonKey(p.Display())
	said := m.Answers[pkey]
	side, sideSurname := m.Family.sideOf(p.Display(), p.Surname)

	for _, e := range entries {
		if yes, ok := said[e.ID]; ok && !yes {
			skipped++
			continue
		}
		rel, rest, hasRel := relationIn(Words(e.Name))
		full, initials := splitInitials(rest)

		var shared []string
		seen := map[string]bool{}
		for _, w := range full {
			k := Key(w)
			if !seen[k] && matches(w) {
				shared = append(shared, w)
				seen[k] = true
			}
		}
		// A reader sometimes leaves a name out of the name field (the wife in
		// "భూతం ముత్యాలు - విమల"). Words of the full line can add a match,
		// never count against one.
		for _, w := range Words(e.Text) {
			k := Key(w)
			if len([]rune(w)) < 3 || seen[k] || (e.Place != "" && SameName(w, e.Place)) {
				continue
			}
			if _, isRel := RelationOf(w); !isRel && matches(w) && !inWords(w, full) {
				shared = append(shared, w)
				seen[k] = true
			}
		}
		badInitial := false
		for _, c := range initials {
			if !wantInitials[c] {
				badInitial = true
			}
		}

		// Sharing only the surname with a line that names someone else (Butham
		// Narsimha vs "ముత్యాలు భూతం") is a relative, not them.
		if len(shared) == 1 && len(full) > 1 && surname != "" && SameName(shared[0], surname) {
			continue
		}

		h := Hit{Entry: e, Shared: shared}
		if hasRel {
			r := rel
			h.Relation = &r
		}
		if wantHasRel && hasRel && wantRel.Word == rel.Word {
			if len(want) == 0 { // just the relation word was asked for
				hits = append(hits, Hit{Entry: e, Relation: h.Relation, Shared: []string{rel.Word}, Strength: Strong})
				continue
			}
			h.Shared = append(h.Shared, rel.Word)
			shared = h.Shared
		}
		switch {
		case len(shared) >= 2 && !badInitial:
			h.Strength = Strong
		case len(shared) >= 1 && !(badInitial && len(shared) == 1):
			h.Strength = Weak
			h.Why = "only the name “" + strings.Join(shared, " ") + "” matches"
			if hasRel && side&rel.Sides != 0 {
				h.Why += ", and the line says " + relWords(rel) + " — " + sideSurname + " is " + sideName(side&rel.Sides) + " surname"
			}
		case len(full) == 0 && hasRel:
			// A bare relation word: who it means comes from the family tree.
			if linked, ok := m.Family.Links[rel.Word]; ok {
				if PersonKey(linked) != pkey {
					continue // it means someone else
				}
				h.Strength = Strong
				h.Why = rel.Word + " is " + linked + ", as you told me"
			} else if side&rel.Sides != 0 {
				h.Strength = Weak
				h.Why = "the line says " + relWords(rel) + ", and " + sideSurname + " is " + sideName(side&rel.Sides) + " surname"
			} else {
				continue
			}
		default:
			continue
		}
		if yes, ok := said[e.ID]; ok && yes {
			h.Strength = Confirmed
			h.Why = "you confirmed this is " + p.Display()
		}
		hits = append(hits, h)
	}

	samePlace := func(h Hit) bool {
		return p.Place != "" && h.Entry.Place != "" && SameName(h.Entry.Place, p.Place)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.Strength != b.Strength {
			return a.Strength > b.Strength
		}
		if len(a.Shared) != len(b.Shared) {
			return len(a.Shared) > len(b.Shared)
		}
		return samePlace(a) && !samePlace(b)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, skipped
}

func relWords(r Relation) string { return r.Word + " (" + r.Meaning + ")" }

func sideName(s Side) string {
	switch {
	case s&FatherSide != 0 && s&MotherSide != 0:
		return "your father's and mother's"
	case s&FatherSide != 0:
		return "your father's"
	default:
		return "your mother's maiden"
	}
}

func inWords(w string, ws []string) bool {
	for _, x := range ws {
		if SameName(w, x) {
			return true
		}
	}
	return false
}
