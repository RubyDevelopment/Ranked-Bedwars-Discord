package general

import (
	"errors"
	"fmt"
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
		return helpers.EditInteractionEmbed(s, i, CompareInvalidTargetEmbed(i))
	}
	if opponent.ID == player.ID {
		return helpers.EditInteractionEmbed(s, i, CompareSelfComparisonEmbed(i))
	}
	if opponent.Bot || player.Bot {
		return helpers.EditInteractionEmbed(s, i, CompareBotTargetEmbed(i))
	}

	// 2. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorCompareEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, CompareNoActiveSeasonEmbed(i))
	}

	// 3. Fetch Player Profiles & Stats
	isAdmin := helpers.MemberIsAdministrator(s, i)
	executorID := helpers.InteractionUserID(i)

	first, err := loadCompareSide(i.GuildID, seasonID, player, isAdmin || player.ID == executorID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, CompareSideErrorEmbed(i, err, player))
	}
	second, err := loadCompareSide(i.GuildID, seasonID, opponent, isAdmin || opponent.ID == executorID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, CompareSideErrorEmbed(i, err, opponent))
	}

	return helpers.EditInteractionEmbed(s, i, CompareOverviewEmbed(seasonKey, first, second))
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

// ============================================================================
// Helper functions
// ============================================================================

func CompareInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify an opponent to compare against. Select a registered player from the command options.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func CompareSelfComparisonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You cannot compare a player against themselves. Select a different opponent.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func CompareBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have ranked stats. Target a registered player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func CompareNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func UnexpectedErrorCompareEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to compare these players. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

// CompareSideErrorEmbed explains why one side of the comparison could not be loaded.
func CompareSideErrorEmbed(i *discordgo.InteractionCreate, err error, user *discordgo.User) *discordgo.MessageEmbed {
	switch {
	case errors.Is(err, helpers.ErrCompareNotRegistered):
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Player Not Registered",
			Description: fmt.Sprintf("<@%s> is not registered in this guild. They must run `/register` before they can be compared.", user.ID),
			Thumbnail:   helpers.ThumbFlag,
		})
	case errors.Is(err, helpers.ErrCompareStatsHidden):
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Stats Hidden",
			Description: fmt.Sprintf("<@%s> has chosen to hide their stats from other players, so they cannot be compared.", user.ID),
			Thumbnail:   helpers.ThumbFlag,
		})
	}
	return UnexpectedErrorCompareEmbed(i)
}

func CompareOverviewEmbed(season string, first, second *helpers.CompareSide) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Player Comparison",
		Description: fmt.Sprintf("Comparing **%s** and **%s** for season `%s`.", first.Username, second.Username, season),
		Fields:      []*discordgo.MessageEmbedField{helpers.CompareSideField(first), helpers.CompareSideField(second)},
		Thumbnail:   helpers.ThumbPlayers,
	})
}
