// Package bot turns Telegram messages into Giveback's two jobs: adding
// notebook pages to the ledger, and answering a forwarded invitation card with
// what that family gave, shown with the handwritten line as proof.
package bot

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/draw"

	"github.com/sivaratrisrinivas/giveback/internal/card"
	"github.com/sivaratrisrinivas/giveback/internal/ledger"
	"github.com/sivaratrisrinivas/giveback/internal/match"
	"github.com/sivaratrisrinivas/giveback/internal/notebook"
	"github.com/sivaratrisrinivas/giveback/internal/telegram"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
	"github.com/sivaratrisrinivas/giveback/internal/worth"
)

// Messenger is what the bot needs from Telegram; tests use a fake.
type Messenger interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
	SendChatAction(ctx context.Context, chatID int64, action string) error
	SendText(ctx context.Context, chatID int64, text string, buttons []telegram.Button) error
	SendPhoto(ctx context.Context, chatID int64, path, caption string, buttons []telegram.Button) error
	AnswerCallback(ctx context.Context, id, text string) error
	ClearButtons(ctx context.Context, chatID, messageID int64) error
	Download(ctx context.Context, fileID string) ([]byte, error)
}

type Bot struct {
	Store *ledger.Store
	TG    Messenger
	Pages notebook.Models // read 1 and read 2 of a notebook page
	Cards vision.Asker    // reads invitation cards

	mu sync.Mutex // one message at a time: simple, and SQLite has one writer
}

const (
	modePages = "pages"
	modeFix   = "fix:" // + line ID: the next typed message is that line's gift
)

// maxSide is the longest photo side sent to a model. Bigger photos are slow to
// read and can pass the connector's 8 MB limit; this is plenty for handwriting.
const maxSide = 2000

const help = `Namaskaram! I keep your family's gift notebook.

• To add notebook pages: send /pages, then send each page as a photo (sending it as a File keeps it sharpest). Send /done when finished.
• When an invitation card arrives: forward the card's photo here. I'll show what that family gave, with the handwritten line.
• Or type a name to look it up (a relation word like babai works too).
• Cash comes with what it's worth today (official CPI-IW price index, source shown). Gold stays by weight, in sovereigns.
• If I'm unsure of an amount, I show the handwriting and ask you to confirm it.
• Tell me about the family once, so I can work out relation words like బాబాయి:
  /family father = Bandaru Subba Rao
  /family mother = Butham Lakshmi   (her maiden name, surname first)

I never decide what you should give, and when I'm not sure, I ask.`

// Handle answers one Telegram update. Errors are logged and told to the user
// where it helps; Handle itself only fails on a broken ledger.
func (b *Bot) Handle(ctx context.Context, u telegram.Update) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if first, err := b.Store.FirstDelivery(u.UpdateID); err != nil || !first {
		return err // a re-delivery of something already handled
	}
	if q := u.CallbackQuery; q != nil {
		return b.answer(ctx, q)
	}
	m := u.Message
	if m == nil {
		return nil
	}
	chat := m.Chat.ID
	text := strings.TrimSpace(m.Text)
	cmd := strings.ToLower(strings.SplitN(text, "@", 2)[0])

	switch {
	case cmd == "/start" || cmd == "/help":
		return b.say(ctx, chat, help)
	case cmd == "/pages":
		if err := b.Store.SetMode(chat, modePages); err != nil {
			return err
		}
		return b.say(ctx, chat, "Send the notebook pages now, one photo per page. Send /done when finished.")
	case cmd == "/done":
		if err := b.Store.SetMode(chat, ""); err != nil {
			return err
		}
		n, err := b.Store.Entries()
		if err != nil {
			return err
		}
		return b.say(ctx, chat, fmt.Sprintf("Done. The notebook now has %d lines. Forward an invitation card whenever one arrives.", len(n)))
	case cmd == "/family" || strings.HasPrefix(cmd, "/family "):
		return b.family(ctx, chat, strings.TrimSpace(text[len("/family"):]))
	}

	if fileID, ok := m.ImageFileID(); ok {
		mode, err := b.Store.Mode(chat)
		if err != nil {
			return err
		}
		data, err := b.TG.Download(ctx, fileID)
		if err != nil {
			log.Printf("chat %d: download: %v", chat, err)
			return b.say(ctx, chat, "Sorry, I couldn't fetch that photo from Telegram. Please send it again.")
		}
		isPage := mode == modePages || strings.Contains(strings.ToLower(m.Caption), "page")
		if isPage {
			return b.addPage(ctx, chat, data)
		}
		return b.answerCard(ctx, chat, data)
	}

	if text != "" && !strings.HasPrefix(text, "/") {
		mode, err := b.Store.Mode(chat)
		if err != nil {
			return err
		}
		if strings.HasPrefix(mode, modeFix) {
			return b.typedGift(ctx, chat, mode, text)
		}
		return b.lookup(ctx, chat, []match.Person{{Names: []string{text}}}, nil)
	}
	return b.say(ctx, chat, help)
}

