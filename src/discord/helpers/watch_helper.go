package helpers

import (
	"fmt"
	"sync"
	"time"

	appLog "ranked-bedwars/src/utility"
)

// guildWatch runs a periodic sweep only for guilds that have something pending. A guild is added by Track (the
// moment something is created, or at startup when the database already holds something) and dropped by the first
// sweep that reports nothing left. The ticker goroutine exists only while at least one guild is tracked.
type guildWatch struct {
	name    string
	every   time.Duration
	sweep   func(guildID string) (pending bool)
	mu      sync.Mutex
	guilds  map[string]uint64 // guild -> Track count, so a Track during a sweep isn't lost to that sweep's "done"
	running bool
}

func newGuildWatch(name string, every time.Duration, sweep func(guildID string) bool) *guildWatch {
	return &guildWatch{name: name, every: every, sweep: sweep, guilds: map[string]uint64{}}
}

// Track starts sweeping guildID if it isn't already.
func (w *guildWatch) Track(guildID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.guilds == nil {
		w.guilds = map[string]uint64{}
	}
	w.guilds[guildID]++
	if !w.running {
		w.running = true
		go w.loop()
	}
}

// Seed tracks every guild that has something pending right now; call it once the bot knows its guilds.
func (w *guildWatch) Seed(guildIDs []string, pending func(guildID string) bool) {
	for _, g := range guildIDs {
		if safePending(w.name, g, pending) {
			w.Track(g)
		}
	}
}

func (w *guildWatch) loop() {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for range t.C {
		w.mu.Lock()
		seen := make(map[string]uint64, len(w.guilds))
		for g, n := range w.guilds {
			seen[g] = n
		}
		w.mu.Unlock()

		for g, n := range seen {
			if safeSweep(w.name, g, w.sweep) {
				continue
			}
			w.mu.Lock()
			if w.guilds[g] == n { // nothing was tracked since this sweep began
				delete(w.guilds, g)
			}
			w.mu.Unlock()
		}

		w.mu.Lock()
		if len(w.guilds) == 0 {
			w.running = false
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()
	}
}

// safeSweep keeps a panic or failure in one guild from stopping the rest; a guild that panicked stays tracked.
func safeSweep(name, guildID string, sweep func(string) bool) (pending bool) {
	pending = true
	defer func() {
		if r := recover(); r != nil {
			appLog.EngineLogger.Error(fmt.Sprintf("[%s] sweep panic in %s: %v", name, guildID, r))
			pending = true
		}
	}()
	return sweep(guildID)
}

func safePending(name, guildID string, pending func(string) bool) bool {
	return safeSweep(name, guildID, pending)
}
