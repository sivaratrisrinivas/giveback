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

func main() {
	home, _ := os.UserHomeDir()
	cfgDir := envOr("GIVEBACK_CONFIG", filepath.Join(home, ".config", "giveback"))
	dataDir := envOr("GIVEBACK_DATA", filepath.Join(home, "giveback", "data"))
	if len(os.Args) > 1 && os.Args[1] == "copy-for" {
		copyFor(dataDir, os.Args[2:])
		return
	}
	token := mustRead(filepath.Join(cfgDir, "bot.token"))
	secret := mustRead(filepath.Join(cfgDir, "webhook.secret"))

	store, err := ledger.Open(dataDir)
	if err != nil {
		log.Fatalf("open ledger: %v", err)
	}
	reader := vision.New()
	cache := filepath.Join(dataDir, "cache") // the same photo never reaches a model twice
	firstModel := vision.Cached(reader, cache, "hydrogen-first")

	// Notice each time the Sprite slept: the process is frozen, not stopped.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	wakes := wake.New(time.Now())
	go wakes.Run(ctx)

	b := &bot.Bot{
		Wakes: wakes,
		Store: store,
		TG:    telegram.New(token),
		Pages: notebook.Models{
			Page:    firstModel,
			Amounts: vision.Cached(reader.Haiku(), cache, "haiku"), // approved exception: saves Gemini's free quota
		},
		Cards: firstModel,
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
