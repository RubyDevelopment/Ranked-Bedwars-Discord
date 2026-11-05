//go:build live

package skins

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// Run against the real skin sites: go test -tags live -run Live -v ./src/discord/helpers/skins/
func TestLive(t *testing.T) {
	names := []string{"notmarsett", "BlitzPlayer1", "Oswiecony_", "SMXLC", "MaxGamer8039", "_ea_", "bummyyyyy", "HT206", "hassanwafdy", "sheepcat21", "_Wiggels"}
	urls := map[string]string{}
	for _, n := range names {
		urls["h"+n] = "https://nmsr.nickac.dev/face/" + n
	}

	started := time.Now()
	probe()
	fmt.Println("probe of every provider took", time.Since(started).Round(time.Millisecond))

	started = time.Now()
	got := FetchAll(urls)
	fmt.Printf("11 faces, cold:  %v  (%d of 11 came back)\n", time.Since(started).Round(time.Millisecond), len(got))
	started = time.Now()
	got = FetchAll(urls)
	fmt.Printf("11 faces, warm:  %v  (%d of 11)\n", time.Since(started).Round(time.Millisecond), len(got))

	started = time.Now()
	s, err := Fetch(Body, "Technoblade")
	fmt.Printf("1 body, cold:    %v  via %s (%v)\n", time.Since(started).Round(time.Millisecond), s.Provider, err)

	// The fastest provider goes down: its host is made unreachable, as if the site were offline.
	old := client
	client = &http.Client{Transport: &http.Transport{Proxy: func(r *http.Request) (*url.URL, error) {
		if r.URL.Host == "nmsr.nickac.dev" {
			return url.Parse("http://127.0.0.1:1") // nothing listens here
		}
		return nil, nil
	}}}
	defer func() { client = old }()
	store.mu.Lock()
	store.by = map[string]cached{}
	store.mu.Unlock()
	started = time.Now()
	got = FetchAll(urls)
	fmt.Printf("11 faces with nmsr OFFLINE (cold): %v  (%d of 11)  first answer from %v\n", time.Since(started).Round(time.Millisecond), len(got), func() string { s, _ := Fetch(Face, names[0]); return s.Provider }())
	started = time.Now()
	got = FetchAll(map[string]string{"x": "https://nmsr.nickac.dev/face/Dream", "y": "https://nmsr.nickac.dev/face/Notch"})
	fmt.Printf("2 more faces after nmsr was marked down: %v (%d of 2) - the failed provider is skipped now\n", time.Since(started).Round(time.Millisecond), len(got))
	for _, p := range ranked(Face) {
		l, down := healths[p.name].snapshot()
		fmt.Printf("   %-8s latency %-8v down=%v\n", p.name, l.Round(time.Millisecond), down)
	}
}
