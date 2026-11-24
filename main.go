// Command giveback is the family gift-notebook agent that runs on a Fly.io Sprite.
//
// Step 0: answer Telegram webhooks so we can watch the Sprite sleep and wake.
package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var started = time.Now()

func main() {
	home, _ := os.UserHomeDir()
	cfgDir := envOr("GIVEBACK_CONFIG", filepath.Join(home, ".config", "giveback"))
	token := mustRead(filepath.Join(cfgDir, "bot.token"))
	secret := mustRead(filepath.Join(cfgDir, "webhook.secret"))

	tg := &telegram{token: token, client: &http.Client{Timeout: 30 * time.Second}}
	seen := newSeenSet(1000)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Giveback is awake.")
	})
	mux.HandleFunc("POST /telegram", func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var u update
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Telegram re-delivers an update if we are slow or fail; answer each one once.
		if !seen.add(u.UpdateID) {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Work happens inside the request on purpose: a Sprite stays awake while it
		// is serving an HTTP request, so it cannot pause halfway through a reply.
		if m := u.Message; m != nil {
			uptime := time.Since(started).Round(time.Millisecond)
			// Seconds from Telegram receiving the message to us handling it: this
			// includes waking the Sprite. Telegram only gives whole seconds.
			lag := time.Now().Unix() - m.Date
			text := fmt.Sprintf("Namaskaram! Giveback is awake.\nThis process started %s ago.\nYour message reached me about %ds after you sent it.", uptime, lag)
			if err := tg.sendMessage(m.Chat.ID, text); err != nil {
				log.Printf("update %d: send failed: %v", u.UpdateID, err)
				http.Error(w, "upstream error", http.StatusBadGateway)
				return
			}
			log.Printf("update %d: replied to chat %d (process uptime %s, delivery lag %ds)", u.UpdateID, m.Chat.ID, uptime, lag)
		}
		w.WriteHeader(http.StatusOK)
	})

	addr := ":" + envOr("PORT", "8080")
	log.Printf("giveback listening on %s", addr)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

type update struct {
	UpdateID int64    `json:"update_id"`
	Message  *message `json:"message"`
}

type message struct {
	MessageID int64  `json:"message_id"`
	Date      int64  `json:"date"` // Unix seconds when Telegram received it
	Text      string `json:"text"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

type telegram struct {
	token  string
	client *http.Client
}

func (t *telegram) sendMessage(chatID int64, text string) error {
	body, _ := json.Marshal(map[string]any{"chat_id": chatID, "text": text})
	resp, err := t.client.Post("https://api.telegram.org/bot"+t.token+"/sendMessage", "application/json", bytes.NewReader(body))
	if err != nil {
		// The URL holds the bot token; never let it reach the logs.
		return fmt.Errorf("sendMessage request failed: %w", redact(err, t.token))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sendMessage: telegram returned %s", resp.Status)
	}
	return nil
}

// seenSet remembers recent update IDs so retried deliveries are ignored.
type seenSet struct {
	mu    sync.Mutex
	ids   map[int64]struct{}
	order []int64
	max   int
}

func newSeenSet(max int) *seenSet { return &seenSet{ids: map[int64]struct{}{}, max: max} }

func (s *seenSet) add(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[id]; ok {
		return false
	}
	s.ids[id] = struct{}{}
	s.order = append(s.order, id)
	if len(s.order) > s.max {
		delete(s.ids, s.order[0])
		s.order = s.order[1:]
	}
	return true
}

func redact(err error, secret string) error {
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), secret, "<token>"))
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
