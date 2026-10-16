package helpers

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

func TestTicketPanelSendSupportsMessageEmbedAndComponentFormats(t *testing.T) {
	panel := &types.InteractivePanel{
		ID: "support",
		Appearance: types.PanelAppearance{
			Title:       "Support",
			Description: "Choose a category.",
			Footer:      "We are here to help.",
			Color:       0x123456,
		},
		Ticket: &types.TicketPanelConfig{Categories: []*types.TicketCategory{{
			ID:          "general",
			Label:       "General",
			ButtonLabel: "Open ticket",
			Description: "For general questions.",
			ButtonStyle: 1,
		}}},
	}

	t.Run("message", func(t *testing.T) {
		panel.Appearance.Format = "message"
		send := ticketPanelSend(panel, false)
		if !strings.Contains(send.Content, "## Support") || !strings.Contains(send.Content, "For general questions.") {
			t.Fatalf("message content omitted configured panel text: %q", send.Content)
		}
		if send.Flags&discordgo.MessageFlagsIsComponentsV2 != 0 || len(send.Components) == 0 {
			t.Fatal("message format should use standard interactive components without the Components V2 flag")
		}
	})

	t.Run("embed", func(t *testing.T) {
		panel.Appearance.Format = "embed"
		send := ticketPanelSend(panel, false)
		if len(send.Embeds) != 1 || send.Embeds[0].Title != "Support" || len(send.Embeds[0].Fields) != 1 {
			t.Fatalf("embed format did not render its title and category: %#v", send.Embeds)
		}
		if send.Flags&discordgo.MessageFlagsIsComponentsV2 != 0 || len(send.Components) == 0 {
			t.Fatal("embed format should use standard interactive components without the Components V2 flag")
		}
	})

	t.Run("component and legacy default", func(t *testing.T) {
		for _, format := range []string{"component", ""} {
			panel.Appearance.Format = format
			send := ticketPanelSend(panel, false)
			if send.Flags&discordgo.MessageFlagsIsComponentsV2 == 0 || len(send.Components) == 0 {
				t.Fatalf("format %q should render Components V2", format)
			}
		}
	})
}
