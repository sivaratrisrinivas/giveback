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
	"path/filepath"
	"strings"
	"time"

	"github.com/sivaratrisrinivas/giveback/internal/bot"
	"github.com/sivaratrisrinivas/giveback/internal/ledger"
	"github.com/sivaratrisrinivas/giveback/internal/notebook"
	"github.com/sivaratrisrinivas/giveback/internal/telegram"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

var started = time.Now()

func main() {
	home, _ := os.UserHomeDir()
	cfgDir := envOr("GIVEBACK_CONFIG", filepath.Join(home, ".config", "giveback"))
	dataDir := envOr("GIVEBACK_DATA", filepath.Join(home, "giveback", "data"))
	token := mustRead(filepath.Join(cfgDir, "bot.token"))
	secret := mustRead(filepath.Join(cfgDir, "webhook.secret"))

	store, err := ledger.Open(dataDir)
	if err != nil {
		log.Fatalf("open ledger: %v", err)
	}
	reader := vision.New()
	cache := filepath.Join(dataDir, "cache") // the same photo never reaches a model twice
	firstModel := vision.Cached(reader, cache, "hydrogen-first")
	b := &bot.Bot{
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
		fmt.Fprintf(w, "Giveback is awake (process up %s).\n", time.Since(started).Round(time.Second))
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
		if m := u.Message; m != nil {
			// Seconds from Telegram receiving the message to us handling it: this
			// includes waking the Sprite. Telegram only gives whole seconds.
			log.Printf("update %d: chat %d, delivery lag %ds, process up %s", u.UpdateID, m.Chat.ID,
				time.Now().Unix()-m.Date, time.Since(started).Round(time.Second))
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
		w.WriteHeader(http.StatusOK)
	})

	addr := ":" + envOr("PORT", "8080")
	log.Printf("giveback listening on %s, ledger in %s", addr, dataDir)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
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
