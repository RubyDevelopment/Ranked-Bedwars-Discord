package helpers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestLogMessagePreservesStructuredDiscordData(t *testing.T) {
	at := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	editedAt := at.Add(time.Minute)
	message := &discordgo.Message{
		ID:              "message-id",
		Timestamp:       at,
		EditedTimestamp: &editedAt,
		Content:         "message content",
		Pinned:          true,
		Author:          &discordgo.User{ID: "author-id", Username: "author", GlobalName: "Author"},
		Embeds: []*discordgo.MessageEmbed{{
			Title:       "Embed title",
			Description: "Embed description",
			Fields:      []*discordgo.MessageEmbedField{{Name: "Field", Value: "Value"}},
		}},
		Attachments: []*discordgo.MessageAttachment{{
			Filename: "image.png", URL: "https://cdn.example/image.png", ContentType: "image/png", Size: 123,
		}},
		Mentions:  []*discordgo.User{{ID: "mentioned-id", Username: "mentioned"}},
		Reactions: []*discordgo.MessageReactions{{Count: 2, Me: true, Emoji: &discordgo.Emoji{Name: "wave", ID: "emoji-id"}}},
	}

	encoded, err := json.Marshal(logMessage(message))
	if err != nil {
		t.Fatalf("marshal archived message: %v", err)
	}
	var archived DiscordLogMessage
	if err := json.Unmarshal(encoded, &archived); err != nil {
		t.Fatalf("unmarshal archived message: %v", err)
	}

	if archived.Content != "message content" {
		t.Fatalf("message content = %q, want content kept separate from embed text", archived.Content)
	}
	if len(archived.Embeds) != 1 || archived.Embeds[0].Title != "Embed title" || len(archived.Embeds[0].Fields) != 1 {
		t.Fatalf("embed data was not preserved: %+v", archived.Embeds)
	}
	if len(archived.Attachments) != 1 || archived.Attachments[0].Name != "image.png" || archived.Attachments[0].Size != 123 {
		t.Fatalf("attachment metadata was not preserved: %+v", archived.Attachments)
	}
	if archived.Mentions["mentioned-id"] != "mentioned" {
		t.Fatalf("mention data was not preserved: %+v", archived.Mentions)
	}
	if archived.EditedAt == nil || !archived.EditedAt.Equal(editedAt) || !archived.Pinned {
		t.Fatalf("edited and pinned metadata was not preserved: %+v", archived)
	}
	if len(archived.Reactions) != 1 || archived.Reactions[0].Name != "wave" || archived.Reactions[0].Count != 2 || !archived.Reactions[0].Me {
		t.Fatalf("reaction data was not preserved: %+v", archived.Reactions)
	}
}
