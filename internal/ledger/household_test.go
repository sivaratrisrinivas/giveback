package ledger

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sivaratrisrinivas/giveback/internal/notebook"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

// A copy for a new household carries the notebook and what the family taught
// it, but not the old chat bot's state, and both ledgers record the split.
func TestCopyForNewHousehold(t *testing.T) {
	src := t.TempDir()
	s, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := notebook.Page{Function: "పద్మ పెళ్ళి", Date: "12-2-1994", Lines: []notebook.Line{
		{Number: "2", Text: "K. Ramana Reddy, Miryalaguda — 116/-", Name: "K. Ramana Reddy", AmountDigits: "116", Crop: image.NewGray(image.Rect(0, 0, 40, 10))},
		{Number: "5", Text: "B. Narsimha — 1116/-", Name: "B. Narsimha", AmountDigits: "1116", Flags: []string{"amount read as ₹1,116, then as ₹116"}},
	}}
	if _, err := s.AddPage("hash1", p); err != nil {
		t.Fatal(err)
	}
	es, _ := s.Entries()
	must(t, s.SetFamily("father", "Bandaru Subba Rao"))
	must(t, s.SetAnswer("reddy ramana", es[0].ID, true))
	must(t, s.ConfirmGift(es[1].ID, "1116", 0))
	if ok, err := s.Claim(7); err != nil || !ok {
		t.Fatal("claim", ok, err)
	}
	must(t, s.Finish(7))
	must(t, s.SetMode(99, "pages"))
	must(t, s.RecordCalls(7, []vision.Call{{Model: "claude-haiku-5-5", OK: true, CostUSD: 0.003}}))
	must(t, os.MkdirAll(filepath.Join(src, "cache", "ab"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "cache", "ab", "x.json"), []byte(`{}`), 0o644))

	dst := filepath.Join(t.TempDir(), "ravi")
	c, err := s.CopyFor(dst, "Bandaru", "Ravi")
	if err != nil {
		t.Fatal(err)
	}
	if c.Pages != 1 || c.Lines != 2 || c.Name != "Ravi" {
		t.Fatalf("copy = %+v", c)
	}
	if _, err := s.CopyFor(dst, "", "Ravi2"); err == nil {
		t.Error("copied into a directory that already exists")
	}
	if _, err := s.CopyFor(filepath.Join(t.TempDir(), "x"), "", "Bandaru"); err == nil {
		t.Error("copied to a household with this household's own name")
	}

	n, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	got, _ := n.Entries()
	if len(got) != 2 || got[0].Text != es[0].Text || !got[1].Checked {
		t.Fatalf("copied entries = %+v", got)
	}
	if _, err := os.Stat(got[0].Crop); err != nil || filepath.Dir(filepath.Dir(filepath.Dir(got[0].Crop))) != dst {
		t.Errorf("line picture not in the copy: %s: %v", got[0].Crop, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "cache", "ab", "x.json")); err != nil {
		t.Errorf("model-answer cache not copied: %v", err)
	}
	if f, _ := n.Family(); f["father"] != "Bandaru Subba Rao" {
		t.Errorf("family tree = %v", f)
	}
	if a, _ := n.Answers(); !a["reddy ramana"][es[0].ID] {
		t.Errorf("answers = %v", a)
	}
	// The new bot's update 7 is a different message from the old bot's update 7.
	if ok, err := n.Claim(7); err != nil || !ok {
		t.Errorf("new household's update 7 was dropped as seen: %v %v", ok, err)
	}
	if m, _ := n.Mode(99); m != "" {
		t.Errorf("chat mode carried over: %q", m)
	}
	if u, _ := n.Usage(time.Time{}); len(u) != 0 {
		t.Errorf("old cost log carried over: %+v", u)
	}
	h, err := n.Household()
	if err != nil || h.Name != "Ravi" || h.CopiedFrom != "Bandaru" || h.Pages != 1 || h.Lines != 2 || h.CopiedAt.IsZero() || len(h.Copies) != 0 {
		t.Errorf("new household = %+v, %v", h, err)
	}

	// The old notebook knows its name now and that a copy was made, and keeps its own state.
	h, err = s.Household()
	if err != nil || h.Name != "Bandaru" || h.CopiedFrom != "" || len(h.Copies) != 1 || h.Copies[0].Name != "Ravi" {
		t.Errorf("old household = %+v, %v", h, err)
	}
	if ok, _ := s.Claim(7); ok {
		t.Error("old household forgot update 7")
	}
	// A second copy for the same household replaces the record of the first.
	if _, err := s.CopyFor(filepath.Join(t.TempDir(), "again"), "", "Ravi"); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.Household(); len(h.Copies) != 1 {
		t.Errorf("copies after copying for Ravi twice = %+v", h.Copies)
	}
	// The two grow apart: a later page added to one is not in the other.
	if _, err := n.AddPage("hash2", notebook.Page{Function: "x", Lines: p.Lines[:1]}); err != nil {
		t.Fatal(err)
	}
	if es, _ := s.Entries(); len(es) != 2 {
		t.Errorf("old notebook has %d lines, want 2", len(es))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
