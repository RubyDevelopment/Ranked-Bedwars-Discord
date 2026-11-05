// Package skins fetches Minecraft skin pictures (a player's face, their body) for banners from several providers at once-ish: it
// measures them, tries the fastest healthy one first, falls back to the next when one fails, and starts the next in parallel when one
// is merely slow, so a slow or broken skin site never stalls a banner. Pictures are cached, and identical requests share one download.
package skins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind is which picture of a player is wanted.
type Kind string

const (
	Face Kind = "face"
	Body Kind = "body"
)

// Skin is one picture: PNG bytes and which provider made it.
type Skin struct {
	Data     []byte
	Provider string
}

// DataURL is the picture as a data: URL, for a banner that is drawn elsewhere.
func (s *Skin) DataURL() string {
	return "data:image/png;base64," + base64Std(s.Data)
}

// ── Providers ───────────────────────────────────────────────────────────────

// provider is one skin site. style groups bodies that look alike: a body from a 3D renderer and a flat one are different pictures, so
// a body is only taken from a lower-numbered style group after every provider of a higher one failed.
type provider struct {
	name  string
	style int
	url   map[Kind]func(name string) string
}

// providers: NMSR renders both pictures in 3D and is the fastest; Minotar is flat but very reliable; Visage renders in 3D but is slow
// (it is the last resort). The skin sites that need a player's UUID (Crafatar, Mineatar) cannot be used: the bot knows usernames only,
// and mc-heads.net answers 403 to servers.
var providers = []*provider{
	{name: "nmsr", style: 0, url: map[Kind]func(string) string{
		Face: func(n string) string { return "https://nmsr.nickac.dev/face/" + n },
		Body: func(n string) string { return "https://nmsr.nickac.dev/fullbody/" + n },
	}},
	{name: "minotar", style: 1, url: map[Kind]func(string) string{
		Face: func(n string) string { return "https://minotar.net/helm/" + n + "/256.png" },
		Body: func(n string) string { return "https://minotar.net/armor/body/" + n + "/400.png" },
	}},
	{name: "visage", style: 0, url: map[Kind]func(string) string{
		Face: func(n string) string { return "https://visage.surgeplay.com/face/256/" + n },
		Body: func(n string) string { return "https://visage.surgeplay.com/full/400/" + n },
	}},
}

// health is what is known about a provider: how fast it answers and whether it is failing.
type health struct {
	mu       sync.Mutex
	latency  time.Duration // moving average of answers; 0 = not measured yet
	failures int           // in a row
	downTill time.Time
}

var healths = map[string]*health{}

func init() {
	for _, p := range providers {
		healths[p.name] = &health{}
	}
}

const unmeasured = 400 * time.Millisecond // what a provider is assumed to take until it has answered once

func (h *health) record(took time.Duration, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		if h.latency == 0 {
			h.latency = took
		} else {
			h.latency = (h.latency*7 + took*3) / 10
		}
		h.failures, h.downTill = 0, time.Time{}
		return
	}
	var miss *missError
	if errors.As(err, &miss) || errors.Is(err, context.Canceled) {
		return // this player is not on this site, or another provider won first: the provider itself is fine
	}
	h.failures++
	h.downTill = time.Now().Add(min(30*time.Second<<min(h.failures-1, 4), 5*time.Minute)) // 30s, 1m, 2m, 4m, 5m
}

func (h *health) snapshot() (latency time.Duration, down bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.latency, time.Now().Before(h.downTill)
}

// ranked lists the providers that have the picture, best first: lowest style group, then healthy before failing, then fastest.
func ranked(kind Kind) []*provider {
	type entry struct {
		p       *provider
		latency time.Duration
		down    bool
	}
	var list []entry
	for _, p := range providers {
		if p.url[kind] == nil {
			continue
		}
		l, down := healths[p.name].snapshot()
		if l == 0 {
			l = unmeasured
		}
		list = append(list, entry{p, l, down})
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.down != b.down {
			return !a.down
		}
		if kind == Body && a.p.style != b.p.style { // a face looks the same from any site; a body from a flat renderer does not
			return a.p.style < b.p.style
		}
		return a.latency < b.latency
	})
	out := make([]*provider, len(list))
	for i, e := range list {
		out[i] = e.p
	}
	return out
}

