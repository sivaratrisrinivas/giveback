package vision

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Fake Gemini and Claude servers: no real model is called.
func fakeModels(t *testing.T, geminiStatus int) {
	t.Helper()
	gem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if geminiStatus != http.StatusOK {
			w.WriteHeader(geminiStatus)
			w.Write([]byte(`{"error":{"code":400,"message":"bad thing"}}`))
			return
		}
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"{\"ok\":true}"}]}}],
			"usageMetadata":{"promptTokenCount":1500,"candidatesTokenCount":300,"thoughtsTokenCount":200}}`))
	}))
	claude := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5",
			"content":[{"type":"text","text":"{\"ok\":true}"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":2000,"output_tokens":400}}`))
	}))
	t.Cleanup(gem.Close)
	t.Cleanup(claude.Close)
	t.Setenv("GIVEBACK_GEMINI_BASE", gem.URL)
	t.Setenv("GIVEBACK_ANTHROPIC_BASE", claude.URL)
}

func TestMeterGemini(t *testing.T) {
	fakeModels(t, http.StatusOK)
	ctx, m := WithMeter(context.Background())
	if _, err := New().ReadJSON(ctx, "p", nil); err != nil {
		t.Fatal(err)
	}
	calls := m.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	c := calls[0]
	if c.Model != PrimaryModel || !c.OK || c.In != 1500 || c.Out != 500 || c.Thinking != 200 || c.CostUSD != 0 {
		t.Errorf("call = %+v", c)
	}
}

func TestMeterFallbackCountsBoth(t *testing.T) {
	fakeModels(t, http.StatusBadRequest) // not retried: straight to Haiku
	ctx, m := WithMeter(context.Background())
	res, err := New().ReadJSON(ctx, "p", nil)
	if err != nil || res.Model != FallbackModel {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	calls := m.Calls()
	if len(calls) != 2 || calls[0].OK || calls[0].Err == "" || !calls[1].OK {
		t.Fatalf("calls = %+v", calls)
	}
	n, in, out, cost := m.Total()
	want := (2000*0.10 + 400*0.50) / 1e6
	if n != 2 || in != 2000 || out != 400 || math.Abs(cost-want) > 1e-12 {
		t.Errorf("total = %d calls, %d in, %d out, $%f; want $%f", n, in, out, cost, want)
	}
}

func TestMeterCountsCacheHits(t *testing.T) {
	fakeModels(t, http.StatusOK)
	c := Cached(New(), t.TempDir(), "r")
	ctx, m := WithMeter(context.Background())
	for range 2 {
		if _, err := c.ReadJSON(ctx, "p", nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.Calls()) != 1 || m.CacheHits() != 1 {
		t.Errorf("calls %d, cache hits %d; want 1 and 1", len(m.Calls()), m.CacheHits())
	}
}
