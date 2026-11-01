package helpers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
)

// HTML banners (the Banner Builder's HTML and CSS mode) need a layout engine, which the bot does not have, so the engine draws them: the bot
// sends the banner's HTML, CSS, size, uploaded pictures and the values of its variables over the existing engine connection ("banner.render")
// and gets the PNG back. No address or secret to set up; the engine is the same one that hosts the games.

const maxRemotePNG = 8 << 20

// engineControl is how the bot asks the engine to do something. A variable so tests can answer for it.
var engineControl = func(guild, action string, params json.RawMessage) controlResult {
	return LiveChat.Control(guild, "", action, params)
}

// renderRemote has the engine draw an html banner. An error means there is no banner (the caller falls back to its embed).
func renderRemote(b *Banner, vars Vars) ([]byte, error) {
	if b.guildID == "" {
		return nil, errors.New("html banner without a guild")
	}
	assets := map[string]Asset{}
	for id, a := range b.assets {
		assets[id] = a
	}
	params, err := json.Marshal(map[string]any{"html": b.HTML, "css": b.CSS, "width": b.Width, "height": b.Height, "vars": map[string]string(vars), "assets": assets})
	if err != nil {
		return nil, err
	}
	res := engineControl(b.guildID, "banner.render", params)
	if !res.OK {
		msg := res.Error
		if msg == "" {
			msg = "the engine could not draw it"
		}
		return nil, fmt.Errorf("html banner: %s", msg)
	}
	var out struct {
		PNG      string   `json:"png"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		return nil, fmt.Errorf("html banner: %w", err)
	}
	if len(out.Warnings) > 0 {
		log.Printf("html banner %s: %s", b.ID, strings.Join(out.Warnings, "; "))
	}
	png, err := base64.StdEncoding.DecodeString(out.PNG)
	if err != nil || len(png) == 0 || len(png) > maxRemotePNG {
		return nil, errors.New("html banner: the engine did not answer with a picture")
	}
	return png, nil
}
