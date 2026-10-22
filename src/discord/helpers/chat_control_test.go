package helpers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func controlRequest(t *testing.T, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/live/control", strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	rec := httptest.NewRecorder()
	LiveChat.ServeControl(rec, req)
	return rec
}

// The console only forwards known actions for a guild id, behind the shared secret, and says so when the engine is away.
func TestServeControl(t *testing.T) {
	t.Setenv("LIVE_CHAT_SECRET", "s3cret")
	good := `{"guild":"123456789012345678","bot":"BotOne","action":"chat","params":{"message":"hi"}}`
	if rec := controlRequest(t, "wrong", good); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret: %d", rec.Code)
	}
	if rec := controlRequest(t, "s3cret", `{"guild":"123456789012345678","action":"rm-rf"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown action: %d", rec.Code)
	}
	if rec := controlRequest(t, "s3cret", `{"guild":"x","action":"chat"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad guild: %d", rec.Code)
	}
	rec := controlRequest(t, "s3cret", good)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "not connected") {
		t.Errorf("engine away: %d %s", rec.Code, rec.Body.String())
	}
	t.Setenv("LIVE_CHAT_SECRET", "")
	if rec := controlRequest(t, "s3cret", good); rec.Code != http.StatusNotFound {
		t.Errorf("feature off: %d", rec.Code)
	}
}