func (b *Bot) addPage(ctx context.Context, chat int64, data []byte) error {
	hash := vision.Hash(data)
	var reread int64
	if p, ok, err := b.Store.PageByHash(hash); err != nil {
		return err
	} else if ok && p.Model != vision.FallbackModel {
		return b.say(ctx, chat, fmt.Sprintf("I already have this page: %s, %s (%d lines). Nothing to do.", p.Function, p.Date, p.Lines))
	} else if ok {
		// Read 1 was done by the fallback model, which reads Telugu far worse.
		// Sending the photo again asks for a better reading; if the primary
		// model is still down, this costs one Haiku call and changes little.
		log.Printf("chat %d: page %d was read by %s; reading it again", chat, p.ID, p.Model)
		b.say(ctx, chat, fmt.Sprintf("I have this page (%s, %s), but it was read by my backup reader. Reading it again…", p.Function, p.Date))
		reread = p.ID
	}
	img, err := forModel(data)
	if err != nil {
		return b.say(ctx, chat, "That doesn't look like a photo I can read. Please send a JPEG or PNG.")
	}
	if reread == 0 {
		b.say(ctx, chat, "Reading the page… this takes about a minute.")
	}
	b.TG.SendChatAction(ctx, chat, "typing")

	p, err := notebook.Read(ctx, b.Pages, img)
	if err != nil {
		log.Printf("chat %d: read page: %v", chat, err)
		return b.say(ctx, chat, "Sorry, I couldn't read that page just now. Please try again in a little while.")
	}
	logSkipped(chat, "page read 1", p.FirstRead)
	if reread != 0 {
		if err := b.Store.DeletePage(reread); err != nil {
			return err
		}
	}
	info, err := b.Store.AddPage(hash, p)
	if err != nil {
		return err
	}
	log.Printf("chat %d: page %d added: %d lines, %d flagged (read 1 %s %.1fs, read 2 %s %.1fs)", chat, info.ID,
		info.Lines, info.Flagged, p.FirstRead.Model, p.FirstRead.Elapsed.Seconds(), p.SecondRead.Model, p.SecondRead.Elapsed.Seconds())

	var sb strings.Builder
	fmt.Fprintf(&sb, "Added: %s, %s. %d lines.", orUnknown(p.Function), orUnknown(p.Date), len(p.Lines))
	if f := p.Flagged(); len(f) > 0 {
		fmt.Fprintf(&sb, "\n\n%d lines need a check by eye; I'll say so whenever I show them:", len(f))
		for _, l := range f {
			fmt.Fprintf(&sb, "\n• %s %s: %s", l.Number, l.Text, strings.Join(l.Flags, "; "))
		}
	}
	return b.say(ctx, chat, sb.String())
}

func (b *Bot) answerCard(ctx context.Context, chat int64, data []byte) error {
	img, err := forModel(data)
	if err != nil {
		return b.say(ctx, chat, "That doesn't look like a photo I can read. Please send a JPEG or PNG.")
	}
	b.say(ctx, chat, "Reading the card…")
	b.TG.SendChatAction(ctx, chat, "typing")
	inv, res, err := card.Read(ctx, b.Cards, img)
	if err != nil {
		log.Printf("chat %d: read card: %v", chat, err)
		return b.say(ctx, chat, "Sorry, I couldn't read that card just now. Please try again in a little while.")
	}
	logSkipped(chat, "card", res)
	log.Printf("chat %d: card read by %s in %.1fs (cached %v)", chat, res.Model, res.Elapsed.Seconds(), res.Cached)

	people := inv.Hosts()
	if len(people) == 0 { // no clear host: look up everyone except the couple
		for _, p := range inv.People {
			if p.Role != "groom" && p.Role != "bride" {
				people = append(people, p)
			}
		}
	}
	if len(people) == 0 {
		return b.say(ctx, chat, "I couldn't find who is inviting on this card. You can type their name to look it up.")
	}
	var who []string
	var persons []match.Person
	for _, p := range people {
		who = append(who, p.NameEnglish)
		persons = append(persons, match.Person{Names: []string{p.NameEnglish, p.NameTelugu}, Surname: p.Surname, Place: p.Place})
	}
	b.say(ctx, chat, fmt.Sprintf("Invitation: %s%s, from %s.", orUnknown(inv.FunctionType), onDate(inv.Date), strings.Join(who, ", ")))
	return b.lookup(ctx, chat, persons, who)
}

