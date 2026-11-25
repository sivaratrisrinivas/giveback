package vision

import (
	"context"
	"encoding/json"
	"testing"
)

type countAsker struct{ calls int }

func (c *countAsker) ReadJSON(context.Context, string, map[string]any, ...Image) (Result, error) {
	c.calls++
	return Result{JSON: json.RawMessage(`{"ok":true}`), Model: "fake"}, nil
}

// The same photo sent twice must not reach the model twice.
func TestCachedNeverAsksTwice(t *testing.T) {
	dir := t.TempDir()
	inner := &countAsker{}
	a := Cached(inner, dir, "hydrogen-first")
	photo := Image{MIME: "image/jpeg", Data: []byte("page one")}
	ctx := context.Background()

	if _, err := a.ReadJSON(ctx, "read", nil, photo); err != nil {
		t.Fatal(err)
	}
	res, err := a.ReadJSON(ctx, "read", nil, Image{MIME: "image/jpeg", Data: []byte("page one")})
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 || !res.Cached || res.Model != "fake" || string(res.JSON) != `{"ok":true}` {
		t.Errorf("calls %d, cached %v, model %q, json %s", inner.calls, res.Cached, res.Model, res.JSON)
	}
	// A different photo, prompt or route is a new question.
	a.ReadJSON(ctx, "read", nil, Image{Data: []byte("page two")})
	a.ReadJSON(ctx, "other prompt", nil, photo)
	Cached(inner, dir, "haiku").ReadJSON(ctx, "read", nil, photo)
	if inner.calls != 4 {
		t.Errorf("calls %d, want 4", inner.calls)
	}
}

type fallbackAsker struct{ calls int }

func (f *fallbackAsker) ReadJSON(context.Context, string, map[string]any, ...Image) (Result, error) {
	f.calls++
	return Result{JSON: json.RawMessage(`{}`), Model: FallbackModel, PrimaryErr: errBusy}, nil
}

var errBusy = &geminiHTTPError{Status: 503, Message: "high demand"}

// A fallback answer is not kept, so the primary model gets another chance.
func TestCachedSkipsFallbackAnswers(t *testing.T) {
	inner := &fallbackAsker{}
	a := Cached(inner, t.TempDir(), "hydrogen-first")
	photo := Image{Data: []byte("card")}
	a.ReadJSON(context.Background(), "read", nil, photo)
	a.ReadJSON(context.Background(), "read", nil, photo)
	if inner.calls != 2 {
		t.Errorf("calls %d, want 2", inner.calls)
	}
}