// ── Fetching ────────────────────────────────────────────────────────────────

// missError means the site answered but has no picture for this name (a 404 or 422): not a fault of the site.
type missError struct{ status int }

func (e *missError) Error() string { return fmt.Sprintf("no picture (status %d)", e.status) }

const (
	maxPicture    = 2 << 20
	attemptLimit  = 3 * time.Second
	minHedgeAfter = 300 * time.Millisecond
)

// client is a variable so tests can point the providers at a test server.
var client = &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 16, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 2 * time.Second}}

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// fetchFrom asks one provider.
func fetchFrom(ctx context.Context, p *provider, kind Kind, name string) (*Skin, error) {
	ctx, cancel := context.WithTimeout(ctx, attemptLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url[kind](url.PathEscape(name)), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/png,image/*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; RankedBedwarsBanners/1.0)")
	started := time.Now()
	skin, err := func() (*Skin, error) {
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		switch {
		case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusUnprocessableEntity || res.StatusCode == http.StatusBadRequest:
			return nil, &missError{res.StatusCode}
		case res.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("%s answered %d", p.name, res.StatusCode)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(limited(res.Body, maxPicture+1)); err != nil {
			return nil, err
		}
		if buf.Len() > maxPicture || !bytes.HasPrefix(buf.Bytes(), pngMagic) {
			return nil, fmt.Errorf("%s did not answer with a PNG", p.name)
		}
		return &Skin{Data: buf.Bytes(), Provider: p.name}, nil
	}()
	healths[p.name].record(time.Since(started), err)
	return skin, err
}

// hedgeAfter is how long to wait for a provider before starting the next one beside it: three times its usual time, at least 300 ms
// (500 ms for one that has not been measured yet).
func hedgeAfter(p *provider) time.Duration {
	l, _ := healths[p.name].snapshot()
	if l == 0 {
		return 500 * time.Millisecond
	}
	return max(minHedgeAfter, 3*l)
}

// race gets the picture from the best provider, falls back to the next on a failure, and starts the next one in parallel when the
// current one is slow. The first good answer wins and the rest are cancelled.
func race(ctx context.Context, kind Kind, name string) (*Skin, error) {
	order := ranked(kind)
	if len(order) == 0 {
		return nil, errors.New("no skin provider")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		skin *Skin
		err  error
	}
	results := make(chan result, len(order))
	next, running := 0, 0
	launch := func() {
		p := order[next]
		next++
		running++
		go func() {
			s, err := fetchFrom(ctx, p, kind, name)
			results <- result{s, err}
		}()
	}
	launch()
	hedge := time.NewTimer(hedgeAfter(order[0]))
	defer hedge.Stop()
	var last error
	for running > 0 {
		select {
		case r := <-results:
			running--
			if r.err == nil {
				return r.skin, nil
			}
			last = r.err
			if next < len(order) {
				launch()
			}
		case <-hedge.C:
			if next < len(order) {
				hedge.Reset(hedgeAfter(order[next-1]))
				launch()
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("no skin provider had %s's %s: %w", name, kind, last)
}

// ── Cache ───────────────────────────────────────────────────────────────────

const (
	keepFor      = 6 * time.Hour    // a skin changes rarely
	keepFailedAs = 30 * time.Second // a name nobody had is not asked for again at once
	cacheBytes   = 64 << 20
)

type cached struct {
	skin    *Skin
	err     error
	expires time.Time
}

var store = struct {
	mu       sync.Mutex
	by       map[string]cached
	order    []string // oldest first
	bytes    int
	inflight map[string]*call
}{by: map[string]cached{}, inflight: map[string]*call{}}

type call struct {
	done chan struct{}
	skin *Skin
	err  error
}

// Fetch returns a player's picture: from the cache, by joining a download already running for it, or by racing the providers.
func Fetch(kind Kind, name string) (*Skin, error) {
	start()
	key := string(kind) + "/" + strings.ToLower(name)
	store.mu.Lock()
	if c, ok := store.by[key]; ok && time.Now().Before(c.expires) {
		store.mu.Unlock()
		return c.skin, c.err
	}
	if c, ok := store.inflight[key]; ok {
		store.mu.Unlock()
		<-c.done
		return c.skin, c.err
	}
	c := &call{done: make(chan struct{})}
	store.inflight[key] = c
	store.mu.Unlock()

	c.skin, c.err = race(context.Background(), kind, name)

	store.mu.Lock()
	delete(store.inflight, key)
	entry := cached{skin: c.skin, err: c.err, expires: time.Now().Add(keepFor)}
	size := 0
	if c.err != nil {
		entry.expires = time.Now().Add(keepFailedAs)
	} else {
		size = len(c.skin.Data)
	}
	if old, ok := store.by[key]; ok && old.skin != nil {
		store.bytes -= len(old.skin.Data)
	} else if !ok {
		store.order = append(store.order, key)
	}
	store.by[key] = entry
	store.bytes += size
	for store.bytes > cacheBytes && len(store.order) > 1 { // drop the oldest first
		oldest := store.order[0]
		store.order = store.order[1:]
		if e, ok := store.by[oldest]; ok {
			if e.skin != nil {
				store.bytes -= len(e.skin.Data)
			}
			delete(store.by, oldest)
		}
	}
	store.mu.Unlock()
	close(c.done)
	return c.skin, c.err
}

// ── URLs the banners use ────────────────────────────────────────────────────

var urlPattern = regexp.MustCompile(`^https://nmsr\.nickac\.dev/(face|fullbody)/([A-Za-z0-9_%]{1,48})$`)

// Parse recognises the picture URLs the banners are given for a player (banner.HeadURL, banner.BodyURL): which picture and whose.
func Parse(raw string) (Kind, string, bool) {
	m := urlPattern.FindStringSubmatch(raw)
	if m == nil {
		return "", "", false
	}
	name, err := url.PathUnescape(m[2])
	if err != nil || name == "" {
		return "", "", false
	}
	if m[1] == "fullbody" {
		return Body, name, true
	}
	return Face, name, true
}

// FetchAll gets the pictures behind the player picture URLs among values, all at once, and returns each as a data: URL by the key it
// was under. A value that is not a player picture URL, or whose picture no provider could give, is left out.
func FetchAll(values map[string]string) map[string]string {
	type job struct{ key, raw string }
	var jobs []job
	for k, v := range values {
		if _, _, ok := Parse(v); ok {
			jobs = append(jobs, job{k, v})
		}
	}
	out := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 12)
	for _, j := range jobs {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			kind, name, _ := Parse(j.raw)
			if s, err := Fetch(kind, name); err == nil {
				mu.Lock()
				out[j.key] = s.DataURL()
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

// ── Measuring ───────────────────────────────────────────────────────────────

var startOnce sync.Once

// probeName is a name every provider knows.
const probeName = "Notch"

// start begins measuring the providers: once now, then every few minutes, so the order follows whichever is fastest at the moment.
func start() {
	startOnce.Do(func() {
		go func() {
			probe()
			for range time.Tick(5 * time.Minute) {
				probe()
			}
		}()
	})
}

// Probe measures every provider (face and body) now and logs the result. It is called once at start and on a timer.
func probe() {
	var wg sync.WaitGroup
	for _, p := range providers {
		for _, kind := range []Kind{Face, Body} {
			if p.url[kind] == nil {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = fetchFrom(context.Background(), p, kind, probeName)
			}()
		}
	}
	wg.Wait()
	var parts []string
	for _, p := range ranked(Face) {
		l, down := healths[p.name].snapshot()
		state := l.Round(time.Millisecond).String()
		if down {
			state = "down"
		}
		parts = append(parts, p.name+" "+state)
	}
	log.Printf("skin providers, fastest first: %s", strings.Join(parts, ", "))
}
