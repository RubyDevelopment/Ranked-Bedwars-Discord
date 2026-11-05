package skins

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var tinyPNG = append(append([]byte{}, pngMagic...), make([]byte, 40)...)

// site is a fake skin site: how it answers, and how many times it was asked.
type site struct {
	status int           // 0 = 200 with a PNG
	delay  time.Duration // before answering
	body   []byte        // nil = a PNG
	hits   atomic.Int32
}

func (s *site) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		time.Sleep(s.delay)
		if s.status != 0 {
			w.WriteHeader(s.status)
			return
		}
		b := s.body
		if b == nil {
			b = tinyPNG
		}
		_, _ = w.Write(b)
	})
}

// install replaces the providers by fake sites (in the order given, all of one style) and forgets everything learnt before.
func install(t *testing.T, sites map[string]*site, order ...string) {
	t.Helper()
	oldProviders, oldHealths, oldClient := providers, healths, client
	t.Cleanup(func() {
		providers, healths, client = oldProviders, oldHealths, oldClient
		store.mu.Lock()
		store.by, store.order, store.bytes = map[string]cached{}, nil, 0
		store.mu.Unlock()
	})
	providers, healths = nil, map[string]*health{}
	client = http.DefaultClient
	for _, name := range order {
		srv := httptest.NewServer(sites[name].handler())
		t.Cleanup(srv.Close)
		base := srv.URL
		providers = append(providers, &provider{name: name, url: map[Kind]func(string) string{
			Face: func(n string) string { return base + "/face/" + n },
			Body: func(n string) string { return base + "/body/" + n },
		}})
		healths[name] = &health{}
	}
	store.mu.Lock()
	store.by, store.order, store.bytes = map[string]cached{}, nil, 0
	store.mu.Unlock()
	startOnce.Do(func() {}) // no background measuring in tests
}

func TestFastestProviderIsTriedFirst(t *testing.T) {
	a, b := &site{}, &site{}
	install(t, map[string]*site{"a": a, "b": b}, "a", "b")
	healths["a"].latency, healths["b"].latency = 300*time.Millisecond, 40*time.Millisecond
	if got := ranked(Face)[0].name; got != "b" {
		t.Fatalf("first is %s, want the faster b", got)
	}
	skin, err := Fetch(Face, "Steve")
	if err != nil || skin.Provider != "b" || a.hits.Load() != 0 {
		t.Fatalf("got %v from %v, a was asked %d times", err, skin, a.hits.Load())
	}
}

func TestFallsBackWhenAProviderFails(t *testing.T) {
	a, b := &site{status: 500}, &site{}
	install(t, map[string]*site{"a": a, "b": b}, "a", "b")
	skin, err := Fetch(Face, "Steve")
	if err != nil || skin.Provider != "b" {
		t.Fatalf("got %v, %v: want a picture from b", skin, err)
	}
	if _, down := healths["a"].snapshot(); !down {
		t.Fatal("a failed with a 500 and should be marked down for a while")
	}
	if got := ranked(Face)[0].name; got != "b" {
		t.Fatalf("a failing provider is still first: %s", got)
	}
}

func TestMissingPlayerDoesNotMarkProviderDown(t *testing.T) {
	a, b := &site{status: 404}, &site{}
	install(t, map[string]*site{"a": a, "b": b}, "a", "b")
	if _, err := Fetch(Face, "Nobody"); err != nil {
		t.Fatal(err)
	}
	if _, down := healths["a"].snapshot(); down {
		t.Fatal("a 404 is about the player, not the site: a must stay usable")
	}
}

func TestSlowProviderIsRacedByTheNext(t *testing.T) {
	a, b := &site{delay: 2 * time.Second}, &site{}
	install(t, map[string]*site{"a": a, "b": b}, "a", "b")
	started := time.Now()
	skin, err := Fetch(Face, "Steve")
	took := time.Since(started)
	if err != nil || skin.Provider != "b" {
		t.Fatalf("got %v, %v: want b to win the race", skin, err)
	}
	if took > 900*time.Millisecond {
		t.Fatalf("took %v: the next provider should have been started after about 300 ms", took)
	}
}

func TestNotAPictureIsRefused(t *testing.T) {
	a, b := &site{body: []byte("<html>blocked</html>")}, &site{}
	install(t, map[string]*site{"a": a, "b": b}, "a", "b")
	skin, err := Fetch(Body, "Steve")
	if err != nil || skin.Provider != "b" {
		t.Fatalf("a page that is not a PNG must be skipped: %v %v", skin, err)
	}
}

func TestAllProvidersFailing(t *testing.T) {
	a, b := &site{status: 500}, &site{status: 503}
	install(t, map[string]*site{"a": a, "b": b}, "a", "b")
	if _, err := Fetch(Face, "Steve"); err == nil {
		t.Fatal("expected an error when every provider fails")
	}
	before := a.hits.Load() + b.hits.Load()
	if _, err := Fetch(Face, "Steve"); err == nil || a.hits.Load()+b.hits.Load() != before {
		t.Fatal("a name nobody could give is remembered briefly and not asked for again at once")
	}
}

func TestCachedAndSharedDownloads(t *testing.T) {
	a := &site{delay: 100 * time.Millisecond}
	install(t, map[string]*site{"a": a}, "a")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Fetch(Face, "Steve"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := Fetch(Face, "steve"); err != nil { // the cache ignores case
		t.Fatal(err)
	}
	if a.hits.Load() != 1 {
		t.Fatalf("the site was asked %d times for one player, want 1", a.hits.Load())
	}
}

func TestParseAndFetchAll(t *testing.T) {
	for raw, want := range map[string][2]string{
		"https://nmsr.nickac.dev/face/Notch":         {"face", "Notch"},
		"https://nmsr.nickac.dev/fullbody/jeb_":      {"body", "jeb_"},
		"https://nmsr.nickac.dev/face/MHF_Steve":     {"face", "MHF_Steve"},
		"https://example.com/face/Notch":             {},
		"https://nmsr.nickac.dev/bust/Notch":         {},
		"https://nmsr.nickac.dev/face/a/../../admin": {},
	} {
		kind, name, ok := Parse(raw)
		if (want[0] == "") == ok || (ok && (string(kind) != want[0] || name != want[1])) {
			t.Errorf("Parse(%q) = %q %q %v, want %v", raw, kind, name, ok, want)
		}
	}
	a := &site{}
	install(t, map[string]*site{"a": a}, "a")
	got := FetchAll(map[string]string{"x": "https://nmsr.nickac.dev/face/Steve", "y": "https://example.com/face/Steve", "z": "plain text"})
	if len(got) != 1 || got["x"] == "" || got["x"][:22] != "data:image/png;base64," {
		t.Fatalf("FetchAll = %v: only the player picture URL should have become a data URL", got)
	}
}

// A face looks the same from any site, so the fastest wins; a body from a flat renderer is a different picture, so it only comes after the 3D ones.
func TestStyleOnlyMattersForBodies(t *testing.T) {
	install(t, map[string]*site{"slow3d": {}, "fastflat": {}}, "slow3d", "fastflat")
	providers[1].style = 1
	healths["slow3d"].latency, healths["fastflat"].latency = 600*time.Millisecond, 50*time.Millisecond
	if got := ranked(Face)[0].name; got != "fastflat" {
		t.Errorf("for a face the first is %s, want the faster fastflat", got)
	}
	if got := ranked(Body)[0].name; got != "slow3d" {
		t.Errorf("for a body the first is %s, want the 3D one even though it is slower", got)
	}
}