// matcher loads what the family has told us.
func (b *Bot) matcher() (match.Matcher, error) {
	fam, err := b.Store.Family()
	if err != nil {
		return match.Matcher{}, err
	}
	ans, err := b.Store.Answers()
	if err != nil {
		return match.Matcher{}, err
	}
	m := match.Matcher{Family: match.Family{Father: fam["father"], Mother: fam["mother"], Links: map[string]string{}}, Answers: ans}
	for r, n := range fam {
		if r != "father" && r != "mother" {
			m.Family.Links[r] = n
		}
	}
	return m, nil
}

// lookup finds and shows the notebook lines for these people. names are how
// to call them in the reply (nil for a typed search).
func (b *Bot) lookup(ctx context.Context, chat int64, persons []match.Person, names []string) error {
	entries, err := b.Store.Entries()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return b.say(ctx, chat, "The notebook is empty so far. Send /pages to add its pages first.")
	}
	m, err := b.matcher()
	if err != nil {
		return err
	}
	// One line may match several people on the card (husband and wife): show
	// it once, for whoever it matches best.
	type shown struct {
		hit match.Hit
		who string
	}
	var order []int64
	best := map[int64]shown{}
	hidden := 0
	for _, p := range persons {
		hits, skipped := m.Find(p, entries, 5)
		hidden += skipped
		for _, h := range hits {
			old, seen := best[h.Entry.ID]
			if !seen {
				order = append(order, h.Entry.ID)
			}
			if !seen || h.Strength > old.hit.Strength || (h.Strength == old.hit.Strength && len(h.Shared) > len(old.hit.Shared)) {
				best[h.Entry.ID] = shown{h, p.Display()}
			}
		}
	}
	if len(order) == 0 {
		who := "that name"
		if len(names) > 0 {
			who = strings.Join(names, ", ")
		}
		msg := fmt.Sprintf("I couldn't find %s in the notebook. They may be written differently (a relation word like మామ, or initials); you can type another spelling to look it up.", who)
		if hidden > 0 {
			msg += fmt.Sprintf(" (%d line(s) you told me aren't them are left out.)", hidden)
		}
		return b.say(ctx, chat, msg)
	}
	// Strongest first across all the people on the card.
	sort.SliceStable(order, func(i, j int) bool { return best[order[i]].hit.Strength > best[order[j]].hit.Strength })
	b.TG.SendChatAction(ctx, chat, "upload_photo")
	for _, id := range order {
		s := best[id]
		caption := Caption(s.hit, s.who)
		var buttons []telegram.Button
		if s.hit.Strength == match.Weak {
			relation := ""
			if s.hit.Relation != nil && len(s.hit.Shared) == 0 {
				relation = s.hit.Relation.Word // a bare relation word: a Yes also says who that word means
			}
			askID, err := b.Store.AddAsk(ledger.Ask{LineID: id, Person: s.who, Relation: relation})
			if err != nil {
				return err
			}
			buttons = []telegram.Button{
				{Text: "✅ Yes, it's them", Data: fmt.Sprintf("y:%d", askID)},
				{Text: "❌ No", Data: fmt.Sprintf("n:%d", askID)},
			}
		}
		if amount := GiftButtons(s.hit.Entry); amount != nil {
			if buttons != nil {
				buttons = append(buttons, telegram.NewRow)
			}
			buttons = append(buttons, amount...)
		}
		if s.hit.Entry.Crop == "" {
			b.TG.SendText(ctx, chat, caption+"\n(No picture of this line was kept.)", buttons)
			continue
		}
		if err := b.TG.SendPhoto(ctx, chat, s.hit.Entry.Crop, caption, buttons); err != nil {
			log.Printf("chat %d: send crop: %v", chat, err)
			b.TG.SendText(ctx, chat, caption, buttons)
		}
	}
	if hidden > 0 {
		b.say(ctx, chat, fmt.Sprintf("(%d line(s) you told me aren't them are left out.)", hidden))
	}
	return nil
}

