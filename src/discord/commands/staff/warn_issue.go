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
	helpers.Register(WarnIssueCommand)
	helpers.RegisterLadderAction("warning", warnSpec)
}

// ============================================================================
// Command Definition
// ============================================================================

// WarnIssueCommand records a formal warning against a player.
var WarnIssueCommand = &types.Command{
	Name:         "warn",
	Requirements: types.PunishmentRequirements("warn_logs"),
	Description:  "Issues a formal warning to a player for breaking the rules.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to warn",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the warning",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
		},
	},
	Execute: executeWarnIssueCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

var warnSpec = &helpers.ApplySpec{
	Command:       "warn_issue",
	Action:        "warn",
	Adjective:     "warned",
	Summary:       "the warning",
	Title:         "Warning Issued",
	Type:          "warn",
	SkipHierarchy: true,
	Ladder:        "warning",
}

func executeWarnIssueCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.ApplyPunishment(s, i, warnSpec)
}
