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
	helpers.Register(WarnRemoveCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// WarnRemoveCommand removes a player's active warning.
var WarnRemoveCommand = &types.Command{
	Name:         "unwarn",
	Requirements: types.PunishmentRequirements("warn_logs"),
	Description:  "Removes an active warning from a player's record.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to unwarn",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for removing the warning",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeWarnRemoveCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeWarnRemoveCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.RevokePunishment(s, i, &helpers.RevokeSpec{
		Command:   "warn_issue", // Use warn_issue for shared alerts
		Action:    "Warning",
		Adjective: "warned",
		Type:      "warn",
	})
}
