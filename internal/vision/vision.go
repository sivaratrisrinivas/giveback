// Package vision sends images to a model and gets JSON back.
//
// Reader.ReadJSON always asks Gemini (gemini-4-hydrogen) first and falls back
// to Claude (claude-haiku-5-5) only when Gemini fails: busy, rate-limited,
// timed out, or returned something that is not JSON. Reader.Haiku() asks
// Claude directly; it is used only for the amount check (read 2), an exception
// the user approved to save Gemini's small free daily quota. Both go through
// the Sprites connector gateway, so no API key ever lives in this program or on
// the Sprite.
package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

const (
	PrimaryModel  = "gemini-4-hydrogen"
	FallbackModel = "claude-haiku-5-5"

	// Connector gateway addresses for this Fly org. They are not secrets: the
	// gateway decides who may call them from the calling Sprite's identity.
	defaultGeminiBase    = "https://api.sprites.dev/v1/gateway/gemini/_etoR37tem2Nad0NLj32jQ/v1beta"
	defaultAnthropicBase = "https://api.sprites.dev/v1/gateway/anthropic/6cVOjdQxyUe2P1S92YnQxg/"
)

// Image is one picture to send, e.g. a notebook page or an invitation card.
type Image struct {
	MIME string // "image/jpeg" or "image/png"
	Data []byte
}

// Result is the model's JSON answer plus how we got it.
type Result struct {
	JSON       json.RawMessage
	Model      string        // which model produced JSON
	Elapsed    time.Duration // total time, including a failed primary attempt
	PrimaryErr error         // why Gemini was skipped, if it was
	Cached     bool          // answered from the disk cache, no model call made
}

// Asker answers a prompt about images with JSON. *Reader, Reader.Haiku(),
// Cached and test fakes all satisfy it.
type Asker interface {
	ReadJSON(ctx context.Context, prompt string, schema map[string]any, imgs ...Image) (Result, error)
}

type Reader struct {
	geminiBase     string
	http           *http.Client
	claude         anthropic.Client
	PrimaryTimeout time.Duration
}

func New() *Reader {
	return &Reader{
		geminiBase: envOr("GIVEBACK_GEMINI_BASE", defaultGeminiBase),
		http:       &http.Client{},
		claude: anthropic.NewClient(
			option.WithBaseURL(envOr("GIVEBACK_ANTHROPIC_BASE", defaultAnthropicBase)),
			// The gateway adds the real key; the SDK just needs something to send.
			option.WithAPIKey("sprites-gateway"),
			option.WithMaxRetries(1),
		),
		PrimaryTimeout: 45 * time.Second,
	}
}

// ReadJSON asks for a JSON answer about the images. The prompt must describe
// the JSON shape wanted; schema (JSON Schema) is also passed to Gemini to
// constrain its output.
func (r *Reader) ReadJSON(ctx context.Context, prompt string, schema map[string]any, imgs ...Image) (Result, error) {
	start := time.Now()
	out, err := r.geminiCall(ctx, prompt, schema, imgs)
	// "High demand" (503) and rate limits (429) on the free tier usually pass in
	// a moment: try Gemini once more before falling back. A timeout is not retried.
	var he *geminiHTTPError
	if errors.As(err, &he) && (he.Status == 503 || (he.Status == 429 && he.RetryAfter <= maxRetryWait)) {
		wait := max(he.RetryAfter, 2*time.Second)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
		}
		out, err = r.geminiCall(ctx, prompt, schema, imgs)
	}
	if err == nil {
		return Result{JSON: out, Model: PrimaryModel, Elapsed: time.Since(start)}, nil
	}
	primaryErr := err

	out, err = r.haikuCall(ctx, prompt, imgs)
	if err != nil {
		return Result{}, fmt.Errorf("both models failed: %s: %v; %s: %w", PrimaryModel, primaryErr, FallbackModel, err)
	}
	return Result{JSON: out, Model: FallbackModel, Elapsed: time.Since(start), PrimaryErr: primaryErr}, nil
}

// Haiku returns an Asker that goes straight to Claude, skipping Gemini.
func (r *Reader) Haiku() Asker { return haikuOnly{r} }

type haikuOnly struct{ r *Reader }

func (h haikuOnly) ReadJSON(ctx context.Context, prompt string, _ map[string]any, imgs ...Image) (Result, error) {
	start := time.Now()
	out, err := h.r.haikuCall(ctx, prompt, imgs)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", FallbackModel, err)
	}
	return Result{JSON: out, Model: FallbackModel, Elapsed: time.Since(start)}, nil
}

