package bot

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sivaratrisrinivas/giveback/internal/ledger"
	"github.com/sivaratrisrinivas/giveback/internal/notebook"
	"github.com/sivaratrisrinivas/giveback/internal/telegram"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
	"github.com/sivaratrisrinivas/giveback/internal/wake"
)

// A second photo of a page already read (different bytes, same gifts) is not
// added twice. If the first reading came from the backup reader, the new one
// replaces it.
func TestSecondPhotoOfSamePage(t *testing.T) {
	tg := &fakeTG{files: map[string][]byte{}}
	rp := &replay{byPhoto: map[string]string{}}
	for _, name := range []string{"page1", "page2"} {
		photo, err := os.ReadFile("../../testdata/notebook/" + name + ".jpeg")
		if err != nil {
			t.Skipf("photo not present (kept out of git): %v", err)
		}
		saved, err := os.ReadFile("../../testdata/replay/" + name + ".read1.json")
		if err != nil {
			t.Fatal(err)
		}
		tg.files[name] = photo
		rp.byPhoto[vision.Hash(photo)] = string(saved)
		if name == "page1" {
			// Two more "photos" of page 1: a trailing byte changes the hash
			// but not the picture.
			for _, v := range []string{"page1-again", "page1-third"} {
				other := append(append([]byte(nil), photo...), byte(len(v)))
				tg.files[v] = other
				rp.byPhoto[vision.Hash(other)] = string(saved)
			}
		}
	}
	store, err := ledger.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	b := &Bot{Store: store, TG: tg, Pages: notebook.Models{Page: rp.page(), Amounts: rp.amounts()}}
	ctx := context.Background()
	lines := func() int {
		es, err := store.Entries()
		if err != nil {
			t.Fatal(err)
		}
		return len(es)
	}
	last := func() string { return tg.texts[len(tg.texts)-1] }

	b.Handle(ctx, msg(1, telegram.Message{Text: "/pages"}))
	rp.model = vision.FallbackModel
	b.Handle(ctx, msg(2, photoMsg("page1")))
	if lines() != 10 {
		t.Fatalf("lines after page 1: %d", lines())
	}

	rp.model = ""
	b.Handle(ctx, msg(3, photoMsg("page1-again")))
	if lines() != 10 || !strings.Contains(strings.Join(tg.texts, "\n"), "Keeping this better reading") {
		t.Fatalf("better reading of the same page: %d lines, replies %q", lines(), tg.texts)
	}
	es, _ := store.Entries()
	if p, ok, _ := store.SamePage(notebook.Page{Lines: []notebook.Line{
		{Number: "1", AmountDigits: es[0].AmountDigits}, {Number: "2", AmountDigits: es[1].AmountDigits}, {Number: "4", AmountDigits: es[3].AmountDigits},
	}}); !ok || p.Model == vision.FallbackModel {
		t.Errorf("the kept page should be the primary reading: %+v %v", p, ok)
	}

	b.Handle(ctx, msg(4, photoMsg("page1-third")))
	if lines() != 10 || !strings.Contains(last(), "I already have this page") {
		t.Fatalf("third photo: %d lines, reply %q", lines(), last())
	}

	b.Handle(ctx, msg(5, photoMsg("page2"))) // a different page is still added
	if lines() != 18 {
		t.Fatalf("lines after page 2: %d", lines())
	}
}

// An update whose first try never finished (the process died mid-reply) is
// handled when Telegram re-delivers it; after that, re-deliveries are dropped.
func TestUnfinishedUpdateIsRetried(t *testing.T) {
	tg := &fakeTG{}
	store, err := ledger.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	b := &Bot{Store: store, TG: tg}
	if ok, _ := store.Claim(10); !ok { // the try that died
		t.Fatal("claim")
	}
	b.Handle(context.Background(), msg(10, telegram.Message{Text: "/help"}))
	b.Handle(context.Background(), msg(10, telegram.Message{Text: "/help"}))
	if len(tg.texts) != 1 {
		t.Errorf("replies: %d, want 1", len(tg.texts))
	}
}

func TestUsageCommand(t *testing.T) {
	tg := &fakeTG{}
	store, err := ledger.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.RecordCalls(3, []vision.Call{
		{Model: vision.PrimaryModel, Err: "gemini HTTP 503"},
		{Model: vision.FallbackModel, OK: true, Elapsed: 13 * time.Second, In: 2500, Out: 1500, CostUSD: 0.001},
	})
	b := &Bot{Store: store, TG: tg, Wakes: wake.New(time.Now())}
	b.Handle(context.Background(), msg(1, telegram.Message{Text: "/usage"}))
	got := tg.texts[0]
	for _, want := range []string{"Last 24 hours:", "claude-haiku-5-5: 1 call(s), 0 failed, tokens in 2500 out 1500, $0.0010, 13.0 s each",
		"gemini-4-hydrogen: 1 call(s), 1 failed", "Total $0.0010", "free tier", "This Sprite: process up"} {
		if !strings.Contains(got, want) {
			t.Errorf("usage reply lacks %q:\n%s", want, got)
		}
	}
}

// A card sent while /pages mode is on is not stored as an empty page, and a
// /pages mode left on for over 30 minutes no longer swallows cards.
func TestForgottenPagesMode(t *testing.T) {
	tg := &fakeTG{files: map[string][]byte{"card": tinyJPEG(t)}}
	pageAsker := askFunc(func(context.Context, string, map[string]any, ...vision.Image) (vision.Result, error) {
		return vision.Result{Model: "replay", JSON: []byte(`{"function":"Wedding Invitation","date":"03-04-2025","rotation":0,"lines":[]}`)}, nil
	})
	cardCalls := 0
	cardAsker := askFunc(func(context.Context, string, map[string]any, ...vision.Image) (vision.Result, error) {
		cardCalls++
		return vision.Result{Model: "fake", JSON: []byte(`{"function_type":"wedding","date":"","venue":"","town":"","languages":["en"],"people":[]}`)}, nil
	})
	store, err := ledger.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	b := &Bot{Store: store, TG: tg, Pages: notebook.Models{Page: pageAsker, Amounts: pageAsker}, Cards: cardAsker}
	ctx := context.Background()
	b.Handle(ctx, msg(1, telegram.Message{Text: "/pages"}))
	b.Handle(ctx, msg(2, photoMsg("card")))
	if es, _ := store.Entries(); len(es) != 0 || !strings.Contains(tg.texts[len(tg.texts)-1], "I found no gift lines") {
		t.Fatalf("empty page: %d lines, reply %q", len(es), tg.texts[len(tg.texts)-1])
	}
	if p, ok, _ := store.PageByHash(vision.Hash(tg.files["card"])); ok {
		t.Fatalf("an empty page was stored: %+v", p)
	}
	if cardCalls != 0 {
		t.Fatal("card read while in /pages mode")
	}

	defer func() { now = time.Now }()
	now = func() time.Time { return time.Now().Add(31 * time.Minute) }
	b.Handle(ctx, msg(3, photoMsg("card")))
	if cardCalls != 1 {
		t.Errorf("after 31 idle minutes the photo should be read as a card; card calls %d, replies %q", cardCalls, tg.texts)
	}
	if mode, _ := store.Mode(7); mode != "" {
		t.Errorf("mode after expiry: %q", mode)
	}
}
