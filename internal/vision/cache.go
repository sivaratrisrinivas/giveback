package vision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Cached wraps an Asker so the same question about the same images is never
// sent to a model twice: answers are kept on disk, keyed by a hash of the
// route, prompt, schema and image bytes. This protects Gemini's small free
// daily quota when a photo is sent again or a command is re-run.
func Cached(a Asker, dir, route string) Asker { return cached{a, dir, route} }

type cached struct {
	next  Asker
	dir   string
	route string // keeps answers from different routes (hydrogen-first, haiku-only) apart
}

type cachedAnswer struct {
	JSON    json.RawMessage `json:"json"`
	Model   string          `json:"model"`
	Elapsed time.Duration   `json:"elapsed"`
}

func (c cached) ReadJSON(ctx context.Context, prompt string, schema map[string]any, imgs ...Image) (Result, error) {
	path := filepath.Join(c.dir, c.key(prompt, schema, imgs)+".json")
	if b, err := os.ReadFile(path); err == nil {
		var a cachedAnswer
		if json.Unmarshal(b, &a) == nil && json.Valid(a.JSON) {
			return Result{JSON: a.JSON, Model: a.Model, Elapsed: a.Elapsed, Cached: true}, nil
		}
	}
	res, err := c.next.ReadJSON(ctx, prompt, schema, imgs...)
	if err != nil {
		return res, err
	}
	// A fallback answer is not kept: next time the primary model may be back,
	// and its reading is the one we want to keep.
	if res.PrimaryErr != nil {
		return res, nil
	}
	if b, err := json.Marshal(cachedAnswer{res.JSON, res.Model, res.Elapsed}); err == nil {
		if os.MkdirAll(c.dir, 0o755) == nil {
			_ = os.WriteFile(path, b, 0o644) // a failed write only costs a repeat call later
		}
	}
	return res, nil
}

func (c cached) key(prompt string, schema map[string]any, imgs []Image) string {
	h := sha256.New()
	sj, _ := json.Marshal(schema)
	for _, part := range [][]byte{[]byte(c.route), []byte(prompt), sj} {
		h.Write(part)
		h.Write([]byte{0})
	}
	for _, im := range imgs {
		sum := sha256.Sum256(im.Data)
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Hash is the hex SHA-256 of a photo: its identity for "have we read this before?".
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
