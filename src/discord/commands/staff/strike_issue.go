package staff

import (
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(StrikeIssueCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// StrikeIssueCommand issues an official strike to a player.
var StrikeIssueCommand = &types.Command{
	Name:         "strike",
	Description:  "Issues an official strike penalty to a player.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Requirements: append(types.ModerationRequirements, types.Requirement{Type: types.ReqRole, Key: "staff_role", Optional: true}),
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
	Execute: executeStrikeIssueCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeStrikeIssueCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, strikeIssueInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, strikeIssueBotTargetEmbed())
	}

	// 2. Validate Reason
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	if reason == "" {
		return helpers.EditInteractionEmbed(s, i, strikeIssueReasonRequiredEmbed())
	}
	strikeType := strings.TrimSpace(helpers.OptionString(i, "type"))
	actor := helpers.InteractionUserID(i)

	// 3. Resolve Target Profile
	playerID, profile, err := helpers.EnsureModerationTarget(actor, i.GuildID, target)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorStrikeIssueEmbed())
	}

	// 4. Record Strike in Database
	strike, err := database.Must().Strikes.Create(actor, i.GuildID, sdk.UintToString(playerID), sdk.NewStrike{
		StaffID: actor, Reason: reason,
	})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorStrikeIssueEmbed())
	}

	username := target.Username
	if profile != nil && profile.Username != "" {
		username = profile.Username
	}
	embed := strikeIssueSuccessEmbed(strike.ID, target, username, strikeType, reason)
	helpers.LogCommandResult(s, i.GuildID, "strike_issue", actor, embed)
	helpers.SendPunishmentAlert(s, i.GuildID, "strike_issue", "applied", target, username, "strike", "", reason)
	return helpers.EditInteractionEmbed(s, i, embed)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorStrikeIssueEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while issuing the strike. Please try again later or contact support.",
	})
}

func strikeIssueInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to strike. Select a registered player from the command options.",
	})
}

func strikeIssueBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive strikes. Target a registered player instead.",
	})
}

func strikeIssueReasonRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Reason Required",
		Description: "You must provide a reason when issuing a strike. Include a clear explanation in the reason field.",
	})
}

func strikeIssueSuccessEmbed(id uint, user *discordgo.User, username, strikeType, reason string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully issued a strike to the player. This action has been logged for staff review.\n\n> **Strike ID:** `#%d`\n> **Player:** <@%s> (**%s**)\n> **Reason:** %s",
		id, user.ID, username, reason,
	)
	if strikeType != "" {
		description += fmt.Sprintf("\n> **Type:** `%s`", strikeType)
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Strike Issued",
		Description: description,
	})
}
