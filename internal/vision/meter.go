package vision

import (
	"context"
	"log"
	"sync"
	"time"
)

// Call is one request sent to a model, successful or not: what it cost and how
// long it took. Answers served from the disk cache are not calls.
type Call struct {
	Model    string
	OK       bool
	Err      string // why it failed, "" if it didn't
	Elapsed  time.Duration
	In       int     // input tokens (prompt + images)
	Out      int     // output tokens, including thinking
	Thinking int     // of Out, tokens spent thinking (Gemini reports these)
	CostUSD  float64 // 0 on Gemini's free tier
}

// Prices per million tokens, in US dollars. Gemini is used on its free tier
// (no billing on the AI Studio key), so its calls cost nothing; their tokens
// are still counted. Haiku 5.5: $0.10 input, $0.50 output.
const (
	haikuInPerMTok  = 0.10
	haikuOutPerMTok = 0.50
)

func haikuCost(in, out int) float64 {
	return (float64(in)*haikuInPerMTok + float64(out)*haikuOutPerMTok) / 1e6
}

// Meter collects the model calls made while handling one request.
type Meter struct {
	mu        sync.Mutex
	calls     []Call
	cacheHits int
}

type meterKey struct{}

// WithMeter returns a context whose model calls are collected in the Meter.
func WithMeter(ctx context.Context) (context.Context, *Meter) {
	m := &Meter{}
	return context.WithValue(ctx, meterKey{}, m), m
}

// Calls returns the calls made so far.
func (m *Meter) Calls() []Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Call(nil), m.calls...)
}

// CacheHits is how many answers came from the disk cache instead of a model.
func (m *Meter) CacheHits() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cacheHits
}

// Total sums the calls: count, input and output tokens, and cost.
func (m *Meter) Total() (calls, in, out int, cost float64) {
	for _, c := range m.Calls() {
		calls++
		in += c.In
		out += c.Out
		cost += c.CostUSD
	}
	return
}

// record logs a call and adds it to the context's Meter, if there is one.
func record(ctx context.Context, c Call) {
	status := "ok"
	if !c.OK {
		status = "failed: " + c.Err
	}
	log.Printf("model call: %s %s in %.1fs, tokens in %d out %d (thinking %d), $%.5f",
		c.Model, status, c.Elapsed.Seconds(), c.In, c.Out, c.Thinking, c.CostUSD)
	if m, ok := ctx.Value(meterKey{}).(*Meter); ok {
		m.mu.Lock()
		m.calls = append(m.calls, c)
		m.mu.Unlock()
	}
}

func recordCacheHit(ctx context.Context) {
	if m, ok := ctx.Value(meterKey{}).(*Meter); ok {
		m.mu.Lock()
		m.cacheHits++
		m.mu.Unlock()
	}
}
