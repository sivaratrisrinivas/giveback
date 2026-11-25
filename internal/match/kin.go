package match

import "strings"

// Side says whose surname a relative carries, which is how Giveback works out
// who a bare relation word in the notebook might be. Telugu surnames come
// first and pass from father to children; a woman takes her husband's.
type Side int

const (
	FatherSide Side = 1 << iota // carries the father's surname (his brothers, their wives, his parents)
	MotherSide                  // carries the mother's maiden surname (her brothers, their wives, her parents)
)

// Relation is a Telugu kinship word, seen from the children of the house.
type Relation struct {
	Word    string // Telugu script, as the reply shows it
	Meaning string // in English
	Sides   Side   // whose surname they may carry; 0 = can't tell (married-in or married-out)
}

// relations lists the words families write instead of names. Spellings are
// matched by Key, so మామ, "mama" and "maama" are the same entry.
var relations = []struct {
	spellings []string
	Relation
}{
	{[]string{"మామ", "మామయ్య", "mama", "mamayya"}, Relation{"మామ", "mother's brother", MotherSide}},
	{[]string{"అత్త", "అత్తయ్య", "atta", "atha", "attayya"}, Relation{"అత్త", "father's sister, or mama's wife", MotherSide}},
	{[]string{"పెద్దనాన్న", "peddananna", "pedananna"}, Relation{"పెద్దనాన్న", "father's elder brother", FatherSide}},
	{[]string{"బాబాయి", "బాబాయ్", "చిన్నాన్న", "babai", "babayi", "chinnanna"}, Relation{"బాబాయి", "father's younger brother", FatherSide}},
	{[]string{"పెద్దమ్మ", "peddamma"}, Relation{"పెద్దమ్మ", "father's elder brother's wife, or mother's elder sister", FatherSide}},
	{[]string{"పిన్ని", "pinni"}, Relation{"పిన్ని", "father's younger brother's wife, or mother's younger sister", FatherSide}},
	{[]string{"తాతయ్య", "తాత", "tatayya", "thatha", "tata"}, Relation{"తాతయ్య", "grandfather", FatherSide | MotherSide}},
	{[]string{"నానమ్మ", "nanamma", "naanamma"}, Relation{"నానమ్మ", "father's mother", FatherSide}},
	{[]string{"అమ్మమ్మ", "ammamma"}, Relation{"అమ్మమ్మ", "mother's mother", MotherSide}},
	{[]string{"అన్నయ్య", "అన్న", "annayya", "anna"}, Relation{"అన్నయ్య", "elder brother", FatherSide}},
	{[]string{"తమ్ముడు", "tammudu", "thammudu"}, Relation{"తమ్ముడు", "younger brother", FatherSide}},
	{[]string{"అక్క", "akka"}, Relation{"అక్క", "elder sister", 0}},
	{[]string{"చెల్లి", "చెల్లెలు", "chelli", "chellelu"}, Relation{"చెల్లి", "younger sister", 0}},
	{[]string{"బావ", "bava", "baava"}, Relation{"బావ", "sister's husband, or a cousin", 0}},
	{[]string{"బావమరది", "bavamaridi"}, Relation{"బావమరది", "wife's brother", 0}},
	{[]string{"వదిన", "vadina", "vadhina"}, Relation{"వదిన", "elder brother's wife", FatherSide}},
	{[]string{"మరదలు", "maradalu"}, Relation{"మరదలు", "younger brother's wife, or a cousin", 0}},
	{[]string{"అల్లుడు", "alludu"}, Relation{"అల్లుడు", "son-in-law", 0}},
	{[]string{"కోడలు", "kodalu"}, Relation{"కోడలు", "daughter-in-law", FatherSide}},
}

var relationByKey = func() map[string]Relation {
	m := map[string]Relation{}
	for _, r := range relations {
		for _, s := range r.spellings {
			m[Key(s)] = r.Relation
		}
	}
	return m
}()

// RelationOf returns the kinship word a name word is, if it is one.
// Exact keys only: relation words are short, so a fuzzy match would turn
// names into relations.
func RelationOf(word string) (Relation, bool) {
	r, ok := relationByKey[Key(word)]
	return r, ok
}

// relationIn finds a relation word in a list of words, including one written
// in two parts ("పెద్ద నాన్న"). It returns the relation and the words that
// are left (the names).
func relationIn(words []string) (Relation, []string, bool) {
	for i, w := range words {
		if r, ok := RelationOf(w); ok {
			return r, append(append([]string{}, words[:i]...), words[i+1:]...), true
		}
		if i+1 < len(words) {
			if r, ok := RelationOf(w + words[i+1]); ok {
				return r, append(append([]string{}, words[:i]...), words[i+2:]...), true
			}
		}
	}
	return Relation{}, words, false
}

// Family is what the household has told Giveback about itself: the parents
// (so a relation word can be tied to a surname), and which person each
// relation word means, learned from "Yes" answers or told with /family.
type Family struct {
	Father string            // e.g. "Bandaru Subba Rao": surname first
	Mother string            // her maiden name, e.g. "Butham Lakshmi"
	Links  map[string]string // relation word (Telugu, as in Relation.Word) -> person's name
}

// Surname is the first word of a Telugu name.
func Surname(name string) string {
	if w := Words(name); len(w) > 0 {
		return w[0]
	}
	return ""
}

// sideOf says which side's surname this name carries, if either.
func (f Family) sideOf(name string, surname string) (Side, string) {
	var s Side
	var which string
	words := Words(name)
	has := func(sur string) bool {
		if sur == "" {
			return false
		}
		if surname != "" && SameName(surname, sur) {
			return true
		}
		return len(words) > 0 && SameName(words[0], sur)
	}
	if fs := Surname(f.Father); has(fs) {
		s |= FatherSide
		which = fs
	}
	if ms := Surname(f.Mother); has(ms) {
		s |= MotherSide
		which = ms
	}
	return s, strings.TrimSpace(which)
}
