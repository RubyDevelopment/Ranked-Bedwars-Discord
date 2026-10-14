package general

import (
	"errors"
	"fmt"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

var (
	errCompareNotRegistered = errors.New("player not registered")
	errCompareStatsHidden   = errors.New("player stats hidden")
)

func init() {
	helpers.Register(GameCompareCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameCompareCommand Compares competitive stats, match history, and performance metrics between players.
var GameCompareCommand = &types.Command{
	Name:        "game_compare",
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
	Execute: executeGameCompareCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameCompareCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target Users
	opponent := helpers.OptionUser(s, i, "opponent")
	player := helpers.ResolveUserOption(s, i, "user")
	if opponent == nil || player == nil {
		return helpers.EditInteractionEmbed(s, i, compareInvalidTargetEmbed())
	}
	if opponent.ID == player.ID {
		return helpers.EditInteractionEmbed(s, i, compareSelfComparisonEmbed())
	}
	if opponent.Bot || player.Bot {
		return helpers.EditInteractionEmbed(s, i, compareBotTargetEmbed())
	}

	// 2. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorCompareEmbed())
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, compareNoActiveSeasonEmbed())
	}

	// 3. Fetch Player Profiles & Stats
	isAdmin := helpers.MemberIsAdministrator(s, i)
	executorID := helpers.InteractionUserID(i)

	first, err := loadCompareSide(i.GuildID, seasonID, player, isAdmin || player.ID == executorID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, compareSideErrorEmbed(err, player))
	}
	second, err := loadCompareSide(i.GuildID, seasonID, opponent, isAdmin || opponent.ID == executorID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, compareSideErrorEmbed(err, opponent))
	}

	return helpers.EditInteractionEmbed(s, i, compareOverviewEmbed(seasonKey, first, second))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// compareSide is one player's resolved profile and season stats.
type compareSide struct {
	username string
	stats    *models.PlayerStatsModel
	showElo  bool
}

// loadCompareSide resolves a player's profile and stats, honouring their privacy settings.
func loadCompareSide(guildID string, seasonID uint, user *discordgo.User, bypassPrivacy bool) (*compareSide, error) {
	view, err := database.Must().Player.Get(guildID, user.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return nil, errCompareNotRegistered
		}
		return nil, err
	}
	if !view.Config.IsStatsVisible && !bypassPrivacy {
		return nil, errCompareStatsHidden
	}

	stats, err := database.Must().Stats.GetOrCreate(guildID, sdk.UintToString(view.Player.ID), seasonID)
	if err != nil {
		return nil, err
	}

	username := user.Username
	if view.Config.Username != "" {
		username = view.Config.Username
	}
	return &compareSide{
		username: username,
		stats:    stats,
		showElo:  view.Config.IsEloVisible || bypassPrivacy,
	}, nil
}

// calculateKDR returns kills per death, treating a deathless record as its kill count.
func calculateKDR(kills, deaths int) float64 {
	if deaths == 0 {
		return float64(kills)
	}
	return float64(kills) / float64(deaths)
}

// compareSideField renders one player's column of the comparison.
func compareSideField(side *compareSide) *discordgo.MessageEmbedField {
	elo := "Hidden"
	if side.showElo {
		elo = fmt.Sprintf("%d", side.stats.Elo)
	}

	total := side.stats.Wins + side.stats.Losses
	winRate := 0.0
	if total > 0 {
		winRate = float64(side.stats.Wins) / float64(total) * 100
	}

	return &discordgo.MessageEmbedField{
		Name: side.username,
		Value: fmt.Sprintf(
			"> **ELO:** `%s`\n> **Rank:** `%s`\n> **Record:** `%dW` / `%dL`\n> **Win Rate:** `%.1f%%`\n> **KDR:** `%.2f`",
			elo, side.stats.Rank, side.stats.Wins, side.stats.Losses, winRate, calculateKDR(side.stats.Kills, side.stats.Deaths),
		),
		Inline: true,
	}
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorCompareEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to compare these players. Please contact an administrator for assistance or try again later.",
	})
}

func compareInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify an opponent to compare against. Select a registered player from the command options.",
	})
}

func compareSelfComparisonEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You cannot compare a player against themselves. Select a different opponent.",
	})
}

func compareBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have ranked stats. Target a registered player instead.",
	})
}

func compareNoActiveSeasonEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
	})
}

// compareSideErrorEmbed explains why one side of the comparison could not be loaded.
func compareSideErrorEmbed(err error, user *discordgo.User) *discordgo.MessageEmbed {
	switch err {
	case errCompareNotRegistered:
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Player Not Registered",
			Description: fmt.Sprintf("<@%s> is not registered in this guild. They must run `/player_register` before they can be compared.", user.ID),
		})
	case errCompareStatsHidden:
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Stats Hidden",
			Description: fmt.Sprintf("<@%s> has chosen to hide their stats from other players, so they cannot be compared.", user.ID),
		})
	}
	return unexpectedErrorCompareEmbed()
}

func compareOverviewEmbed(season string, first, second *compareSide) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Player Comparison",
		Description: fmt.Sprintf("Comparing **%s** and **%s** for season `%s`.", first.username, second.username, season),
		Fields:      []*discordgo.MessageEmbedField{compareSideField(first), compareSideField(second)},
	})
}
