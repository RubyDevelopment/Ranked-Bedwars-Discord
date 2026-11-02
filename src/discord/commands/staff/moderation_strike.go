package staff

import (
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ModerationStrikeCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationStrikeCommand issues an official strike to a player.
var ModerationStrikeCommand = &types.Command{
	Name:         "strike",
	Requirements: types.PunishmentRequirements("strike_logs", "queue_banned_role", "chat_muted_role", "vc_muted_role"),
	Description:  "Issues an official strike penalty to a player.",
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to strike",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the strike",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
			{
				Name:        "type",
				Description: "An optional label for the type of strike",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeModerationStrikeCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeModerationStrikeCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationStrikeInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationStrikeBotTargetEmbed(i))
	}

	if target.ID == helpers.InteractionUserID(i) {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationStrikeInvalidTargetEmbed(i))
	}

	// 2. Validate Reason
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	if reason == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationStrikeReasonRequiredEmbed(i))
	}
	strikeType := strings.TrimSpace(helpers.OptionString(i, "type"))
	actor := helpers.InteractionUserID(i)

	// 3. The ladder picks the step, the ELO is taken, the strike is recorded and the step's punishment applied
	embed, _ := helpers.IssueStrike(s, i.GuildID, actor, target, reason, strikeType)
	return helpers.EditInteractionEmbed(s, i, embed)
}
