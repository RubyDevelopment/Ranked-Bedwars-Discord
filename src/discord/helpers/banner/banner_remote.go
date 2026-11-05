package banner

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// HTML banners (the Banner Builder's HTML and CSS mode) need a layout engine, which the bot does not have, so the engine draws them: the bot
// sends the banner's HTML, CSS, size, uploaded pictures and the values of its variables over the existing engine connection ("banner.render")
// and gets the PNG back. No address or secret to set up; the engine is the same one that hosts the games.

const maxRemotePNG = 8 << 20

// slowEngineBanner is how long an html banner may take before it is written to the log, with how long it took.
const slowEngineBanner = 1500 * time.Millisecond

// RemoteResult is the engine's answer to a banner.render request.
type RemoteResult struct {
	OK    bool
	Data  json.RawMessage
	Error string
}

// EngineControl is how the bot asks the engine to do something; the bot sets it once the engine connection exists.
var EngineControl = func(guild, action string, params json.RawMessage) RemoteResult {
	return RemoteResult{Error: "The engine is not connected."}
}

// renderRemote has the engine draw an html banner. An error means there is no banner (the caller falls back to its embed).
func renderRemote(b *Banner, vars Vars) ([]byte, error) {
	if b.guildID == "" {
		return nil, errors.New("html banner without a guild")
	}
	assets := b.assetsUsed(vars)
	params, err := json.Marshal(map[string]any{"html": b.HTML, "css": b.CSS, "width": b.Width, "height": b.Height, "vars": map[string]string(vars), "assets": assets})
	if err != nil {
		return nil, err
	}
	asked := time.Now()
	res := EngineControl(b.guildID, "banner.render", params)
	if took := time.Since(asked); took > slowEngineBanner {
		log.Printf("html banner %s: the engine took %s to answer (request %d KB)", b.ID, took.Round(time.Millisecond), len(params)/1024)
	}
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

// assetsUsed are the uploaded pictures this banner refers to (`asset:<id>` in its HTML or CSS, or in the value of a variable). A guild's
// document holds every picture of every banner, megabytes of base64, and the engine would otherwise be sent and would parse all of it
// for each banner it draws.
func (b *Banner) assetsUsed(vars Vars) map[string]Asset {
	out := map[string]Asset{}
	if len(b.assets) == 0 {
		return out
	}
	var text strings.Builder
	text.WriteString(b.HTML)
	text.WriteString(b.CSS)
	for _, v := range vars {
		if strings.Contains(v, "asset:") {
			text.WriteString(v)
		}
	}
	all := text.String()
	for id, a := range b.assets {
		if strings.Contains(all, "asset:"+id) {
			out[id] = a
		}
	}
	return out
}
