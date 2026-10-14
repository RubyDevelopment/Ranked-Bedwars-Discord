package staff

import (
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ServerUnmuteCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerUnmuteCommand removes a player's active text chat mute.
var ServerUnmuteCommand = &types.Command{
	Name:         "unmute",
	Description:  "Removes a player's text chat mute.",
	Requirements: []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}, {Type: types.ReqRole, Key: "muted_role"}},
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to chat unmute",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the chat unmute",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeServerUnmuteCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeServerUnmuteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.RevokePunishment(s, i, &helpers.RevokeSpec{
		Command:   "server_mute", // Use server_mute for shared alerts
		RoleKey:   "chat_muted_role",
		Action:    "Chat Mute",
		Adjective: "chat muted",
		Type:      "mute",
	})
}
