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
	files   map[string][]byte
	texts   []string
	photos  []string            // captions
	buttons [][]telegram.Button // buttons sent with each photo, in order
	cleared int                 // messages whose buttons were removed
}

func (f *fakeTG) SendMessage(_ context.Context, _ int64, text string) error {
	f.texts = append(f.texts, text)
	return nil
}
func (f *fakeTG) SendText(_ context.Context, _ int64, text string, _ []telegram.Button) error {
	f.texts = append(f.texts, text)
	return nil
}
func (f *fakeTG) SendChatAction(context.Context, int64, string) error { return nil }
func (f *fakeTG) SendPhoto(_ context.Context, _ int64, path, caption string, buttons []telegram.Button) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	f.photos = append(f.photos, caption)
	f.buttons = append(f.buttons, buttons)
	return nil
}
func (f *fakeTG) AnswerCallback(context.Context, string, string) error { return nil }
func (f *fakeTG) ClearButtons(context.Context, int64, int64) error {
	f.cleared++
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
	model   string // who read 1 claims to be; "" means "replay"
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
	if r.model == vision.FallbackModel {
		return vision.Result{JSON: json.RawMessage(a), Model: r.model, PrimaryErr: errors.New("gemini HTTP 503")}, nil
	}
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

	if tg.buttons[0] != nil || tg.buttons[1] != nil {
		t.Errorf("strong matches must not ask: %v", tg.buttons)
	}

	// A typed name that shares only one word is asked about, with buttons.
	tg.photos, tg.buttons = nil, nil
	send(msg(7, telegram.Message{Text: "Muthyalu"}))
	if len(tg.photos) != 2 {
		t.Fatalf("typed lookup sent %d pictures, want 2", len(tg.photos))
	}
	for i, c := range tg.photos {
		if !strings.HasPrefix(c, "Is this Muthyalu?") || len(tg.buttons[i]) != 2 {
			t.Errorf("one shared word must be asked about: %q %v", c, tg.buttons[i])
		}
	}

	// Tap "No" on the first, "Yes" on the second: buttons go away, answers stick.
	tap := func(id int64, data string) {
		t.Helper()
		send(telegram.Update{UpdateID: id, CallbackQuery: &telegram.CallbackQuery{ID: "q", Data: data,
			Message: &telegram.Message{MessageID: 99, Chat: tg.chat()}}})
	}
	tap(8, tg.buttons[0][1].Data)
	if !strings.Contains(tg.texts[len(tg.texts)-1], "is not Muthyalu") {
		t.Errorf("no reply: %q", tg.texts[len(tg.texts)-1])
	}
	tap(9, tg.buttons[1][0].Data)
	tap(9, tg.buttons[1][0].Data) // Telegram re-delivers: handled once
	if tg.cleared != 2 {
		t.Errorf("buttons cleared on %d messages, want 2", tg.cleared)
	}
	tg.photos, tg.buttons = nil, nil
	send(msg(10, telegram.Message{Text: "Muthyalu"}))
	if len(tg.photos) != 1 || !strings.Contains(tg.photos[0], "you confirmed this is Muthyalu") || tg.buttons[0] != nil {
		t.Errorf("after answers: %q %v", tg.photos, tg.buttons)
	}
	if !strings.Contains(tg.texts[len(tg.texts)-1], "1 line(s) you told me aren't them") {
		t.Errorf("hidden line not mentioned: %q", tg.texts[len(tg.texts)-1])
	}
}

func (f *fakeTG) chat() (c struct {
	ID int64 `json:"id"`
}) {
	c.ID = 7
	return c
}

