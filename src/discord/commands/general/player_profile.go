package general

import (
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(PlayerProfileCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerProfileCommand shows a player's ranked profile, username, nickname, and rank.
var PlayerProfileCommand = &types.Command{
	Name:        "profile",
	Description: "Displays a player's ranked profile, username, nickname, and rank.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to view (defaults to you)",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    false,
			},
		},
	},
	Execute: executePlayerProfileCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerProfileCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.ResolveUserOption(s, i, "user")
	if target == nil || target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerProfileBotTargetEmbed(i))
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerProfileNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerProfileEmbed(i))
	}
	config := view.Config

	// 3. Check Privacy Settings
	isSelf := target.ID == helpers.InteractionUserID(i)
	isAdmin := helpers.MemberIsAdministrator(s, i)
	if !config.IsStatsVisible && !isSelf && !isAdmin {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerProfilePrivacyEmbed(i, config.Username))
	}

	// 4. Fetch Season Stats
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerProfileEmbed(i))
	}

	var stats *models.PlayerStatsModel
	if seasonKey != "" {
		stats, err = database.Must().Stats.GetOrCreate(i.GuildID, sdk.UintToString(view.Player.ID), seasonID)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerProfileEmbed(i))
		}
	}

	showElo := config.IsEloVisible || isSelf || isAdmin
	return helpers.EditInteractionEmbed(s, i, helpers.PlayerProfileOverviewEmbed(i, target, config, stats, seasonKey, showElo))
}
