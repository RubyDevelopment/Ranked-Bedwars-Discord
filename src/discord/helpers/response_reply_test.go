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
