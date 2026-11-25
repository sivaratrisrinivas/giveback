package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"strings"
	"testing"

	"github.com/sivaratrisrinivas/giveback/internal/ledger"
	"github.com/sivaratrisrinivas/giveback/internal/match"
	"github.com/sivaratrisrinivas/giveback/internal/notebook"
	"github.com/sivaratrisrinivas/giveback/internal/telegram"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

// fakeTG records what the bot sends and serves "downloads" from a map.
type fakeTG struct {
	files  map[string][]byte
	texts  []string
	photos []string // captions
}

func (f *fakeTG) SendMessage(_ context.Context, _ int64, text string) error {
	f.texts = append(f.texts, text)
	return nil
}
func (f *fakeTG) SendChatAction(context.Context, int64, string) error { return nil }
func (f *fakeTG) SendPhoto(_ context.Context, _ int64, path, caption string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	f.photos = append(f.photos, caption)
	return nil
}
func (f *fakeTG) Download(_ context.Context, id string) ([]byte, error) {
	if b, ok := f.files[id]; ok {
		return b, nil
	}
	return nil, errors.New("no such file")
}

// replay answers read 1 with the saved answer for the photo it is given, and
// read 2 by agreeing with read 1. It counts calls so tests can prove a repeat
// photo costs nothing.
type replay struct {
	byPhoto map[string]string // photo hash -> saved read-1 JSON
	last    []json.RawMessage
	calls   int
}

func (r *replay) page() vision.Asker    { return askFunc(r.read1) }
func (r *replay) amounts() vision.Asker { return askFunc(r.read2) }

func (r *replay) read1(_ context.Context, _ string, _ map[string]any, imgs ...vision.Image) (vision.Result, error) {
	r.calls++
	a, ok := r.byPhoto[vision.Hash(imgs[0].Data)]
	if !ok {
		return vision.Result{}, fmt.Errorf("no saved answer for this photo")
	}
	var p struct {
		Lines []json.RawMessage `json:"lines"`
	}
	json.Unmarshal([]byte(a), &p)
	r.last = p.Lines
	return vision.Result{JSON: json.RawMessage(a), Model: "replay"}, nil
}

func (r *replay) read2(_ context.Context, _ string, _ map[string]any, imgs ...vision.Image) (vision.Result, error) {
	r.calls++
	var out []map[string]any
	for _, raw := range r.last[:len(imgs)] {
		var l notebook.Line
		json.Unmarshal(raw, &l)
		out = append(out, map[string]any{"digits_seen": l.AmountDigits, "digit_count": len(l.AmountDigits), "gold_sovereigns": l.GoldSovereigns})
	}
	b, _ := json.Marshal(map[string]any{"lines": out})
	return vision.Result{JSON: b, Model: "replay"}, nil
}

type askFunc func(context.Context, string, map[string]any, ...vision.Image) (vision.Result, error)

func (f askFunc) ReadJSON(ctx context.Context, p string, s map[string]any, imgs ...vision.Image) (vision.Result, error) {
	return f(ctx, p, s, imgs...)
}

func msg(id int64, m telegram.Message) telegram.Update {
	m.Chat.ID = 7
	return telegram.Update{UpdateID: id, Message: &m}
}

func photoMsg(fileID string) telegram.Message {
	return telegram.Message{Photo: []telegram.PhotoSize{{FileID: "thumb"}, {FileID: fileID}}}
}

