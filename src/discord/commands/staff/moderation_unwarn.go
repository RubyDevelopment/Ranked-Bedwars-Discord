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
	helpers.Register(ModerationUnwarnCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationUnwarnCommand removes a player's active warning.
var ModerationUnwarnCommand = &types.Command{
	Name:         "unwarn",
	Requirements: types.PunishmentRequirements("warn_logs"),
	Description:  "Removes an active warning from a player's record.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: helpers.PunishmentOptions("Discord user to unwarn", "Reason for removing the warning", false, false, false),
	},
	Execute: executeModerationUnwarnCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeModerationUnwarnCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.RevokePunishment(s, i, &helpers.RevokeSpec{
		Command:   "warn",
		Action:    "Warning",
		Adjective: "warned",
		Type:      "warn",
	})
}
