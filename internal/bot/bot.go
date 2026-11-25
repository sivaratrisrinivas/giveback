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
	"strings"
	"sync"

	"golang.org/x/image/draw"

	"github.com/sivaratrisrinivas/giveback/internal/card"
	"github.com/sivaratrisrinivas/giveback/internal/ledger"
	"github.com/sivaratrisrinivas/giveback/internal/match"
	"github.com/sivaratrisrinivas/giveback/internal/notebook"
	"github.com/sivaratrisrinivas/giveback/internal/telegram"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

// Messenger is what the bot needs from Telegram; tests use a fake.
type Messenger interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
	SendChatAction(ctx context.Context, chatID int64, action string) error
	SendPhoto(ctx context.Context, chatID int64, path, caption string) error
	Download(ctx context.Context, fileID string) ([]byte, error)
}

type Bot struct {
	Store *ledger.Store
	TG    Messenger
	Pages notebook.Models // read 1 and read 2 of a notebook page
	Cards vision.Asker    // reads invitation cards

	mu sync.Mutex // one message at a time: simple, and SQLite has one writer
}

const modePages = "pages"

// maxSide is the longest photo side sent to a model. Bigger photos are slow to
// read and can pass the connector's 8 MB limit; this is plenty for handwriting.
const maxSide = 2000

const help = `Namaskaram! I keep your family's gift notebook.

• To add notebook pages: send /pages, then send each page as a photo (sending it as a File keeps it sharpest). Send /done when finished.
• When an invitation card arrives: forward the card's photo here. I'll show what that family gave, with the handwritten line.
• Or type a name to look it up.

I never decide what you should give, and when I'm not sure, I ask.`

// Handle answers one Telegram update. Errors are logged and told to the user
// where it helps; Handle itself only fails on a broken ledger.
func (b *Bot) Handle(ctx context.Context, u telegram.Update) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if first, err := b.Store.FirstDelivery(u.UpdateID); err != nil || !first {
		return err // a re-delivery of something already handled
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
		persons = append(persons, match.Person{Names: []string{p.NameEnglish, p.NameTelugu}, Place: p.Place})
	}
	b.say(ctx, chat, fmt.Sprintf("Invitation: %s%s, from %s.", orUnknown(inv.FunctionType), onDate(inv.Date), strings.Join(who, ", ")))
	return b.lookup(ctx, chat, persons, who)
}

// lookup finds and shows the notebook lines for these people. names are how
// to call them in the reply ("" for a typed search).
func (b *Bot) lookup(ctx context.Context, chat int64, persons []match.Person, names []string) error {
	entries, err := b.Store.Entries()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return b.say(ctx, chat, "The notebook is empty so far. Send /pages to add its pages first.")
	}
	// One line may match several people on the card (husband and wife): show it once.
	var order []int64
	best := map[int64]match.Hit{}
	for _, p := range persons {
		for _, h := range match.Find(p, entries, 5) {
			old, seen := best[h.Entry.ID]
			if !seen {
				order = append(order, h.Entry.ID)
			}
			if !seen || len(h.Shared) > len(old.Shared) {
				best[h.Entry.ID] = h
			}
		}
	}
	if len(order) == 0 {
		who := "that name"
		if len(names) > 0 {
			who = strings.Join(names, ", ")
		}
		return b.say(ctx, chat, fmt.Sprintf("I couldn't find %s in the notebook. They may be written differently (a relation word like మామ, or initials); you can type another spelling to look it up.", who))
	}
	b.TG.SendChatAction(ctx, chat, "upload_photo")
	for _, id := range order {
		h := best[id]
		caption := Caption(h)
		if h.Entry.Crop == "" {
			if err := b.say(ctx, chat, caption+"\n(No picture of this line was kept.)"); err != nil {
				return err
			}
			continue
		}
		if err := b.TG.SendPhoto(ctx, chat, h.Entry.Crop, caption); err != nil {
			log.Printf("chat %d: send crop: %v", chat, err)
			b.say(ctx, chat, caption)
		}
	}
	return nil
}

// Caption describes one notebook line under its handwritten picture.
func Caption(h match.Hit) string {
	e := h.Entry
	var sb strings.Builder
	if h.Strength == match.Strong {
		sb.WriteString("Found in the notebook:\n")
	} else {
		fmt.Fprintf(&sb, "Is this them? Only the name %q matches.\n", strings.Join(h.Shared, " "))
	}
	fmt.Fprintf(&sb, "%s, %s", orUnknown(e.Function), orUnknown(e.Date))
	if e.Number != "" {
		fmt.Fprintf(&sb, ", line %s", e.Number)
	}
	fmt.Fprintf(&sb, "\n“%s”\nGave: %s", e.Text, Gift(e))
	if len(e.Flags) > 0 {
		fmt.Fprintf(&sb, "\n⚠️ Please check the handwriting: %s", strings.Join(e.Flags, "; "))
	}
	return sb.String()
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
		parts = append(parts, fmt.Sprintf("%g %s of gold", e.Gold, unit))
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
