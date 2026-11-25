package match

import (
	"reflect"
	"testing"

	"github.com/sivaratrisrinivas/giveback/internal/ledger"
)

func TestWords(t *testing.T) {
	got := Words("Sri K. Ramana Reddy garu, Miryalaguda")
	want := []string{"ramana", "reddy", "miryalaguda"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
	// Telugu vowel signs are marks, not punctuation: the word must stay whole.
	if got := Words("భూతం ముత్యాలు - విమల"); !reflect.DeepEqual(got, []string{"భూతం", "ముత్యాలు", "విమల"}) {
		t.Errorf("telugu: %q", got)
	}
}

// Rows from the answer key (docs/synthetic-notebook.md), as written.
var notebook = []ledger.Entry{
	{ID: 1, Name: "ముత్యాలు భూతం"},
	{ID: 2, Name: "K. Ramana Reddy", Place: "Miryalaguda"},
	{ID: 5, Name: "B. Narsimha", Place: "Nalgonda"},
	{ID: 10, Name: "Muthyalu", Place: "Chityal"},
	{ID: 11, Name: "భూతం ముత్యాలు - విమల"},
	{ID: 13, Name: "K.V. Ramana Reddy"},
}

func ids(hs []Hit) []int64 {
	var out []int64
	for _, h := range hs {
		out = append(out, h.Entry.ID)
	}
	return out
}

func TestFindCardHost(t *testing.T) {
	// The card prints the host in both scripts, surname first.
	hs := Find(Person{Names: []string{"Butham Muthyalu", "భూతం ముత్యాలు"}}, notebook, 5)
	if got := ids(hs); !reflect.DeepEqual(got, []int64{1, 11, 10}) {
		t.Fatalf("hits %v, want [1 11 10]", got)
	}
	// Same person written given-name first is still a strong match;
	// "Muthyalu, Chityal" shares only the given name, so it must be asked about.
	if hs[0].Strength != Strong || hs[1].Strength != Strong || hs[2].Strength != Weak {
		t.Errorf("strengths %v %v %v", hs[0].Strength, hs[1].Strength, hs[2].Strength)
	}
}

func TestFindPrefersSamePlace(t *testing.T) {
	hs := Find(Person{Names: []string{"Kothapalli Venkata Ramana Reddy"}, Place: "Miryalaguda"}, notebook, 5)
	if got := ids(hs); !reflect.DeepEqual(got, []int64{2, 13}) {
		t.Errorf("hits %v, want [2 13]", got)
	}
}

func TestFindNothing(t *testing.T) {
	if hs := Find(Person{Names: []string{"Sri Jayaprakash garu"}}, notebook, 5); len(hs) != 0 {
		t.Errorf("unexpected hits %v", ids(hs))
	}
}