// A bare relation word is tied to a person through the family tree, and a
// Yes teaches the bot who the word means.
func TestRelationAskAndRemember(t *testing.T) {
	store, err := ledger.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.AddPage("h1", notebook.Page{Function: "పద్మ పెళ్ళి", Date: "12-2-1994", Lines: []notebook.Line{
		{Number: "3", Text: "పెద్దనాన్న - 1 కాసు ఉంగరం", Name: "పెద్దనాన్న", GoldSovereigns: 1, Item: "ఉంగరం"},
		{Number: "7", Text: "బాబాయి - Rs 216/-", Name: "బాబాయి", AmountDigits: "216"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddPage("h2", notebook.Page{Function: "గృహప్రవేశం", Date: "8-6-2003", Lines: []notebook.Line{
		{Number: "5", Text: "బాబాయి - 2 కాసులు గొలుసు", Name: "బాబాయి", GoldSovereigns: 2, Item: "గొలుసు"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tg := &fakeTG{}
	b := &Bot{Store: store, TG: tg}
	ctx := context.Background()
	send := func(u telegram.Update) {
		t.Helper()
		if err := b.Handle(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	last := func() string { return tg.texts[len(tg.texts)-1] }

	// No family tree yet: nothing to go on.
	send(msg(1, telegram.Message{Text: "Bandaru Ramulu"}))
	if !strings.Contains(last(), "couldn't find") {
		t.Fatalf("without a family tree: %q", last())
	}
	send(msg(2, telegram.Message{Text: "/family father = Bandaru Subba Rao"}))
	send(msg(3, telegram.Message{Text: "/family mother = Butham Lakshmi"}))
	if !strings.Contains(last(), "Noted: mother = Butham Lakshmi") {
		t.Fatalf("family reply: %q", last())
	}
	// The lines have no pictures here, so they come back as texts.
	tg.texts = nil
	send(msg(4, telegram.Message{Text: "Bandaru Ramulu"}))
	if len(tg.texts) != 3 {
		t.Fatalf("want 3 asks, got %q", tg.texts)
	}
	for _, c := range tg.texts {
		if !strings.HasPrefix(c, "Is this Bandaru Ramulu? The line says") || !strings.Contains(c, "Bandaru is your father's surname") {
			t.Errorf("ask: %q", c)
		}
	}
	// Yes to the first బాబాయి line (ask IDs follow the order shown: 3, 7, 5).
	asks := []int64{1, 2, 3}
	a, _, _ := store.GetAsk(asks[1])
	if a.Relation != "బాబాయి" {
		t.Fatalf("ask 2 is %+v", a)
	}
	send(telegram.Update{UpdateID: 5, CallbackQuery: &telegram.CallbackQuery{ID: "q", Data: "y:2"}})
	if !strings.Contains(last(), "From now on బాబాయి in the notebook means Bandaru Ramulu") {
		t.Errorf("yes reply: %q", last())
	}
	send(telegram.Update{UpdateID: 6, CallbackQuery: &telegram.CallbackQuery{ID: "q", Data: "n:1"}})

	tg.texts = nil
	send(msg(7, telegram.Message{Text: "Bandaru Ramulu"}))
	got := strings.Join(tg.texts, "\n---\n")
	for _, want := range []string{"Found in the notebook (you confirmed this is Bandaru Ramulu)", "Found in the notebook (బాబాయి is Bandaru Ramulu, as you told me)", "Gave: 2 sovereigns of gold + గొలుసు", "1 line(s) you told me aren't them"} {
		if !strings.Contains(got, want) {
			t.Errorf("after answers, missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Is this") {
		t.Errorf("still asking:\n%s", got)
	}
	send(msg(8, telegram.Message{Text: "/family"}))
	if !strings.Contains(last(), "• బాబాయి: Bandaru Ramulu") {
		t.Errorf("/family: %q", last())
	}
}

func hit(shared string, e ledger.Entry) match.Hit {
	return match.Hit{Entry: e, Shared: []string{shared}, Strength: match.Weak, Why: "only the name “" + shared + "” matches"}
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
		AmountDigits: "101", Flags: []string{"amount read as ₹101, then as ₹10"}}), "Butham Muthyalu")
	for _, want := range []string{"Is this Butham Muthyalu? Only the name “muthyalu” matches.", "Gave: ₹101", "⚠️ Please check the handwriting: amount read as ₹101, then as ₹10"} {
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

// A page first read by the fallback model is read again when its photo is
// sent again, and replaces the old reading instead of adding to it.
func TestFallbackPageIsReread(t *testing.T) {
	photo, err := os.ReadFile("../../testdata/notebook/page2.jpeg")
	if err != nil {
		t.Skipf("photo not present (kept out of git): %v", err)
	}
	saved, err := os.ReadFile("../../testdata/replay/page2.read1.json")
	if err != nil {
		t.Fatal(err)
	}
	rp := &replay{byPhoto: map[string]string{vision.Hash(photo): string(saved)}, model: vision.FallbackModel}
	tg := &fakeTG{files: map[string][]byte{"p": photo}}
	store, err := ledger.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	b := &Bot{Store: store, TG: tg, Pages: notebook.Models{Page: rp.page(), Amounts: rp.amounts()}}
	ctx := context.Background()
	b.Handle(ctx, msg(1, telegram.Message{Text: "/pages"}))
	b.Handle(ctx, msg(2, photoMsg("p")))
	rp.model = "" // the primary model is back
	b.Handle(ctx, msg(3, photoMsg("p")))
	if rp.calls != 4 {
		t.Errorf("model calls %d, want 4 (read twice)", rp.calls)
	}
	b.Handle(ctx, msg(4, photoMsg("p")))
	if rp.calls != 4 {
		t.Errorf("a primary-model page was read again: %d calls", rp.calls)
	}
	es, _ := store.Entries()
	if len(es) != 8 {
		t.Errorf("%d lines in the ledger, want 8 (old reading replaced)", len(es))
	}
}
