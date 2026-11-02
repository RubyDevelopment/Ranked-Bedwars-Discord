package staff

import (
	"fmt"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/discord/helpers/ratings"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(GameScoreCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameScoreCommand scores a game in progress by marking the winning team.
var GameScoreCommand = &types.Command{
	Name:         "score",
	Requirements: types.GameRequirements(),
	Description:  "Scores a game in progress by marking the winning team.",
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "winner",
				Description: "Winning team (A, B, Team A, ...)",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    true,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Team A", Value: 0},
					{Name: "Team B", Value: 1},
					{Name: "Team C", Value: 2},
					{Name: "Team D", Value: 3},
				},
			},
			{
				Name:         "game",
				Description:  "Game number (default: the game of this channel)",
				Type:         discordgo.ApplicationCommandOptionInteger,
				Required:     false,
				Autocomplete: true,
			},
			{
				Name:        "mvp",
				Description: "The MVP, one of the winners (default: picked from the stats)",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    false,
			},
		},
	},
	Autocomplete: match.GameNumberAutocomplete(match.GameInGame, match.GamePostGame),
	Execute:      executeGameScoreCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameScoreCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)
	opts := i.ApplicationCommandData().Options

	// 1. Parse Options: the game is the one of this channel unless a number is given
	teamName := match.TeamName(helpers.SubOptionInt(opts, "winner"))
	gameNumber := helpers.SubOptionInt(opts, "game")
	var game match.Game
	var ok bool
	if gameNumber > 0 {
		game, ok = match.Games.ByNumber(i.GuildID, gameNumber)
	} else if game, ok = match.Games.ByChannel(i.ChannelID); ok {
		gameNumber = game.Number
	} else {
		return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("Run this in a game's channel, or give the game number."))
	}
	if !ok {
		return helpers.EditInteractionEmbed(s, i, GameScoreNotFoundEmbed(i, gameNumber))
	}

	// 2. The MVP, if one was named, must be a player of the game
	mvpID := ""
	if mvp := helpers.SubOptionUser(s, opts, "mvp"); mvp != nil {
		if _, plays := game.Player(mvp.ID); !plays {
			return helpers.EditInteractionEmbed(s, i, helpers.GameRescoreMvpNotInMatchEmbed(i, mvp))
		}
		mvpID = mvp.ID
	} else if helpers.OptionUserUnresolved(i, "mvp") {
		return helpers.EditInteractionEmbed(s, i, helpers.GameRescoreInvalidMvpEmbed(i))
	}

	// 3. Score the Game
	result, err := match.Games.Score(actor, i.GuildID, game.ID, teamName, match.ScoreOptions{MVP: mvpID})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("score the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, GameScoreSuccessEmbed(i, gameNumber, teamName, result.Results))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// ============================================================================
// Helper functions
// ============================================================================

func GameScoreNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameScoreSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, teamName string, results []ratings.EloResult) *discordgo.MessageEmbed {
	winners, losers := match.SplitResultMentions(results)

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Game Scored",
		Description: fmt.Sprintf("Game **#%d** has been scored. This action has been logged for staff review.\n\n> **Winning Team:** `%s`\n> **Winners:** %s\n> **Losers:** %s", gameNumber, teamName, winners, losers),
		Thumbnail:   helpers.ThumbDoubleSwords,
	})
}
