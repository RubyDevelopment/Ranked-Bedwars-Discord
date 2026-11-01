package helpers

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestHTMLBannerIsDrawnByTheEngine(t *testing.T) {
	var action, guild string
	var got struct {
		HTML   string                 `json:"html"`
		CSS    string                 `json:"css"`
		Width  int                    `json:"width"`
		Vars   map[string]string      `json:"vars"`
		Assets map[string]Asset       `json:"assets"`
	}
	old := engineControl
	defer func() { engineControl = old }()
	engineControl = func(g, a string, params json.RawMessage) controlResult {
		guild, action = g, a
		_ = json.Unmarshal(params, &got)
		data, _ := json.Marshal(map[string]any{"png": base64.StdEncoding.EncodeToString([]byte("\x89PNG-data")), "warnings": []string{}})
		return controlResult{OK: true, Data: data}
	}

	b := &Banner{ID: "bn-1", Mode: "html", HTML: "<b>{player.name}</b>", CSS: "b{color:red}", Width: 600, Height: 300, guildID: "123456789012345678", assets: map[string]Asset{"a1": {Name: "bg", Data: "data:image/png;base64,AA"}}}
	png, err := RenderBanner(b, Vars{"player.name": "Wiggels"})
	if err != nil || string(png) != "\x89PNG-data" {
		t.Fatalf("png=%q err=%v", png, err)
	}
	if action != "banner.render" || guild != "123456789012345678" || got.HTML != b.HTML || got.CSS != b.CSS || got.Width != 600 || got.Vars["player.name"] != "Wiggels" || got.Assets["a1"].Data == "" {
		t.Fatalf("the engine was sent %s %s %+v", guild, action, got)
	}
}

func TestHTMLBannerFailsWhenTheEngineCannotDrawIt(t *testing.T) {
	old := engineControl
	defer func() { engineControl = old }()
	engineControl = func(string, string, json.RawMessage) controlResult { return controlResult{Error: "The engine is not connected."} }
	if _, err := RenderBanner(&Banner{Mode: "html", Width: 600, Height: 300, guildID: "1"}, Vars{}); err == nil {
		t.Fatal("an html banner the engine cannot draw must fail, so the command falls back")
	}
}
