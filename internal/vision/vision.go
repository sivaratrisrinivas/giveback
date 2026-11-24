// Package vision sends images to a model and gets JSON back.
//
// It always asks Gemini (gemini-4-hydrogen) first and falls back to Claude
// (claude-haiku-5-5) only when Gemini fails: busy, rate-limited, timed out, or
// returned something that is not JSON. Both go through the Sprites connector
// gateway, so no API key ever lives in this program or on the Sprite.
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
	pctx, cancel := context.WithTimeout(ctx, r.PrimaryTimeout)
	out, err := r.gemini(pctx, prompt, schema, imgs)
	cancel()
	if err == nil {
		return Result{JSON: out, Model: PrimaryModel, Elapsed: time.Since(start)}, nil
	}
	primaryErr := err

	out, err = r.haiku(ctx, prompt, imgs)
	if err != nil {
		return Result{}, fmt.Errorf("both models failed: %s: %v; %s: %w", PrimaryModel, primaryErr, FallbackModel, err)
	}
	return Result{JSON: out, Model: FallbackModel, Elapsed: time.Since(start), PrimaryErr: primaryErr}, nil
}

func (r *Reader) gemini(ctx context.Context, prompt string, schema map[string]any, imgs []Image) (json.RawMessage, error) {
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
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini HTTP %d: %s", resp.StatusCode, firstLine(raw))
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
	}
	if err := json.Unmarshal(raw, &gr); err != nil {
		return nil, fmt.Errorf("gemini: decode response: %w", err)
	}
	if len(gr.Candidates) == 0 {
		return nil, errors.New("gemini: no candidates returned")
	}
	var sb strings.Builder
	for _, p := range gr.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	return asJSON(sb.String())
}

func (r *Reader) haiku(ctx context.Context, prompt string, imgs []Image) (json.RawMessage, error) {
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
		return nil, err
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return nil, errors.New("claude declined the request")
	}
	var sb strings.Builder
	for _, b := range msg.Content {
		if t, ok := b.AsAny().(anthropic.TextBlock); ok {
			sb.WriteString(t.Text)
		}
	}
	return asJSON(sb.String())
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
