package helpers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeAppearanceChecksBeforeAnything(t *testing.T) {
	call := func(method, auth, body string) int {
		req := httptest.NewRequest(method, "/live/appearance", strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		ServeAppearance(rec, req)
		return rec.Code
	}

	t.Setenv("LIVE_CHAT_SECRET", "")
	if got := call(http.MethodPost, "Bearer x", `{"guild":"123456789012345678"}`); got != http.StatusNotFound {
		t.Errorf("without a secret the endpoint should not exist, got %d", got)
	}

	t.Setenv("LIVE_CHAT_SECRET", "a-long-shared-secret")
	cases := []struct {
		name         string
		method, auth string
		body         string
		want         int
	}{
		{"wrong method", http.MethodGet, "Bearer a-long-shared-secret", "", http.StatusMethodNotAllowed},
		{"no secret given", http.MethodPost, "", `{"guild":"123456789012345678"}`, http.StatusUnauthorized},
		{"wrong secret", http.MethodPost, "Bearer nope", `{"guild":"123456789012345678"}`, http.StatusUnauthorized},
		{"not a server id", http.MethodPost, "Bearer a-long-shared-secret", `{"guild":"../etc"}`, http.StatusBadRequest},
		{"not json", http.MethodPost, "Bearer a-long-shared-secret", `nope`, http.StatusBadRequest},
		{"bot not ready", http.MethodPost, "Bearer a-long-shared-secret", `{"guild":"123456789012345678"}`, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		if got := call(c.method, c.auth, c.body); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