// answer handles a tap on "Yes, it's them" / "No" and remembers it.
func (b *Bot) answer(ctx context.Context, q *telegram.CallbackQuery) error {
	var chat, msgID int64
	if q.Message != nil {
		chat, msgID = q.Message.Chat.ID, q.Message.MessageID
	}
	kind, idText, _ := strings.Cut(q.Data, ":")
	if kind == "c" {
		return b.confirmTap(ctx, q, chat, msgID, idText)
	}
	askID, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || (kind != "y" && kind != "n") {
		return b.TG.AnswerCallback(ctx, q.ID, "")
	}
	a, ok, err := b.Store.GetAsk(askID)
	if err != nil {
		return err
	}
	if !ok {
		b.TG.AnswerCallback(ctx, q.ID, "That question is out of date (the page was read again).")
		return nil
	}
	yes := kind == "y"
	if err := b.Store.SetAnswer(match.PersonKey(a.Person), a.LineID, yes); err != nil {
		return err
	}
	e, _, err := b.Store.Entry(a.LineID)
	if err != nil {
		return err
	}
	where := fmt.Sprintf("“%s” (%s, %s)", e.Text, orUnknown(e.Function), orUnknown(e.Date))
	var reply string
	if yes {
		reply = fmt.Sprintf("Noted: %s is %s. I'll remember.", where, a.Person)
		if a.Relation != "" {
			fam, err := b.Store.Family()
			if err != nil {
				return err
			}
			if old, ok := fam[a.Relation]; !ok || old == "" {
				if err := b.Store.SetFamily(a.Relation, a.Person); err != nil {
					return err
				}
				reply += fmt.Sprintf(" From now on %s in the notebook means %s (change it with /family %s = <name>).", a.Relation, a.Person, a.Relation)
			}
		}
	} else {
		reply = fmt.Sprintf("Noted: %s is not %s. I won't show it for them again.", where, a.Person)
	}
	log.Printf("chat %d: answer %s for line %d = %v", chat, a.Person, a.LineID, yes)
	b.TG.AnswerCallback(ctx, q.ID, "Noted")
	if msgID != 0 {
		b.TG.ClearButtons(ctx, chat, msgID)
	}
	return b.say(ctx, chat, reply)
}

// family shows or changes the family tree: "/family father = Bandaru Subba Rao".
func (b *Bot) family(ctx context.Context, chat int64, arg string) error {
	if arg == "" {
		fam, err := b.Store.Family()
		if err != nil {
			return err
		}
		var sb strings.Builder
		sb.WriteString("What I know about the family:")
		if len(fam) == 0 {
			sb.WriteString(" nothing yet.")
		}
		for _, r := range []string{"father", "mother"} {
			if n, ok := fam[r]; ok {
				fmt.Fprintf(&sb, "\n• %s: %s", r, n)
			}
		}
		var rels []string
		for r := range fam {
			if r != "father" && r != "mother" {
				rels = append(rels, r)
			}
		}
		sort.Strings(rels)
		for _, r := range rels {
			fmt.Fprintf(&sb, "\n• %s: %s", r, fam[r])
		}
		sb.WriteString("\n\nTo change it:\n/family father = Bandaru Subba Rao\n/family mother = Butham Lakshmi (maiden name, surname first)\n/family babai = Bandaru Ramulu\n/family babai =   (forgets it)")
		return b.say(ctx, chat, sb.String())
	}
	// Several lines in one message ("/family father = …" then "/family mother = …").
	var replies []string
	for _, line := range strings.Split(arg, "\n") {
		line = strings.TrimSpace(line)
		if low := strings.ToLower(line); strings.HasPrefix(low, "/family") {
			line = strings.TrimSpace(line[len("/family"):])
		}
		if line == "" {
			continue
		}
		r, err := b.familyLine(line)
		if err != nil {
			return err
		}
		replies = append(replies, r)
	}
	return b.say(ctx, chat, strings.Join(replies, "\n"))
}

