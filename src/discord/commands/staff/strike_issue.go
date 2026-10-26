package staff

import (
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	cmdcfg "ranked-bedwars/src/discord/config"
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
	Requirements: types.PunishmentRequirements("strike_logs", "queue_banned_role"),
	Description:  "Issues an official strike penalty to a player.",
	Ephemeral:    true,
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
	Execute: executeStrikeIssueCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeStrikeIssueCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeIssueInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeIssueBotTargetEmbed(i))
	}

	// 2. Validate Reason
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	if reason == "" {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeIssueReasonRequiredEmbed(i))
	}
	strikeType := strings.TrimSpace(helpers.OptionString(i, "type"))
	actor := helpers.InteractionUserID(i)

	// 3. Resolve Target Profile
	playerID, profile, err := helpers.EnsureModerationTarget(actor, i.GuildID, target)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorStrikeIssueEmbed(i))
	}

	// 4. Record Strike in Database
	strike, err := database.Must().Strikes.Create(actor, i.GuildID, sdk.UintToString(playerID), sdk.NewStrike{
		StaffID: actor, Reason: reason,
	})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorStrikeIssueEmbed(i))
	}

	username := target.Username
	if profile != nil && profile.Username != "" {
		username = profile.Username
	}
	embed := cmdcfg.StrikeIssueSuccessEmbed(i, strike.ID, target, username, strikeType, reason)
	helpers.LogCommandResult(s, i.GuildID, "strike_issue", actor, embed)
	helpers.SendPunishmentAlert(s, i.GuildID, "strike_issue", "applied", target, username, "strike", "", reason)
	return helpers.EditInteractionEmbed(s, i, embed)
}
