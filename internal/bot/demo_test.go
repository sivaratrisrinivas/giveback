package bot

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/sivaratrisrinivas/giveback/internal/ledger"
	"github.com/sivaratrisrinivas/giveback/internal/notebook"
	"github.com/sivaratrisrinivas/giveback/internal/telegram"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

// demoBot is a demo bot over a small seeded notebook: two బాబాయి lines, a
// flagged amount, and the made-up family's father and mother.
func demoBot(t *testing.T, cards vision.Asker) (*Bot, *fakeTG, *ledger.Store) {
	t.Helper()
	store, err := ledger.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	crop := image.NewRGBA(image.Rect(0, 0, 4, 4))
	_, err = store.AddPage("h1", notebook.Page{Function: "పద్మ పెళ్ళి", Date: "12-2-1994", Lines: []notebook.Line{
		{Number: "7", Text: "బాబాయి - Rs 216/-", Name: "బాబాయి", AmountDigits: "216", Crop: crop},
		{Number: "5", Text: "B. Narsimha, Nalgonda - 1116/-", Name: "B. Narsimha", Place: "Nalgonda", AmountDigits: "1116",
			SecondDigits: "116", Crop: crop, Flags: []string{"amount read as ₹1,116, then as ₹116"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddPage("h2", notebook.Page{Function: "గృహప్రవేశం", Date: "8-6-2003", Lines: []notebook.Line{
		{Number: "5", Text: "బాబాయి - 2 కాసులు గొలుసు", Name: "బాబాయి", GoldSovereigns: 2, Item: "గొలుసు", Crop: crop},
	}})
	if err != nil {
		t.Fatal(err)
	}
	store.SetFamily("father", "Bandaru Subba Rao")
	store.SetFamily("mother", "Butham Lakshmi")
	tg := &fakeTG{files: map[string][]byte{}}
	return &Bot{Store: store, TG: tg, Cards: cards, Demo: &Demo{CardsPerDay: 2, CardsPerChat: 1}}, tg, store
}

func inChat(chat, id int64, m telegram.Message) telegram.Update {
	m.Chat.ID = chat
	return telegram.Update{UpdateID: id, Message: &m}
}

func tapIn(chat, id int64, data string) telegram.Update {
	m := &telegram.Message{MessageID: 1}
	m.Chat.ID = chat
	return telegram.Update{UpdateID: id, CallbackQuery: &telegram.CallbackQuery{ID: "q", Data: data, Message: m}}
}

func TestDemoNotebookIsReadOnly(t *testing.T) {
	b, tg, store := demoBot(t, nil)
	ctx := context.Background()
	for i, text := range []string{"/pages", "/family father = Someone Else", "/family babai = Bandaru Ramulu"} {
		if err := b.Handle(ctx, inChat(1, int64(i+1), telegram.Message{Text: text})); err != nil {
			t.Fatal(err)
		}
		if last := tg.texts[len(tg.texts)-1]; last != demoReadOnly {
			t.Errorf("%s: %q", text, last)
		}
	}
	if mode, _ := store.Mode(1); mode != "" {
		t.Errorf("chat went into mode %q", mode)
	}
	fam, _ := store.Family()
	if fam["father"] != "Bandaru Subba Rao" || len(fam) != 2 {
		t.Errorf("family changed: %v", fam)
	}
	// /family alone still shows the tree; /start explains the demo.
	b.Handle(ctx, inChat(1, 10, telegram.Message{Text: "/family"}))
	if last := tg.texts[len(tg.texts)-1]; !strings.Contains(last, "father: Bandaru Subba Rao") {
		t.Errorf("/family: %q", last)
	}
	b.Handle(ctx, inChat(1, 11, telegram.Message{Text: "/start"}))
	if last := tg.texts[len(tg.texts)-1]; !strings.HasPrefix(last, "Namaskaram! This is the public demo") {
		t.Errorf("/start: %q", last)
	}
}

// One visitor's answers and confirmed amounts never reach another visitor.
func TestDemoAnswersStayInTheirChat(t *testing.T) {
	b, tg, store := demoBot(t, nil)
	ctx := context.Background()
	send := func(u telegram.Update) {
		t.Helper()
		if err := b.Handle(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	asks := func(chat, id int64) (ask []string) {
		t.Helper()
		tg.photos, tg.buttons = nil, nil
		send(inChat(chat, id, telegram.Message{Text: "Bandaru Ramulu"}))
		for i, c := range tg.photos {
			if strings.HasPrefix(c, "Is this Bandaru Ramulu?") {
				ask = append(ask, tg.buttons[i][0].Data) // the ✅ button
			}
		}
		return ask
	}

	// Visitor A says Yes to the first బాబాయి line: from then on both are known.
	a := asks(1, 1)
	if len(a) != 2 {
		t.Fatalf("visitor A: %d asks, want 2", len(a))
	}
	send(tapIn(1, 2, a[0]))
	if last := tg.texts[len(tg.texts)-1]; !strings.Contains(last, "From now on బాబాయి in the notebook means Bandaru Ramulu") {
		t.Errorf("yes reply: %q", last)
	}
	if again := asks(1, 3); len(again) != 0 {
		t.Errorf("visitor A is still asked %d time(s)", len(again))
	}
	// Visitor A confirms the flagged amount.
	tg.buttons = nil
	send(inChat(1, 4, telegram.Message{Text: "Butham Narsimha"}))
	confirm := ""
	for _, bt := range tg.buttons[0] {
		if strings.HasPrefix(bt.Text, "✅ ₹1,116") {
			confirm = bt.Data
		}
	}
	send(tapIn(1, 5, confirm))
	if last := tg.texts[len(tg.texts)-1]; !strings.Contains(last, "gave ₹1,116") {
		t.Errorf("confirm reply: %q", last)
	}

	// Visitor B gets the questions, and the amount is still to be checked.
	if bAsks := asks(2, 6); len(bAsks) != 2 {
		t.Errorf("visitor B: %d asks, want 2", len(bAsks))
	}
	tg.photos = nil
	send(inChat(2, 7, telegram.Message{Text: "Butham Narsimha"}))
	if len(tg.photos) != 1 || !strings.Contains(tg.photos[0], "once you confirm the amount") {
		t.Errorf("visitor B sees A's confirmation: %q", tg.photos)
	}
	// And the shared notebook is untouched.
	if ans, _ := store.Answers(); len(ans) != 0 {
		t.Errorf("shared answers: %v", ans)
	}
	if fam, _ := store.Family(); len(fam) != 2 {
		t.Errorf("shared family: %v", fam)
	}
	for _, e := range must(store.Entries()) {
		if e.Checked {
			t.Errorf("shared line %d marked checked", e.ID)
		}
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Card reads by a model are capped per chat and per day; a card read before
// is still answered from the cache, at no cost.
func TestDemoCardCap(t *testing.T) {
	calls := 0
	model := askFunc(func(context.Context, string, map[string]any, ...vision.Image) (vision.Result, error) {
		calls++
		return vision.Result{Model: "fake", JSON: json.RawMessage(`{"function_type":"wedding","date":"","venue":"","town":"","languages":["en"],
			"people":[{"name_english":"Nobody Known","name_telugu":"","surname":"Nobody","role":"host","deceased":false,"place":""}]}`)}, nil
	})
	b, tg, _ := demoBot(t, vision.Cached(model, t.TempDir(), "hydrogen-first"))
	for i, c := range []uint8{10, 20, 30} {
		tg.files[string(rune('a'+i))] = colourJPEG(t, c)
	}
	ctx := context.Background()
	resting := func() bool { return strings.Contains(tg.texts[len(tg.texts)-1], "card reader is resting") }
	steps := []struct {
		chat    int64
		file    string
		resting bool
		calls   int
	}{
		{1, "a", false, 1}, // A's one read for today
		{1, "b", true, 1},  // A is over the per-chat cap
		{1, "a", false, 1}, // but a card read before comes from the cache
		{2, "b", false, 2}, // B's read: the day's total is now 2
		{3, "c", true, 2},  // C: the daily cap is spent
		{3, "a", false, 2}, // the cached card still works for C
	}
	for i, s := range steps {
		if err := b.Handle(ctx, inChat(s.chat, int64(i+1), photoMsg(s.file))); err != nil {
			t.Fatal(err)
		}
		if resting() != s.resting || calls != s.calls {
			t.Errorf("step %d (chat %d, card %s): resting %v, calls %d; want %v, %d (last reply %q)",
				i, s.chat, s.file, resting(), calls, s.resting, s.calls, tg.texts[len(tg.texts)-1])
		}
	}
	// A photo captioned "page" is still a card in the demo.
	b.Handle(ctx, inChat(4, 20, telegram.Message{Caption: "page 3", Photo: []telegram.PhotoSize{{FileID: "a"}}}))
	if strings.Contains(strings.Join(tg.texts, "\n"), "Reading the page") {
		t.Error("the demo read a photo as a notebook page")
	}
}

func colourJPEG(t *testing.T, c uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{c, c, c, 255})
		}
	}
	b, err := notebook.EncodeJPEG(img)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