// familyLine handles one "relation = name" and returns the reply line.
func (b *Bot) familyLine(arg string) (string, error) {
	rel, name, ok := strings.Cut(arg, "=")
	if !ok {
		return fmt.Sprintf("I didn't understand %q. Please write it like this: /family father = Bandaru Subba Rao", arg), nil
	}
	rel, name = strings.TrimSpace(rel), strings.TrimSpace(name)
	key := ""
	switch strings.ToLower(rel) {
	case "father", "nanna", "నాన్న":
		key = "father"
	case "mother", "amma", "అమ్మ":
		key = "mother"
	default:
		r, ok := match.RelationOf(strings.ReplaceAll(rel, " ", ""))
		if !ok {
			return fmt.Sprintf("I don't know the relation word %q. Try father, mother, or a word like mama, atha, babai, peddananna, pinni.", rel), nil
		}
		key = r.Word
	}
	if err := b.Store.SetFamily(key, name); err != nil {
		return "", err
	}
	if name == "" {
		return fmt.Sprintf("Forgotten: %s.", key), nil
	}
	return fmt.Sprintf("Noted: %s = %s.", key, name), nil
}

// Caption describes one notebook line under its handwritten picture. who is
// the person it may be.
func Caption(h match.Hit, who string) string {
	e := h.Entry
	var sb strings.Builder
	switch h.Strength {
	case match.Confirmed:
		fmt.Fprintf(&sb, "Found in the notebook (%s):\n", h.Why)
	case match.Strong:
		sb.WriteString("Found in the notebook")
		if h.Why != "" {
			fmt.Fprintf(&sb, " (%s)", h.Why)
		}
		sb.WriteString(":\n")
	default:
		fmt.Fprintf(&sb, "Is this %s? %s.\n", who, capitalize(h.Why))
	}
	fmt.Fprintf(&sb, "%s, %s", orUnknown(e.Function), orUnknown(e.Date))
	if e.Number != "" {
		fmt.Fprintf(&sb, ", line %s", e.Number)
	}
	fmt.Fprintf(&sb, "\n“%s”\nGave: %s", e.Text, Gift(e))
	if e.Checked {
		sb.WriteString(" (you checked this)")
	}
	if len(e.Flags) > 0 {
		fmt.Fprintf(&sb, "\n⚠️ Please check the handwriting: %s", strings.Join(e.Flags, "; "))
	}
	if t := TodayValue(e); t != "" {
		sb.WriteString("\n" + t)
	}
	return sb.String()
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// Gift says what a line records: cash, gold by weight, or an item.
func Gift(e ledger.Entry) string {
	var parts []string
	if e.AmountDigits != "" {
		parts = append(parts, "₹"+groupIndian(e.AmountDigits))
	}
	if e.Gold > 0 {
		unit := "sovereigns"
		if e.Gold == 1 {
			unit = "sovereign"
		}
		parts = append(parts, fmt.Sprintf("%g %s of gold (%g g)", e.Gold, unit, e.Gold*gramsPerSovereign))
	}
	if e.Item != "" {
		parts = append(parts, e.Item)
	}
	if len(parts) == 0 {
		return "not written"
	}
	return strings.Join(parts, " + ")
}

// groupIndian writes 1116 as 1,116 and 100000 as 1,00,000.
func groupIndian(d string) string {
	if len(d) <= 3 {
		return d
	}
	head, tail := d[:len(d)-3], d[len(d)-3:]
	var groups []string
	for len(head) > 2 {
		groups = append([]string{head[len(head)-2:]}, groups...)
		head = head[:len(head)-2]
	}
	groups = append([]string{head}, groups...)
	return strings.Join(groups, ",") + "," + tail
}

func (b *Bot) say(ctx context.Context, chat int64, text string) error {
	if err := b.TG.SendMessage(ctx, chat, text); err != nil {
		log.Printf("chat %d: send: %v", chat, err)
	}
	return nil // a failed reply is logged, not retried: Telegram would re-deliver the whole update
}

// forModel prepares a photo for a model: as sent if small enough, otherwise
// shrunk so its longest side is maxSide.
func forModel(data []byte) (vision.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return vision.Image{}, err
	}
	mime := http.DetectContentType(data)
	if max(cfg.Width, cfg.Height) <= maxSide && len(data) < 4<<20 {
		return vision.Image{MIME: mime, Data: data}, nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return vision.Image{}, err
	}
	w, h := cfg.Width, cfg.Height
	if w >= h {
		w, h = maxSide, h*maxSide/w
	} else {
		w, h = w*maxSide/h, maxSide
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	jpg, err := notebook.EncodeJPEG(dst)
	if err != nil {
		return vision.Image{}, err
	}
	return vision.Image{MIME: "image/jpeg", Data: jpg}, nil
}

// logSkipped records why the primary model was not used, if it wasn't.
func logSkipped(chat int64, what string, r vision.Result) {
	if r.PrimaryErr != nil {
		log.Printf("chat %d: %s: %s skipped: %v", chat, what, vision.PrimaryModel, r.PrimaryErr)
	}
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(not written)"
	}
	return s
}

