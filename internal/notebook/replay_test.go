package notebook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

// These tests replay answers saved from real model runs (testdata/replay/*.read1.json,
// run 4: Haiku read 1) so the page pipeline is tested without spending any
// model quota. They need the handwritten photos in testdata/notebook/, which
// are kept out of git, and are skipped without them.

// replayPage answers read 1 from saved JSON, one answer per call, in order.
type replayPage struct {
	answers []string
	calls   int
}

func (f *replayPage) ReadJSON(_ context.Context, prompt string, _ map[string]any, imgs ...vision.Image) (vision.Result, error) {
	if prompt != pagePrompt || len(imgs) != 1 {
		return vision.Result{}, fmt.Errorf("unexpected read-1 call: %d images", len(imgs))
	}
	if f.calls >= len(f.answers) {
		return vision.Result{}, fmt.Errorf("read 1 called %d times; only %d answers saved", f.calls+1, len(f.answers))
	}
	a := f.answers[f.calls]
	f.calls++
	return vision.Result{JSON: json.RawMessage(a), Model: "replay"}, nil
}

// agreeAmounts answers read 2 by repeating read 1's amounts, except for the
// lines (by index into the crops) listed in override.
type agreeAmounts struct {
	lines    []Line
	override map[int]string
	calls    int
}

func (f *agreeAmounts) ReadJSON(_ context.Context, prompt string, _ map[string]any, imgs ...vision.Image) (vision.Result, error) {
	f.calls++
	if prompt != amountPrompt {
		return vision.Result{}, fmt.Errorf("unexpected read-2 prompt")
	}
	if len(imgs) != len(f.lines) {
		return vision.Result{}, fmt.Errorf("read 2 got %d crops for %d lines", len(imgs), len(f.lines))
	}
	type ans struct {
		DigitsSeen string  `json:"digits_seen"`
		DigitCount int     `json:"digit_count"`
		Gold       float64 `json:"gold_sovereigns"`
	}
	var out struct {
		Lines []ans `json:"lines"`
	}
	for i, l := range f.lines {
		d := l.AmountDigits
		if o, ok := f.override[i]; ok {
			d = o
		}
		out.Lines = append(out.Lines, ans{strings.Join(strings.Split(d, ""), " "), len(d), l.GoldSovereigns})
	}
	b, _ := json.Marshal(out)
	return vision.Result{JSON: b, Model: "replay"}, nil
}

func loadReplay(t *testing.T, name string) (vision.Image, string, []Line) {
	t.Helper()
	photo, err := os.ReadFile("../../testdata/notebook/" + name + ".jpeg")
	if err != nil {
		t.Skipf("photo not present (kept out of git): %v", err)
	}
	saved, err := os.ReadFile("../../testdata/replay/" + name + ".read1.json")
	if err != nil {
		t.Fatal(err)
	}
	var p Page
	if err := json.Unmarshal(saved, &p); err != nil {
		t.Fatal(err)
	}
	return vision.Image{MIME: http.DetectContentType(photo), Data: photo}, string(saved), p.Lines
}

func answerKey(t *testing.T) map[int]KeyPage {
	t.Helper()
	f, err := os.Open("../../docs/synthetic-notebook.md")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	key, err := ParseKey(f)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestReplayPages(t *testing.T) {
	key := answerKey(t)
	for _, tc := range []struct {
		photo   string
		keyPage int
		turn    int // a sideways photo: read 1 first reports this turn, then is re-asked upright
	}{
		{"page1", 1, 0},
		{"page1-rotated", 1, 270},
		{"page2", 2, 0},
	} {
		t.Run(tc.photo, func(t *testing.T) {
			photo, saved, lines := loadReplay(t, tc.photo)
			page := &replayPage{answers: []string{saved}}
			if tc.turn != 0 {
				page.answers = []string{fmt.Sprintf(`{"function":"","date":"","rotation":%d,"lines":[]}`, tc.turn), saved}
			}
			amounts := &agreeAmounts{lines: lines}
			p, err := Read(context.Background(), Models{Page: page, Amounts: amounts}, photo)
			if err != nil {
				t.Fatal(err)
			}
			if page.calls != len(page.answers) || amounts.calls != 1 {
				t.Errorf("model calls: read 1 %d (want %d), read 2 %d (want 1)", page.calls, len(page.answers), amounts.calls)
			}
			if p.Rotation != tc.turn {
				t.Errorf("rotation %d, want %d", p.Rotation, tc.turn)
			}
			for _, l := range p.Lines {
				if l.Crop == nil {
					t.Errorf("line %s: no crop: %v", l.Number, l.Flags)
				}
			}
			s := Score(p, key[tc.keyPage])
			if s.SilentBad != 0 {
				t.Errorf("%d wrong amounts not flagged", s.SilentBad)
			}
			// With read 2 agreeing, only lines the model itself marked unclear are flagged.
			for _, l := range p.Lines {
				if (len(l.Flags) > 0) != l.Unclear {
					t.Errorf("line %s: unclear=%v but flags %v", l.Number, l.Unclear, l.Flags)
				}
			}
		})
	}
}

// The worst error is ₹116 read for ₹1,116. When read 2 disagrees with read 1,
// the line must be flagged with both readings.
func TestReplayDisagreementFlagsLine(t *testing.T) {
	photo, saved, lines := loadReplay(t, "page1")
	i := -1
	for k, l := range lines {
		if l.AmountDigits == "1116" {
			i = k
		}
	}
	if i < 0 {
		t.Fatal("saved answer has no 1116 line")
	}
	amounts := &agreeAmounts{lines: lines, override: map[int]string{i: "116"}}
	p, err := Read(context.Background(), Models{Page: &replayPage{answers: []string{saved}}, Amounts: amounts}, photo)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(p.Lines[i].Flags, "; ")
	if !strings.Contains(got, "₹1116") || !strings.Contains(got, "₹116") {
		t.Errorf("line %s flags %q; want both readings", p.Lines[i].Number, got)
	}
}
