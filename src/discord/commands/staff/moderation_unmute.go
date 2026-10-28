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
	helpers.Register(ModerationUnmuteCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationUnmuteCommand removes a player's active text chat mute.
var ModerationUnmuteCommand = &types.Command{
	Name:         "unmute",
	Requirements: types.PunishmentRequirements("chat_mute_logs", "chat_muted_role"),
	Description:  "Removes a player's text chat mute.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: helpers.PunishmentOptions("Discord user to chat unmute", "Reason for the chat unmute", false, false, false),
	},
	Execute: executeModerationUnmuteCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeModerationUnmuteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.RevokePunishment(s, i, &helpers.RevokeSpec{
		Command:   "mute",
		Ladder:    "chat_mute",
		RoleKey:   "chat_muted_role",
		Action:    "Chat Mute",
		Adjective: "chat muted",
		Type:      "mute",
	})
}