func onDate(d string) string {
	if d == "" {
		return ""
	}
	return " on " + d
}

// gramsPerSovereign: one sovereign (కాసు) of gold is 8 grams.
const gramsPerSovereign = 8

// TodayValue says what the cash on a line is worth today, with its source.
// Gold is never turned into rupees, and a flagged amount gets no value until
// the family confirms it: a misread 116 for 1,116 must not be multiplied.
func TodayValue(e ledger.Entry) string {
	var lines []string
	if e.AmountDigits != "" {
		n, err := strconv.Atoi(e.AmountDigits)
		switch {
		case len(e.Flags) > 0:
			lines = append(lines, "Today's value: I'll work it out once you confirm the amount (buttons below).")
		case err != nil || n == 0:
		default:
			if v, ok := worth.Today(n, e.Date); ok {
				lines = append(lines, valueLine(v))
			} else {
				lines = append(lines, fmt.Sprintf("I can't say what that's worth today: the page needs a clear year between %d and %d, the years my official price table covers.", worth.FirstYear, worth.LastYear+1))
			}
		}
	}
	if e.Gold > 0 {
		lines = append(lines, "Gold is shown by weight only, never turned into rupees.")
	}
	return strings.Join(lines, "\n")
}

func valueLine(v worth.Value) string {
	s := fmt.Sprintf("That's about ₹%s in today's money (prices are %.1f× those of %s).",
		groupIndian(strconv.Itoa(v.Today)), v.Factor, v.Year)
	if v.Guessed {
		s += fmt.Sprintf(" Only the year is written, so I used April–March %s.", v.Year)
	}
	return s + "\n" + v.Cite()
}

// GiftButtons offers the readings of a flagged gift to confirm with one tap,
// plus "something else" to type it. nil when the line needs no check.
func GiftButtons(e ledger.Entry) []telegram.Button {
	if len(e.Flags) == 0 || e.Checked {
		return nil
	}
	type reading struct {
		digits string
		gold   float64
	}
	choices := []reading{{e.AmountDigits, e.Gold}}
	disagree := false
	for _, f := range e.Flags {
		disagree = disagree || strings.Contains(f, ", then as ")
	}
	// An empty second reading is offered as no choice: ledgers from before
	// read 2 was stored have none, and "Something else" covers the rest.
	if second := (reading{e.SecondDigits, e.SecondGold}); disagree && second != choices[0] && second != (reading{}) {
		choices = append(choices, second)
	}
	var out []telegram.Button
	for _, c := range choices {
		label := Gift(ledger.Entry{AmountDigits: c.digits, Gold: c.gold})
		if label == "not written" {
			label = "no cash or gold"
		}
		label = strings.ReplaceAll(label, " of gold", "")
		if i := strings.Index(label, " ("); i >= 0 && strings.HasSuffix(label, " g)") {
			label = label[:i] // grams don't fit on a button
		}
		out = append(out, telegram.Button{Text: "✅ " + label, Data: fmt.Sprintf("c:%d:%s:%g", e.ID, c.digits, c.gold)})
	}
	return append(out, telegram.Button{Text: "✏️ Something else", Data: fmt.Sprintf("c:%d:x", e.ID)})
}

