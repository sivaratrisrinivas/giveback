// Command giveback is the family gift-notebook agent that runs on a Fly.io Sprite.
//
// Telegram delivers each message by webhook to the Sprite's public URL, which
// wakes the Sprite; between messages it sleeps. Pages go into a SQLite ledger
// with a picture of every handwritten line; a forwarded invitation card is
// answered with the matching lines.
package main

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sivaratrisrinivas/giveback/internal/bot"
	"github.com/sivaratrisrinivas/giveback/internal/ledger"
	"github.com/sivaratrisrinivas/giveback/internal/notebook"
	"github.com/sivaratrisrinivas/giveback/internal/telegram"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
	"github.com/sivaratrisrinivas/giveback/internal/wake"
)

// sampleCard is the made-up invitation the demo bot sends with /start.
//
//go:embed testdata/sample-card.jpg
var sampleCard []byte

func main() {
	home, _ := os.UserHomeDir()
	cfgDir := envOr("GIVEBACK_CONFIG", filepath.Join(home, ".config", "giveback"))
	dataDir := envOr("GIVEBACK_DATA", filepath.Join(home, "giveback", "data"))
	if len(os.Args) > 1 && os.Args[1] == "copy-for" {
		copyFor(dataDir, os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "seed-demo" {
		seedDemo(dataDir, os.Args[2:])
		return
	}
	token := mustRead(filepath.Join(cfgDir, "bot.token"))
	secret := mustRead(filepath.Join(cfgDir, "webhook.secret"))

	store, err := ledger.Open(dataDir)
	if err != nil {
		log.Fatalf("open ledger: %v", err)
	}
	firstModel := vision.Cached(vision.New(), filepath.Join(dataDir, "cache"), "hydrogen-first")

	// Notice each time the Sprite slept: the process is frozen, not stopped.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	wakes := wake.New(time.Now())
	go wakes.Run(ctx)

	b := &bot.Bot{
		Wakes: wakes,
		Store: store,
		TG:    telegram.New(token),
		Pages: pageModels(dataDir),
		Cards: firstModel,
	}
	// The public demo bot: a file named "demo" beside the bot token turns it on.
	if _, err := os.Stat(filepath.Join(cfgDir, "demo")); err == nil {
		card := filepath.Join(dataDir, "sample-card.jpg")
		if err := os.WriteFile(card, sampleCard, 0o644); err != nil {
			log.Fatalf("write sample card: %v", err)
		}
		b.Demo = &bot.Demo{CardsPerDay: 40, CardsPerChat: 5, SampleCard: card}
		log.Printf("demo mode: read-only notebook, answers kept per chat, %d card reads a day (%d per chat)", b.Demo.CardsPerDay, b.Demo.CardsPerChat)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Giveback is awake: %s.\n", wakes.Status(time.Now()))
	})
	mux.HandleFunc("POST /telegram", func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var u telegram.Update
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		arrived := time.Now()
		woke, afterWake := wakes.FirstRequest(arrived)
		// Seconds from Telegram receiving the message to us handling it: this
		// includes waking the Sprite. Telegram only gives whole seconds.
		lag := int64(-1)
		if m := u.Message; m != nil {
			lag = arrived.Unix() - m.Date
			log.Printf("update %d: chat %d, delivery lag %ds", u.UpdateID, m.Chat.ID, lag)
		} else {
			log.Printf("update %d: button tap", u.UpdateID)
		}
		if afterWake {
			log.Printf("update %d: first request after the Sprite woke (slept %s, running again %s before this arrived)",
				u.UpdateID, woke.Slept.Round(time.Second), arrived.Sub(woke.At).Round(time.Millisecond))
		}
		// Work happens inside the request on purpose: a Sprite stays awake while
		// it is serving a request. The work gets its own context so it finishes
		// even if Telegram gives up waiting and re-delivers (the ledger drops
		// the repeat).
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		start := time.Now()
		if err := b.Handle(ctx, u); err != nil {
			log.Printf("update %d: %v", u.UpdateID, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		log.Printf("update %d: handled in %s", u.UpdateID, time.Since(start).Round(time.Millisecond))
		if afterWake && lag >= 0 {
			log.Printf("update %d: woken by this message; from sending it to the reply: about %ds (delivery %ds + handling %s)",
				u.UpdateID, lag+int64(time.Since(start).Round(time.Second)/time.Second), lag, time.Since(start).Round(time.Millisecond))
		}
		w.WriteHeader(http.StatusOK)
	})

	addr := ":" + envOr("PORT", "8080")
	log.Printf("giveback listening on %s, ledger in %s", addr, dataDir)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		// On a restart (deploy), let a reply in progress finish first. If it
		// can't, Telegram re-delivers it and the ledger allows one more try.
		<-ctx.Done()
		log.Printf("shutting down: finishing requests in progress")
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Printf("stopped")
}

// copyFor writes a copy of the notebook for a new household (see
// ledger.CopyFor). scripts/split.sh runs it on the old household's Sprite
// while the bot keeps running.
func copyFor(dataDir string, args []string) {
	if len(args) != 3 {
		log.Fatal("usage: giveback copy-for <out-dir> <this household's name> <new household's name>")
	}
	store, err := ledger.Open(dataDir)
	if err != nil {
		log.Fatalf("open ledger: %v", err)
	}
	defer store.Close()
	start := time.Now()
	c, err := store.CopyFor(args[0], args[1], args[2])
	if err != nil {
		log.Fatalf("copy: %v", err)
	}
	fmt.Printf("copied %d page(s), %d line(s) for the %s household into %s in %s\n",
		c.Pages, c.Lines, c.Name, args[0], time.Since(start).Round(time.Millisecond))
}

// pageModels reads notebook pages. Every answer is kept in the data dir's
// cache: the same photo never reaches a model twice.
func pageModels(dataDir string) notebook.Models {
	reader, cache := vision.New(), filepath.Join(dataDir, "cache")
	return notebook.Models{
		Page:    vision.Cached(reader, cache, "hydrogen-first"),
		Amounts: vision.Cached(reader.Haiku(), cache, "haiku"), // approved exception: saves Gemini's free quota
	}
}

// seedDemo fills the demo bot's notebook: the sample pages and the made-up
// family's father and mother. Run it on the demo Sprite. A page already read
// by the primary model is skipped; one read by the fallback is read again.
func seedDemo(dataDir string, photos []string) {
	if len(photos) == 0 {
		log.Fatal("usage: giveback seed-demo page1.jpeg page2.jpeg …")
	}
	store, err := ledger.Open(dataDir)
	if err != nil {
		log.Fatalf("open ledger: %v", err)
	}
	defer store.Close()
	models := pageModels(dataDir)
	ctx := context.Background()
	for _, path := range photos {
		data, err := os.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		hash := vision.Hash(data)
		old, have, err := store.PageByHash(hash)
		if err != nil {
			log.Fatal(err)
		}
		if have && old.Model != vision.FallbackModel {
			fmt.Printf("%s: already read by %s (page %d, %d lines)\n", path, old.Model, old.ID, old.Lines)
			continue
		}
		img, err := bot.ForModel(data)
		if err != nil {
			log.Fatalf("%s: %v", path, err)
		}
		start := time.Now()
		p, err := notebook.Read(ctx, models, img)
		if err != nil {
			log.Fatalf("%s: %v", path, err)
		}
		if have {
			if err := store.DeletePage(old.ID); err != nil {
				log.Fatal(err)
			}
		}
		info, err := store.AddPage(hash, p)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: page %d, %s %s, %d lines, %d flagged; read 1 %s, read 2 %s, %s\n", path, info.ID, p.Function, p.Date,
			info.Lines, info.Flagged, p.FirstRead.Model, p.SecondRead.Model, time.Since(start).Round(time.Second))
		if p.FirstRead.Model == vision.FallbackModel {
			fmt.Printf("  read 1 fell back to %s (%v); run seed-demo again later for a %s reading\n", vision.FallbackModel, p.FirstRead.PrimaryErr, vision.PrimaryModel)
		}
	}
	for r, n := range map[string]string{"father": "Bandaru Subba Rao", "mother": "Butham Lakshmi"} {
		if err := store.SetFamily(r, n); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println("family: father = Bandaru Subba Rao, mother = Butham Lakshmi")
}

func mustRead(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(b))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
