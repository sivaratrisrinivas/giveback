// Package wake notices when the Sprite has slept. A sleeping Sprite keeps the
// process in memory but frozen, so the program never sees itself go to sleep;
// it only sees time jump when it runs again. A ticker checks every second, and
// each request checks too, since a request can arrive before the ticker runs.
package wake

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// gap is how long the process must have been frozen to count as a sleep. The
// ticker runs every second, so anything over a few seconds is not a GC pause.
const gap = 5 * time.Second

// Wake is one sleep that ended.
type Wake struct {
	At        time.Time     // when the process ran again (wall clock)
	Slept     time.Duration // how long it was frozen, by the wall clock
	SleptMono time.Duration // the same by the monotonic clock, which may not count a frozen VM
}

type Watcher struct {
	mu      sync.Mutex
	started time.Time
	last    time.Time // last time the process was seen running
	wakes   int
	latest  Wake
	unseen  bool // the latest wake has had no request yet
}

func New(now time.Time) *Watcher { return &Watcher{started: now, last: now} }

// Run checks every second until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Not the tick's own time: Go stamps a tick with when it was due,
			// which after a freeze is a time from before it.
			w.Check(time.Now())
		}
	}
}

// Check records a wake if the process was frozen since it last looked.
func (w *Watcher) Check(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	mono := now.Sub(w.last)
	wall := now.Round(0).Sub(w.last.Round(0)) // Round(0) drops the monotonic reading
	w.last = now
	if max(mono, wall) < gap {
		return
	}
	w.wakes++
	w.latest = Wake{At: now.Round(0), Slept: wall, SleptMono: mono}
	w.unseen = true
	log.Printf("wake: the Sprite slept for %s (wall clock; monotonic clock says %s). Wake #%d since the process started %s ago.",
		round(wall), round(mono), w.wakes, round(now.Sub(w.started)))
}

// FirstRequest returns the latest wake if this is the first request since it,
// so the caller can log how quickly that request was answered.
func (w *Watcher) FirstRequest(now time.Time) (Wake, bool) {
	w.Check(now)
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.unseen {
		return Wake{}, false
	}
	w.unseen = false
	return w.latest, true
}

// Status says how long the process has run and when it last slept.
func (w *Watcher) Status(now time.Time) string {
	w.Check(now)
	w.mu.Lock()
	defer w.mu.Unlock()
	s := fmt.Sprintf("process up %s, slept %d time(s) since it started", round(now.Sub(w.started)), w.wakes)
	if w.wakes > 0 {
		s += fmt.Sprintf("; the last sleep lasted %s and ended %s ago", round(w.latest.Slept), round(now.Round(0).Sub(w.latest.At)))
	}
	return s
}

func round(d time.Duration) time.Duration {
	if d > time.Minute {
		return d.Round(time.Second)
	}
	return d.Round(100 * time.Millisecond)
}