// confirmTap handles a tap on one of GiftButtons: "c:<line>:<digits>:<gold>"
// or "c:<line>:x" to type the gift instead.
func (b *Bot) confirmTap(ctx context.Context, q *telegram.CallbackQuery, chat, msgID int64, rest string) error {
	parts := strings.Split(rest, ":")
	lineID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || len(parts) < 2 {
		return b.TG.AnswerCallback(ctx, q.ID, "")
	}
	e, ok, err := b.Store.Entry(lineID)
	if err != nil {
		return err
	}
	if !ok {
		return b.TG.AnswerCallback(ctx, q.ID, "That line is out of date (the page was read again).")
	}
	if parts[1] == "x" {
		if err := b.Store.SetMode(chat, modeFix+parts[0]); err != nil {
			return err
		}
		b.TG.AnswerCallback(ctx, q.ID, "")
		return b.say(ctx, chat, fmt.Sprintf("Please look at the handwriting of “%s” and type what it says: the rupees in digits (like 1116), or gold like “2 sovereigns”. Type 0 if no cash is written, or “cancel”.", e.Text))
	}
	if len(parts) != 3 {
		return b.TG.AnswerCallback(ctx, q.ID, "")
	}
	gold, err := strconv.ParseFloat(parts[2], 64)
	if err != nil || !allDigits(parts[1]) {
		return b.TG.AnswerCallback(ctx, q.ID, "")
	}
	b.TG.AnswerCallback(ctx, q.ID, "Noted")
	if msgID != 0 {
		b.TG.ClearButtons(ctx, chat, msgID)
	}
	return b.confirm(ctx, chat, e, parts[1], gold)
}

// typedGift reads the gift the family typed after "Something else".
func (b *Bot) typedGift(ctx context.Context, chat int64, mode, text string) error {
	lineID, _ := strconv.ParseInt(strings.TrimPrefix(mode, modeFix), 10, 64)
	low := strings.ToLower(strings.TrimSpace(text))
	if low == "cancel" {
		if err := b.Store.SetMode(chat, ""); err != nil {
			return err
		}
		return b.say(ctx, chat, "OK, left as it was.")
	}
	e, ok, err := b.Store.Entry(lineID)
	if err != nil {
		return err
	}
	if !ok {
		b.Store.SetMode(chat, "")
		return b.say(ctx, chat, "That line is out of date (the page was read again). Look the name up again to see it.")
	}
	digits, gold, ok := parseGift(low, e)
	if !ok {
		return b.say(ctx, chat, "I didn't understand that. Type the rupees in digits (like 1116), or gold like “2 sovereigns”, or “cancel”.")
	}
	if err := b.Store.SetMode(chat, ""); err != nil {
		return err
	}
	return b.confirm(ctx, chat, e, digits, gold)
}

// parseGift understands "1116", "Rs 1,116/-", "₹516", "0", "2 sovereigns",
// "1.5 kasu", "2 కాసులు". Typing one kind keeps the line's other kind.
func parseGift(s string, e ledger.Entry) (digits string, gold float64, ok bool) {
	for _, w := range []string{"sovereigns", "sovereign", "sov", "kasulu", "kasu", "కాసులు", "కాసు"} {
		if i := strings.Index(s, w); i >= 0 {
			g, err := strconv.ParseFloat(strings.TrimSpace(s[:i]), 64)
			if err != nil || g < 0 || g > 1000 {
				return "", 0, false
			}
			return e.AmountDigits, g, true
		}
	}
	r := strings.NewReplacer("rs.", "", "rs", "", "₹", "", ",", "", "/-", "", "/", "", " ", "")
	s = r.Replace(s)
	if s == "" || !allDigits(s) || len(s) > 9 {
		return "", 0, false
	}
	s = strings.TrimLeft(s, "0")
	return s, e.Gold, true
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// confirm stores the family's reading of a gift and answers with today's value.
func (b *Bot) confirm(ctx context.Context, chat int64, e ledger.Entry, digits string, gold float64) error {
	if err := b.Store.ConfirmGift(e.ID, digits, gold); err != nil {
		return err
	}
	log.Printf("chat %d: gift on line %d confirmed as %q + %g sovereigns", chat, e.ID, digits, gold)
	e.AmountDigits, e.Gold, e.Flags, e.Checked = digits, gold, nil, true
	reply := fmt.Sprintf("Noted: “%s” (%s, %s) gave %s. I'll remember.", e.Text, orUnknown(e.Function), orUnknown(e.Date), Gift(e))
	if t := TodayValue(e); t != "" {
		reply += "\n" + t
	}
	return b.say(ctx, chat, reply)
}
