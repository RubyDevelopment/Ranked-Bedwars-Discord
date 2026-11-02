package staff

import (
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/punish"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ModerationWarnCommand)
	punish.RegisterLadderAction("warning", warnSpec)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationWarnCommand records a formal warning against a player.
var ModerationWarnCommand = &types.Command{
	Name:         "warn",
	Requirements: types.PunishmentRequirements("warn_logs"),
	Description:  "Issues a formal warning to a player for breaking the rules.",
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: punish.PunishmentOptions("Discord user to warn", "Reason for the warning", true, true, false),
	},
	Autocomplete: punish.PunishmentLevelAutocomplete("warning"),
	Execute:      executeModerationWarnCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

var warnSpec = &punish.ApplySpec{
	Command:       "warn",
	Action:        "warn",
	Adjective:     "warned",
	Summary:       "the warning",
	Title:         "Warning Issued",
	Type:          "warn",
	SkipHierarchy: true,
	Ladder:        "warning",
}

func executeModerationWarnCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return punish.ApplyPunishment(s, i, warnSpec)
}
