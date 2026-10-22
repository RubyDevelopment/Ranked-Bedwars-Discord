package helpers

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLiveChatOnlyReachesItsViewers(t *testing.T) {
	c := &ChatStream{viewers: map[*chatViewer]struct{}{}, bots: map[string][]string{}}
	one, stopOne := c.Subscribe("11111", "BotOne")
	all, stopAll := c.Subscribe("11111", "")
	other, stopOther := c.Subscribe("22222", "")
	defer stopAll()
	defer stopOther()

	c.Line("11111", "BotOne", "hi", "§ahi", 1)
	c.Line("11111", "BotTwo", "yo", "yo", 2)
	count := func(v *chatViewer) (n int) {
		for {
			select {
			case e := <-v.out:
				if e.name == "line" {
					n++
				}
			default:
				return
			}
		}
	}
	if got := count(one); got != 1 {
		t.Errorf("a viewer of one bot got %d lines, want 1", got)
	}
	if got := count(all); got != 2 {
		t.Errorf("a viewer of the guild got %d lines, want 2", got)
	}
	if got := count(other); got != 0 {
		t.Errorf("another guild's viewer got %d lines, want 0", got)
	}

	stopOne()
	stopOne() // leaving twice is harmless
	c.mu.Lock()
	left := len(c.viewers)
	c.mu.Unlock()
	if left != 2 {
		t.Errorf("%d viewers left, want 2", left)
	}
}

func TestLiveChatHTTP(t *testing.T) {
	t.Setenv("LIVE_CHAT_SECRET", "s3cret")
	c := &ChatStream{viewers: map[*chatViewer]struct{}{}, bots: map[string][]string{}}
	srv := httptest.NewServer(c)
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"?guild=12345", nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no secret: %v %v", resp, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req, _ = http.NewRequestWithContext(ctx, "GET", srv.URL+"?guild=12345&bot=Bot", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(resp.Body)
	first, _ := r.ReadString('\n')
	if !strings.HasPrefix(first, "event: status") {
		t.Fatalf("first event = %q", first)
	}
	c.Line("12345", "Bot", "hello", "hello", 5)
	deadline := time.After(2 * time.Second)
	got := make(chan string, 1)
	go func() {
		for {
			l, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if strings.Contains(l, `"text":"hello"`) {
				got <- l
				return
			}
		}
	}()
	select {
	case <-got:
	case <-deadline:
		t.Fatal("the line never arrived")
	}

	cancel() // the dashboard hangs up
	for i := 0; i < 50; i++ {
		c.mu.Lock()
		n := len(c.viewers)
		c.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the viewer stayed registered after the stream closed")
}
