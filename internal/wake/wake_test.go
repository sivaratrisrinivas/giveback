package wake

import (
	"strings"
	"testing"
	"time"
)

func TestWake(t *testing.T) {
	t0 := time.Date(2026, 11, 26, 1, 0, 0, 0, time.UTC)
	w := New(t0)
	w.Check(t0.Add(time.Second))
	w.Check(t0.Add(2 * time.Second))
	if _, ok := w.FirstRequest(t0.Add(3 * time.Second)); ok {
		t.Fatal("no sleep yet, but a wake was reported")
	}
	// Frozen for 14 minutes; a request arrives before the ticker runs.
	back := t0.Add(3*time.Second + 14*time.Minute)
	wk, ok := w.FirstRequest(back)
	if !ok || wk.Slept != 14*time.Minute || !wk.At.Equal(back) {
		t.Fatalf("wake = %+v, %v", wk, ok)
	}
	if _, ok := w.FirstRequest(back.Add(time.Second)); ok {
		t.Error("the second request after a wake is not the first")
	}
	st := w.Status(back.Add(2 * time.Second))
	if !strings.Contains(st, "slept 1 time(s)") || !strings.Contains(st, "lasted 14m0s") {
		t.Errorf("status = %q", st)
	}
}
