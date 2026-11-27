package bot

import (
	"context"
	"errors"
	"log"

	"github.com/sivaratrisrinivas/giveback/internal/card"
	"github.com/sivaratrisrinivas/giveback/internal/ledger"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

// Demo makes the bot the public demo: anyone can message it, so the notebook
// is read-only, each visitor's answers stay in their own chat, and model
// reads of cards are capped per day. Typed names cost no model call.
type Demo struct {
	CardsPerDay  int    // cards read by a model per UTC day, all visitors together
	CardsPerChat int    // and per visitor
	SampleCard   string // the made-up invitation /start sends, to forward back
}

const demoHelp = `Namaskaram! This is the public demo of Giveback, a family gift-notebook bot: github.com/sivaratrisrinivas/giveback

It holds a made-up notebook: two handwritten pages (Telugu and English) of the invented Bandaru family. The father is Bandaru Subba Rao; the mother, before marriage, was Butham Lakshmi.

Three things to try:
1. Forward the invitation card below back to me. I'll find its hosts in the notebook and show the handwritten lines, what they gave, and what that's worth today.
2. Type Bandaru Ramulu. That name is never written: the notebook says బాబాయి (father's younger brother). I'll ask "Is this them?". Tap ✅, then type the name again.
3. Type another name, like Puram Nagaiah or Muthyalu.

Your answers stay in your chat. This notebook can't be changed here; to keep your own family's notebook, run your own Giveback (steps in the README).`

const demoReadOnly = "This is a demo notebook, so it can't be changed here. To keep your own family's notebook, run your own Giveback: the steps are in the README at github.com/sivaratrisrinivas/giveback"

// ledgerFor is the ledger as this chat sees it: in the demo, the shared
// notebook with this visitor's own answers on top.
func (b *Bot) ledgerFor(chat int64) *ledger.Store {
	if b.Demo != nil {
		return b.Store.ForChat(chat)
	}
	return b.Store
}

func (b *Bot) demoStart(ctx context.Context, chat int64, withCard bool) error {
	b.say(ctx, chat, demoHelp)
	if !withCard || b.Demo.SampleCard == "" {
		return nil
	}
	if err := b.TG.SendPhoto(ctx, chat, b.Demo.SampleCard, "A made-up invitation. Forward it back to me.", nil); err != nil {
		log.Printf("chat %d: send sample card: %v", chat, err)
	}
	return nil
}

// readCard reads a card, keeping to the demo's daily cap. Once the cap is
// reached only cards read before (like the sample card) are answered.
func (b *Bot) readCard(ctx context.Context, chat int64, img vision.Image) (card.Invitation, vision.Result, error) {
	if b.Demo == nil {
		return card.Read(ctx, b.Cards, img)
	}
	st := b.ledgerFor(chat)
	day := now().UTC().Format("2006-01-02")
	total, mine, err := st.CardReads(day)
	if err != nil {
		return card.Invitation{}, vision.Result{}, err
	}
	if total >= b.Demo.CardsPerDay || mine >= b.Demo.CardsPerChat {
		ctx = vision.CacheOnly(ctx)
	}
	inv, res, err := card.Read(ctx, b.Cards, img)
	if !res.Cached && !errors.Is(err, vision.ErrNotCached) {
		if cerr := st.AddCardRead(day); cerr != nil {
			log.Printf("chat %d: count card read: %v", chat, cerr)
		}
	}
	return inv, res, err
}

// ForModel prepares a photo for a model, as the bot does (giveback seed-demo).
func ForModel(data []byte) (vision.Image, error) { return forModel(data) }
