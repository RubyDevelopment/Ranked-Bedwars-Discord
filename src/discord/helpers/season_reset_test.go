package helpers

import (
	"time"

	"github.com/bwmarrin/discordgo"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func resetCall(secret, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/live/reset", strings.NewReader(body))
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	w := httptest.NewRecorder()
	ServeReset(w, r)
	return w
}

func TestServeReset(t *testing.T) {
	t.Setenv("LIVE_CHAT_SECRET", "0123456789abcdef")
	if w := resetCall("wrong", `{"guild":"123456789012345678","action":"status"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", w.Code)
	}
	if w := resetCall("0123456789abcdef", `{"guild":"nope","action":"status"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad guild: %d", w.Code)
	}
	if w := resetCall("0123456789abcdef", `{"guild":"123456789012345678","action":"boom"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad action: %d", w.Code)
	}
	w := resetCall("0123456789abcdef", `{"guild":"123456789012345678","action":"status"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"idle"`) || !strings.Contains(w.Body.String(), `"recent":[]`) {
		t.Fatalf("idle status: %d %s", w.Code, w.Body.String())
	}
	// No Discord session yet: start is refused instead of running.
	if w := resetCall("0123456789abcdef", `{"guild":"123456789012345678","action":"start"}`); w.Code != http.StatusConflict {
		t.Fatalf("start without session: %d", w.Code)
	}
}

func TestResetLimitAndBusy(t *testing.T) {
	t.Setenv("LIVE_CHAT_SECRET", "0123456789abcdef")
	appearanceSession.Store(&discordgo.Session{})
	t.Cleanup(func() { appearanceSession.Store(nil) })
	const guild = "223456789012345678"
	body := `{"guild":"` + guild + `","action":"start"}`

	// Already running: refused with 409, and it does not use up a slot.
	running := &resetJob{cancel: make(chan struct{}), st: ResetStatus{State: "running"}}
	resetJobs.Store(guild, running)
	if w := resetCall("0123456789abcdef", body); w.Code != http.StatusConflict {
		t.Fatalf("running: %d %s", w.Code, w.Body.String())
	}
	if len(resetStarts[guild]) != 0 {
		t.Fatal("a refused start used a slot")
	}

	// Cancel flips the job to cancelling and closes its channel (twice is safe).
	for i := 0; i < 2; i++ {
		w := resetCall("0123456789abcdef", `{"guild":"`+guild+`","action":"cancel"}`)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"cancelling":true`) {
			t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
		}
	}
	if !running.cancelled() {
		t.Fatal("cancel did not signal the job")
	}

	// Three starts already in the window: refused with 429 and the status says when a slot frees.
	running.st.State = "done"
	now := time.Now()
	resetStarts[guild] = []time.Time{now.Add(-25 * time.Minute), now.Add(-10 * time.Minute), now.Add(-time.Minute)}
	if w := resetCall("0123456789abcdef", body); w.Code != http.StatusTooManyRequests {
		t.Fatalf("limit: %d %s", w.Code, w.Body.String())
	}
	w := resetCall("0123456789abcdef", `{"guild":"`+guild+`","action":"status"}`)
	if !strings.Contains(w.Body.String(), `"startsLeft":0`) || !strings.Contains(w.Body.String(), `"nextSlotAt"`) {
		t.Fatalf("status quota: %s", w.Body.String())
	}

	// The oldest start ages out of the window: a slot is free again.
	resetStarts[guild][0] = now.Add(-31 * time.Minute)
	resetMu.Lock()
	left, _ := resetQuota(guild, time.Now())
	resetMu.Unlock()
	if left != 1 {
		t.Fatalf("expected 1 slot left, got %d", left)
	}
}
