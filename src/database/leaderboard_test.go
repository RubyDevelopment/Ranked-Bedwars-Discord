package database

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A leaderboard page comes with the number of ranked players (the SDK's own call loses it), so the bot knows how many pages there are.
func TestLeaderboardReadsTheTotal(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.RequestURI(), r.Header.Get("Authorization")
		w.Header().Set("X-Total-Count", "1190")
		_, _ = w.Write([]byte(`[{"position":11,"id":7,"elo":1234},{"position":12,"id":8,"elo":1200}]`))
	}))
	defer srv.Close()
	apiConn.baseURL, apiConn.apiKey = srv.URL, "secret"

	rows, total, err := Leaderboard("123", 4, 10, 10, "kills")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1190 || len(rows) != 2 || rows[0].Position != 11 {
		t.Fatalf("total %d, %d rows, first position %d", total, len(rows), rows[0].Position)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("authorization %q", gotAuth)
	}
	for _, want := range []string{"/v1/guilds/123/leaderboard?", "season_id=4", "limit=10", "offset=10", "sort=kills"} {
		if !contains(gotPath, want) {
			t.Errorf("request %q lacks %q", gotPath, want)
		}
	}
}

// Without the header, the page still proves how many players there are at least.
func TestLeaderboardWithoutATotalHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"position":21},{"position":22}]`))
	}))
	defer srv.Close()
	apiConn.baseURL, apiConn.apiKey = srv.URL, "k"
	if _, total, err := Leaderboard("1", 1, 10, 20, ""); err != nil || total != 22 {
		t.Fatalf("total %d, error %v: want 22 (2 rows after an offset of 20)", total, err)
	}
}

func TestLeaderboardAPIFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer srv.Close()
	apiConn.baseURL, apiConn.apiKey = srv.URL, "k"
	if _, _, err := Leaderboard("1", 1, 10, 0, ""); err == nil {
		t.Fatal("a 500 must be an error")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
