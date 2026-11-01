package helpers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTMLBannerIsDrawnByTheDashboard(t *testing.T) {
	var got struct {
		Guild  string            `json:"guild_id"`
		Banner string            `json:"banner_id"`
		Vars   map[string]string `json:"vars"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s3cret-s3cret-s3cret" || r.URL.Path != "/api/v1/banners/render" {
			http.Error(w, `{"error":"no"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG-data"))
	}))
	defer srv.Close()
	t.Setenv("BANNER_RENDER_URL", srv.URL+"/")
	t.Setenv("BANNER_RENDER_SECRET", "s3cret-s3cret-s3cret")

	b := &Banner{ID: "bn-1", Mode: "html", Width: 600, Height: 300, guildID: "123456789012345678"}
	png, err := RenderBanner(b, Vars{"player.name": "Wiggels"})
	if err != nil || string(png) != "\x89PNG-data" {
		t.Fatalf("png=%q err=%v", png, err)
	}
	if got.Guild != "123456789012345678" || got.Banner != "bn-1" || got.Vars["player.name"] != "Wiggels" {
		t.Fatalf("the dashboard was sent %+v", got)
	}
}

func TestHTMLBannerNeedsTheRenderSettings(t *testing.T) {
	t.Setenv("BANNER_RENDER_URL", "")
	t.Setenv("BANNER_RENDER_SECRET", "")
	if _, err := RenderBanner(&Banner{Mode: "html", Width: 600, Height: 300, guildID: "1"}, Vars{}); err == nil {
		t.Fatal("an html banner with no render settings must fail, so the command falls back")
	}
}
