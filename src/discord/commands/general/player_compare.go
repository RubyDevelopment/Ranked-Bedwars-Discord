package general

import (
	"time"

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
	helpers.Register(PlayerCompareCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerCompareCommand Compares competitive stats, match history, and performance metrics between players.
var PlayerCompareCommand = &types.Command{
	Name:        "compare",
	Description: "Compares competitive stats, match history, and performance metrics between players.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "opponent",
				Description: "The Discord user to compare against",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "user",
				Description: "The first Discord user to compare (defaults to you)",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    false,
			},
		},
	},
	Execute: executePlayerCompareCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerCompareCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target Users
	opponent := helpers.OptionUser(s, i, "opponent")
	player := helpers.ResolveUserOption(s, i, "user")
	if opponent == nil || player == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.CompareInvalidTargetEmbed(i))
	}
	if opponent.ID == player.ID {
		return helpers.EditInteractionEmbed(s, i, helpers.CompareSelfComparisonEmbed(i))
	}
	if opponent.Bot || player.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.CompareBotTargetEmbed(i))
	}

	// 2. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorCompareEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.CompareNoActiveSeasonEmbed(i))
	}

	// 3. Fetch Player Profiles & Stats
	isAdmin := helpers.MemberIsAdministrator(s, i)
	executorID := helpers.InteractionUserID(i)

	first, err := loadCompareSide(i.GuildID, seasonID, player, isAdmin || player.ID == executorID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.CompareSideErrorEmbed(i, err, player))
	}
	second, err := loadCompareSide(i.GuildID, seasonID, opponent, isAdmin || opponent.ID == executorID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.CompareSideErrorEmbed(i, err, opponent))
	}

	return helpers.EditInteractionEmbed(s, i, helpers.CompareOverviewEmbed(seasonKey, first, second))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// loadCompareSide resolves a player's profile and stats, honouring their privacy settings.
func loadCompareSide(guildID string, seasonID uint, user *discordgo.User, bypassPrivacy bool) (*helpers.CompareSide, error) {
	view, err := database.Must().Player.Get(guildID, user.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return nil, helpers.ErrCompareNotRegistered
		}
		return nil, err
	}
	if !view.Config.IsStatsVisible && !bypassPrivacy {
		return nil, helpers.ErrCompareStatsHidden
	}

	stats, err := database.Must().Stats.GetOrCreate(guildID, sdk.UintToString(view.Player.ID), seasonID)
	if err != nil {
		return nil, err
	}

	username := user.Username
	if view.Config.Username != "" {
		username = view.Config.Username
	}
	return &helpers.CompareSide{
		Username: username,
		Stats:    stats,
		ShowElo:  view.Config.IsEloVisible || bypassPrivacy,
	}, nil
}

// ============================================================================
// Embed Builders
// ============================================================================
