package base

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	appLog "ranked-bedwars/src/utility"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
)

type ttlEntry[T any] struct {
	v   T
	exp time.Time
}

type TTLCache[T any] struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]ttlEntry[T]
}

func NewTTLCache[T any](ttl time.Duration) *TTLCache[T] {
	return &TTLCache[T]{ttl: ttl, m: map[string]ttlEntry[T]{}}
}

// Get returns the cached value or loads it; failed loads are not cached.
func (c *TTLCache[T]) Get(key string, load func() (T, error)) (T, error) {
	c.mu.Lock()
	if e, ok := c.m[key]; ok && time.Now().Before(e.exp) {
		c.mu.Unlock()
		return e.v, nil
	}
	c.mu.Unlock()
	v, err := load()
	if err != nil {
		return v, err
	}
	c.mu.Lock()
	c.m[key] = ttlEntry[T]{v, time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return v, nil
}

func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GuildWatch runs a periodic sweep only for guilds that have something pending. A guild is added by Track (the
// moment something is created, or at startup when the database already holds something) and dropped by the first
// sweep that reports nothing left. The ticker goroutine exists only while at least one guild is tracked.
type GuildWatch struct {
	name    string
	every   time.Duration
	sweep   func(guildID string) (pending bool)
	mu      sync.Mutex
	guilds  map[string]uint64 // guild -> Track count, so a Track during a sweep isn't lost to that sweep's "done"
	running bool
}

func NewGuildWatch(name string, every time.Duration, sweep func(guildID string) bool) *GuildWatch {
	return &GuildWatch{name: name, every: every, sweep: sweep, guilds: map[string]uint64{}}
}

// Track starts sweeping guildID if it isn't already.
func (w *GuildWatch) Track(guildID string) {
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
func (w *GuildWatch) Seed(guildIDs []string, pending func(guildID string) bool) {
	for _, g := range guildIDs {
		if safePending(w.name, g, pending) {
			w.Track(g)
		}
	}
}

func (w *GuildWatch) loop() {
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

func Clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// ConfigCacheTTL is how long a guild's saved config is kept before it is read again.
const ConfigCacheTTL = 30 * time.Second

// GuildIDPattern matches a guild id as the engine and the dashboard send it.
var GuildIDPattern = regexp.MustCompile(`^[0-9]{5,25}$`)

// Session is the live Discord session, recorded on every queue event, so work that runs outside any event (game
// channels, notices) can reach Discord.
var Session atomic.Pointer[discordgo.Session]
