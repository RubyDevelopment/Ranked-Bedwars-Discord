package helpers

import (
	"testing"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

// A rule that carries a dashboard-built reply answers without any saved message, with the response's variables filled in.
func TestResponseRuleWithReply(t *testing.T) {
	st := &responseState{cfg: ResponseConfig{Overrides: map[string]ResponseRule{
		"bots.*": {Reply: &types.ResponseReply{Type: "embed", Embeds: []Embed{{Title: "{response.title}!", Description: "{response.description}"}}}},
	}}}
	emb := &discordgo.MessageEmbed{Title: "No Registered Bots", Description: "none", Color: ColorError}
	payload, ok := buildResponsePayload(st, "bots", "", emb, map[string]string{})
	if !ok {
		t.Fatal("rule with a reply was not used")
	}
	e := payload["embeds"].([]map[string]any)[0]
	if e["title"] != "No Registered Bots!" || e["description"] != "none" {
		t.Errorf("embed %v", e)
	}

	st.cfg.Overrides["bots.*"] = ResponseRule{Reply: &types.ResponseReply{Type: "component"}}
	if p, _ := buildResponsePayload(st, "bots", "", emb, map[string]string{}); p["flags"] != V2Flag {
		t.Errorf("component reply is not Components V2: %v", p["flags"])
	}
}

// A switched-off rule is skipped: the next broader rule (here the look's default) answers.
func TestDisabledRuleFallsThrough(t *testing.T) {
	off := false
	cfg := ResponseConfig{
		Overrides: map[string]ResponseRule{"bots.*": {Reply: &types.ResponseReply{Type: "message", Content: "command"}, Enabled: &off}},
		Defaults:  map[string]ResponseRule{"error": {Reply: &types.ResponseReply{Type: "message", Content: "style"}}},
	}
	rule, ok := cfg.Resolve("bots", "no-registered-bots", "error")
	if !ok || rule.Reply.Content != "style" {
		t.Errorf("resolved %+v", rule)
	}
}

// Channel names can use the game's season number, its number among all the guild's games, and its unique id.
func TestExpandGameNameNumbers(t *testing.T) {
	g := Game{ID: 91, Number: 4, Total: 212, Mode: types.Mode{Name: "Solo"}}
	got := expandGameName("g{game}-s{season_game}-t{total_game}-id{game_id}-{mode}", g, "")
	if got != "g4-s4-t212-id91-Solo" {
		t.Errorf("got %q", got)
	}
}