// geminiCall makes one Gemini attempt with its own timeout and records it.
func (r *Reader) geminiCall(ctx context.Context, prompt string, schema map[string]any, imgs []Image) (json.RawMessage, error) {
	start := time.Now()
	pctx, cancel := context.WithTimeout(ctx, r.PrimaryTimeout)
	out, u, err := r.gemini(pctx, prompt, schema, imgs)
	cancel()
	c := Call{Model: PrimaryModel, OK: err == nil, Elapsed: time.Since(start), In: u.In, Out: u.Out, Thinking: u.Thinking}
	if err != nil {
		c.Err = shortErr(err)
	}
	record(ctx, c)
	return out, err
}

// haikuCall makes one Claude request and records it.
func (r *Reader) haikuCall(ctx context.Context, prompt string, imgs []Image) (json.RawMessage, error) {
	start := time.Now()
	out, u, err := r.haiku(ctx, prompt, imgs)
	c := Call{Model: FallbackModel, OK: err == nil, Elapsed: time.Since(start), In: u.In, Out: u.Out, CostUSD: haikuCost(u.In, u.Out)}
	if err != nil {
		c.Err = shortErr(err)
	}
	record(ctx, c)
	return out, err
}

type usage struct{ In, Out, Thinking int }

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

func (r *Reader) gemini(ctx context.Context, prompt string, schema map[string]any, imgs []Image) (json.RawMessage, usage, error) {
	parts := []map[string]any{{"text": prompt}}
	for _, im := range imgs {
		parts = append(parts, map[string]any{"inline_data": map[string]any{
			"mime_type": im.MIME, "data": base64.StdEncoding.EncodeToString(im.Data),
		}})
	}
	gen := map[string]any{"responseMimeType": "application/json"}
	if schema != nil {
		gen["responseJsonSchema"] = schema
	}
	body, _ := json.Marshal(map[string]any{
		"contents":         []map[string]any{{"role": "user", "parts": parts}},
		"generationConfig": gen,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.geminiBase+"/models/"+PrimaryModel+":generateContent", bytes.NewReader(body))
	if err != nil {
		return nil, usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, usage{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, usage{}, newGeminiHTTPError(resp.StatusCode, raw)
	}
	var gr struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			Prompt     int `json:"promptTokenCount"`
			Candidates int `json:"candidatesTokenCount"`
			Thoughts   int `json:"thoughtsTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(raw, &gr); err != nil {
		return nil, usage{}, fmt.Errorf("gemini: decode response: %w", err)
	}
	u := usage{In: gr.UsageMetadata.Prompt, Out: gr.UsageMetadata.Candidates + gr.UsageMetadata.Thoughts, Thinking: gr.UsageMetadata.Thoughts}
	if len(gr.Candidates) == 0 {
		return nil, u, errors.New("gemini: no candidates returned")
	}
	var sb strings.Builder
	for _, p := range gr.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	out, err := asJSON(sb.String())
	return out, u, err
}

func (r *Reader) haiku(ctx context.Context, prompt string, imgs []Image) (json.RawMessage, usage, error) {
	var blocks []anthropic.ContentBlockParamUnion
	for _, im := range imgs {
		blocks = append(blocks, anthropic.NewImageBlockBase64(im.MIME, base64.StdEncoding.EncodeToString(im.Data)))
	}
	blocks = append(blocks, anthropic.NewTextBlock(prompt+"\n\nReply with the JSON object only, no other text."))
	msg, err := r.claude.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     FallbackModel,
		MaxTokens: 16000,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(blocks...)},
	})
	if err != nil {
		return nil, usage{}, err
	}
	u := usage{In: int(msg.Usage.InputTokens), Out: int(msg.Usage.OutputTokens)}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return nil, u, errors.New("claude declined the request")
	}
	var sb strings.Builder
	for _, b := range msg.Content {
		if t, ok := b.AsAny().(anthropic.TextBlock); ok {
			sb.WriteString(t.Text)
		}
	}
	out, err := asJSON(sb.String())
	return out, u, err
}

// asJSON accepts the model's text if it is JSON, tolerating a ```json fence.
func asJSON(s string) (json.RawMessage, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if !json.Valid([]byte(s)) {
		return nil, fmt.Errorf("model did not return valid JSON: %.120q", s)
	}
	return json.RawMessage(s), nil
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// maxRetryWait is the longest Gemini may ask us to wait (429 retryDelay) before
// we try it again rather than falling back.
const maxRetryWait = 20 * time.Second

type geminiHTTPError struct {
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *geminiHTTPError) Error() string {
	return fmt.Sprintf("gemini HTTP %d: %s", e.Status, e.Message)
}

func newGeminiHTTPError(status int, raw []byte) *geminiHTTPError {
	e := &geminiHTTPError{Status: status, Message: firstLine(raw)}
	var body struct {
		Error struct {
			Message string `json:"message"`
			Details []struct {
				RetryDelay string `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Error.Message != "" {
		e.Message = firstLine([]byte(body.Error.Message))
		for _, d := range body.Error.Details {
			if t, err := time.ParseDuration(d.RetryDelay); err == nil {
				e.RetryAfter = t
			}
		}
	}
	return e
}
