package match

import (
	"reflect"
	"testing"

	"github.com/sivaratrisrinivas/giveback/internal/ledger"
)

func TestWords(t *testing.T) {
	got := Words("Sri K. Ramana Reddy garu, Miryalaguda")
	want := []string{"K", "Ramana", "Reddy", "Miryalaguda"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
	// Telugu vowel signs are marks, not punctuation: the word must stay whole.
	if got := Words("భూతం ముత్యాలు - విమల"); !reflect.DeepEqual(got, []string{"భూతం", "ముత్యాలు", "విమల"}) {
		t.Errorf("telugu: %q", got)
	}
}

func TestSameNameAcrossScripts(t *testing.T) {
	same := [][2]string{
		{"Butham", "భూతం"}, {"Bhootham", "భూతం"}, {"Muthyalu", "ముత్యాలు"}, {"Vimala", "విమల"},
		{"Reddy", "రెడ్డి"}, {"Ramana", "రమణ"}, {"Venkata", "వెంకట"}, {"Kothapalli", "కొత్తపల్లి"},
		{"Narsimha", "నరసింహ"}, {"Narasimha", "Narsimha"}, {"Nagaiah", "నాగయ్య"}, {"Chamala", "చామల"},
		{"Yadagiri", "యాదగిరి"}, {"Jyothi", "జ్యోతి"}, {"Subba", "సుబ్బ"}, {"Lakshmi", "లక్ష్మి"},
		{"Muthyalu", "Mutyala"},
	}
	for _, c := range same {
		if !SameName(c[0], c[1]) {
			t.Errorf("%s / %s: keys %q %q should match", c[0], c[1], Key(c[0]), Key(c[1]))
		}
	}
	different := [][2]string{
		{"Ramulu", "Ramana"}, {"Narsimha", "Muthyalu"}, {"Puram", "Butham"}, {"Ramu", "Raju"},
		{"Chamala", "చాకలి"}, {"Vimala", "Kamala"},
	}
	for _, c := range different {
		if SameName(c[0], c[1]) {
			t.Errorf("%s / %s: keys %q %q should differ", c[0], c[1], Key(c[0]), Key(c[1]))
		}
	}
}

func TestRelationWords(t *testing.T) {
	for _, w := range []string{"బాబాయి", "babai", "Babayi"} {
		if r, ok := RelationOf(w); !ok || r.Word != "బాబాయి" {
			t.Errorf("%s: %v %v", w, r, ok)
		}
	}
	// Written in two parts by some readers.
	if r, rest, ok := relationIn(Words("పెద్ద నాన్న")); !ok || r.Word != "పెద్దనాన్న" || len(rest) != 0 {
		t.Errorf("two-part relation: %v %q %v", r, rest, ok)
	}
	if r, rest, ok := relationIn(Words("విమల అత్త")); !ok || r.Word != "అత్త" || !reflect.DeepEqual(rest, []string{"విమల"}) {
		t.Errorf("name + relation: %v %q %v", r, rest, ok)
	}
	if _, ok := RelationOf("Ramana"); ok {
		t.Error("a name is not a relation")
	}
}

// The notebook as written (docs/synthetic-notebook.md), as a good reading gives it.
var notebook = []ledger.Entry{
	{ID: 1, Name: "ముత్యాలు భూతం"},
	{ID: 2, Name: "K. Ramana Reddy", Place: "Miryalaguda"},
	{ID: 3, Name: "పెద్దనాన్న"},
	{ID: 4, Name: "Puram Nagaiah", Place: "Bollaram"},
	{ID: 5, Name: "B. Narsimha", Place: "Nalgonda"},
	{ID: 6, Name: "చామల యాదగిరి"},
	{ID: 7, Name: "బాబాయి"},
	{ID: 8, Name: "Alisha Saheb", Place: "Kanagal"},
	{ID: 9, Name: "పిన్ని"},
	{ID: 10, Name: "Muthyalu", Place: "Chityal"},
	{ID: 11, Name: "భూతం ముత్యాలు - విమల"},
	{ID: 12, Name: "K.V. Ramana Reddy"},
	{ID: 13, Name: "విమల అత్త"},
	{ID: 14, Name: "Puram Nagaiah"},
	{ID: 15, Name: "బాబాయి"},
	{ID: 16, Name: "Chamala Jyothi"},
	{ID: 17, Name: "Narsimha", Place: "Nalgonda"},
	{ID: 18, Name: "Muthyalu", Place: "Chityal"},
}

var bandaru = Family{Father: "Bandaru Subba Rao", Mother: "Butham Lakshmi"}

type want struct {
	id int64
	s  Strength
}

func check(t *testing.T, label string, hs []Hit, ws []want) {
	t.Helper()
	var got []want
	for _, h := range hs {
		got = append(got, want{h.Entry.ID, h.Strength})
	}
	if !reflect.DeepEqual(got, ws) {
		t.Errorf("%s:\n got %v\nwant %v", label, got, ws)
		for _, h := range hs {
			t.Logf("  %d %q shared=%q why=%q", h.Entry.ID, h.Entry.Name, h.Shared, h.Why)
		}
	}
}

// The real card prints the host in English only. Given name first (row 1) and
// surname first (row 11) are both him; "Muthyalu, Chityal" is only asked about.
func TestEnglishCardFindsTeluguLines(t *testing.T) {
	m := Matcher{Family: bandaru}
	hs, _ := m.Find(Person{Names: []string{"Butham Muthyalu"}, Surname: "Butham"}, notebook, 10)
	check(t, "Butham Muthyalu", hs, []want{{1, Strong}, {11, Strong}, {10, Weak}, {18, Weak}})

	// His wife, named alone with the surname Butham: row 11 by both names, row
	// 13 "విమల అత్త" by name and by relation (అత్త can be mama's wife, and
	// Butham is the mother's maiden name). Row 1 is her husband alone.
	hs, _ = m.Find(Person{Names: []string{"Vimala"}, Surname: "Butham"}, notebook, 10)
	check(t, "Vimala", hs, []want{{11, Strong}, {13, Weak}})
	if hs[1].Why == "" || hs[1].Relation == nil {
		t.Errorf("relation not explained: %+v", hs[1])
	}
}

func TestInitials(t *testing.T) {
	m := Matcher{}
	hs, _ := m.Find(Person{Names: []string{"Kothapalli Venkata Ramana Reddy"}, Place: "Miryalaguda"}, notebook, 10)
	check(t, "K.V. Ramana Reddy", hs, []want{{2, Strong}, {12, Strong}})

	// The initial B. fits Butham, so "B. Narsimha" is asked about; it must not
	// be stated, and a different surname's initial rules it out.
	hs, _ = m.Find(Person{Names: []string{"Butham Narsimha"}}, notebook, 10)
	check(t, "Butham Narsimha", hs, []want{{5, Weak}, {17, Weak}})
	hs, _ = m.Find(Person{Names: []string{"Gaddam Narsimha"}}, notebook, 10)
	check(t, "Gaddam Narsimha", hs, []want{{17, Weak}})
}

// A Telugu-only card finds English lines too.
func TestTeluguCardFindsEnglishLines(t *testing.T) {
	hs, _ := Matcher{}.Find(Person{Names: []string{"", "పురం నాగయ్య"}}, notebook, 10)
	check(t, "పురం నాగయ్య", hs, []want{{4, Strong}, {14, Strong}})
}

func TestRelationThroughFamilyTree(t *testing.T) {
	ramulu := Person{Names: []string{"Bandaru Ramulu"}, Surname: "Bandaru"}
	// Without the family tree, a bare relation word can't be tied to anyone.
	if hs, _ := (Matcher{}).Find(ramulu, notebook, 10); len(hs) != 0 {
		t.Errorf("no family tree, yet hits %v", hs)
	}
	// With it: Bandaru is the father's surname, so his brothers (and their
	// wives) are candidates, each asked about.
	m := Matcher{Family: bandaru}
	hs, _ := m.Find(ramulu, notebook, 10)
	check(t, "Bandaru Ramulu", hs, []want{{3, Weak}, {7, Weak}, {9, Weak}, {15, Weak}})

	// The family said: line 7 is him (బాబాయి), line 3 is not. Now every
	// బాబాయి line is his, line 7 is confirmed, and line 3 is hidden.
	m.Answers = Answers{PersonKey("Bandaru Ramulu"): {7: true, 3: false}}
	m.Family.Links = map[string]string{"బాబాయి": "Bandaru Ramulu"}
	hs, skipped := m.Find(ramulu, notebook, 10)
	check(t, "after answers", hs, []want{{7, Confirmed}, {15, Strong}, {9, Weak}})
	if skipped != 1 {
		t.Errorf("skipped %d, want 1", skipped)
	}
	// And బాబాయి no longer comes up for the other Bandaru brother.
	hs, _ = m.Find(Person{Names: []string{"Bandaru Venkaiah"}}, notebook, 10)
	check(t, "Bandaru Venkaiah", hs, []want{{3, Weak}, {9, Weak}})
}

// Answers are remembered per person whatever the script or word order.
func TestPersonKey(t *testing.T) {
	if PersonKey("Butham Muthyalu") != PersonKey("ముత్యాలు భూతం") || PersonKey("Sri Butham Muthyalu garu") != PersonKey("Muthyalu Butham") {
		t.Errorf("keys differ: %q %q", PersonKey("Butham Muthyalu"), PersonKey("ముత్యాలు భూతం"))
	}
}

func TestFindNothing(t *testing.T) {
	if hs, _ := (Matcher{Family: bandaru}).Find(Person{Names: []string{"Sri Jayaprakash garu"}}, notebook, 5); len(hs) != 0 {
		t.Errorf("unexpected hits %v", hs)
	}
}

func TestTypedRelation(t *testing.T) {
	hs, _ := Matcher{}.Find(Person{Names: []string{"babai"}}, notebook, 10)
	check(t, "babai", hs, []want{{7, Strong}, {15, Strong}})
	hs, _ = Matcher{}.Find(Person{Names: []string{"Vimala atha"}}, notebook, 10)
	check(t, "Vimala atha", hs, []want{{13, Strong}, {11, Weak}})
}

// The reader left the wife out of the name field; the line's text still has her.
func TestNameMissingFromNameField(t *testing.T) {
	es := []ledger.Entry{{ID: 1, Name: "భూతం ముత్యాలు", Text: "భూతం ముత్యాలు - విమల - Rs 1,116/-"}}
	hs, _ := Matcher{}.Find(Person{Names: []string{"Vimala"}}, es, 5)
	check(t, "Vimala", hs, []want{{1, Weak}})
}