// The whole thin slice: add two pages, forward a card, get the handwritten
// lines back. No model is called: pages replay saved answers, the card is faked.
func TestThinSlice(t *testing.T) {
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
	}
	// A real 1×1 JPEG so the card passes decoding.
	tg.files["card"] = tinyJPEG(t)
	cardCalls := 0
	cardAsker := askFunc(func(context.Context, string, map[string]any, ...vision.Image) (vision.Result, error) {
		cardCalls++
		return vision.Result{Model: "fake", JSON: json.RawMessage(`{"function_type":"wedding","date":"2026-12-10","venue":"","town":"Nalgonda","languages":["te","en"],
			"people":[{"name_english":"Kothapalli Venkata Ramana Reddy","name_telugu":"కొత్తపల్లి వెంకట రమణ రెడ్డి","surname":"Kothapalli","role":"host","deceased":false,"place":"Miryalaguda"},
			          {"name_english":"Sai Kiran","name_telugu":"","surname":"","role":"groom","deceased":false,"place":""}]}`)}, nil
	})

	store, err := ledger.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	b := &Bot{Store: store, TG: tg, Pages: notebook.Models{Page: rp.page(), Amounts: rp.amounts()}, Cards: cardAsker}
	ctx := context.Background()
	send := func(u telegram.Update) {
		t.Helper()
		if err := b.Handle(ctx, u); err != nil {
			t.Fatal(err)
		}
	}

	send(msg(1, telegram.Message{Text: "/pages"}))
	send(msg(2, photoMsg("page1")))
	send(msg(3, photoMsg("page2")))
	if rp.calls != 4 {
		t.Fatalf("model calls for two pages: %d, want 4", rp.calls)
	}
	if last := tg.texts[len(tg.texts)-1]; !strings.Contains(last, "Added: గృహప్రవేశం, 8-6-2003. 8 lines.") {
		t.Errorf("page reply: %q", last)
	}

	// The same photo again, and Telegram re-delivering an update: no model calls.
	send(msg(4, photoMsg("page1")))
	send(msg(3, photoMsg("page2")))
	if rp.calls != 4 {
		t.Errorf("repeat photo or re-delivery reached the model: %d calls", rp.calls)
	}
	if !strings.Contains(tg.texts[len(tg.texts)-1], "I already have this page") {
		t.Errorf("repeat reply: %q", tg.texts[len(tg.texts)-1])
	}
	send(msg(5, telegram.Message{Text: "/done"}))
	if !strings.Contains(tg.texts[len(tg.texts)-1], "18 lines") {
		t.Errorf("done reply: %q", tg.texts[len(tg.texts)-1])
	}

	// Forward a card: both of his lines come back as pictures, with amount and function.
	send(msg(6, photoMsg("card")))
	if cardCalls != 1 {
		t.Errorf("card model calls %d", cardCalls)
	}
	if len(tg.photos) != 2 {
		t.Fatalf("sent %d line pictures, want 2: %q", len(tg.photos), tg.photos)
	}
	for i, want := range []string{"12-2-1994, line 2\n“K Ramana Reddy, Miryalaguda - 116/-”\nGave: ₹116", "8-6-2003, line 2"} {
		if !strings.Contains(tg.photos[i], "Found in the notebook") || !strings.Contains(tg.photos[i], want) {
			t.Errorf("photo %d caption %q; want %q", i, tg.photos[i], want)
		}
	}

	// A typed name that shares only one word is asked about, not stated.
	tg.photos = nil
	send(msg(7, telegram.Message{Text: "Muthyalu"}))
	if len(tg.photos) != 2 {
		t.Fatalf("typed lookup sent %d pictures, want 2", len(tg.photos))
	}
	for _, c := range tg.photos {
		if !strings.HasPrefix(c, "Is this them?") {
			t.Errorf("one shared word must be asked about: %q", c)
		}
	}
}

func hit(shared string, e ledger.Entry) match.Hit {
	return match.Hit{Entry: e, Shared: []string{shared}, Strength: match.Weak}
}

func tinyJPEG(t *testing.T) []byte {
	b, err := notebook.EncodeJPEG(image.NewRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCaptionFlagsAndWeakMatch(t *testing.T) {
	c := Caption(hit("muthyalu", ledger.Entry{Function: "గృహప్రవేశం", Date: "8-6-2003", Number: "8", Text: "Muthyalu, Chityal - 101/-",
		AmountDigits: "101", Flags: []string{"amount read as ₹101, then as ₹10"}}))
	for _, want := range []string{"Is this them?", "Gave: ₹101", "⚠️ Please check the handwriting: amount read as ₹101, then as ₹10"} {
		if !strings.Contains(c, want) {
			t.Errorf("caption %q lacks %q", c, want)
		}
	}
}

func TestGift(t *testing.T) {
	for _, c := range []struct {
		e    ledger.Entry
		want string
	}{
		{ledger.Entry{AmountDigits: "1116"}, "₹1,116"},
		{ledger.Entry{AmountDigits: "100000"}, "₹1,00,000"},
		{ledger.Entry{Gold: 1, Item: "ఉంగరం"}, "1 sovereign of gold + ఉంగరం"},
		{ledger.Entry{Gold: 2}, "2 sovereigns of gold"},
		{ledger.Entry{}, "not written"},
	} {
		if got := Gift(c.e); got != c.want {
			t.Errorf("Gift(%+v) = %q, want %q", c.e, got, c.want)
		}
	}
}
