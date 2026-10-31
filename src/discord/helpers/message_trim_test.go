package helpers

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestTrimMessageData(t *testing.T) {
	data := &discordgo.InteractionResponseData{
		Content: "  reply \n",
		Embeds: []*discordgo.MessageEmbed{{
			Title:       "  title ",
			Description: "\n description \t",
			Fields:      []*discordgo.MessageEmbedField{{Name: " field ", Value: " value  "}},
		}},
	}

	trimMessageData(data)

	if data.Content != "reply" {
		t.Fatalf("content = %q, want %q", data.Content, "reply")
	}
	if data.Embeds[0].Title != "title" || data.Embeds[0].Description != "description" {
		t.Fatalf("embed text was not trimmed: %#v", data.Embeds[0])
	}
	if data.Embeds[0].Fields[0].Name != "field" || data.Embeds[0].Fields[0].Value != "value" {
		t.Fatalf("embed field text was not trimmed: %#v", data.Embeds[0].Fields[0])
	}
}

func TestTrimPayloadStrings(t *testing.T) {
	payload := map[string]any{
		"content": " message ",
		"components": []map[string]any{
			{"content": " component text "},
		},
	}

	trimPayloadStrings(payload)

	if payload["content"] != "message" {
		t.Fatalf("content = %q, want %q", payload["content"], "message")
	}
	component := payload["components"].([]map[string]any)[0]
	if component["content"] != "component text" {
		t.Fatalf("component content = %q, want %q", component["content"], "component text")
	}
}
