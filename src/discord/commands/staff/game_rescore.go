package staff

import (
	"fmt"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(GameRescoreCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameRescoreCommand rescores a game that has already been completed or voided.
var GameRescoreCommand = &types.Command{
	Name:         "rescore",
	Requirements: types.GameRequirements(),
	Description:  "Recalculates ELO for a completed or voided game after a dispute or correction.",
	Cooldown:     5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:         "game",
				Description:  "Game number to rescore",
				Type:         discordgo.ApplicationCommandOptionInteger,
				Required:     true,
				Autocomplete: true,
			},
			{
				Name:        "winner_team",
				Description: "Winning team (Team A, Team B, or team name)",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
			{
				Name:        "mvp",
				Description: "MVP player",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Autocomplete: match.GameNumberAutocomplete(match.GameCleanup, match.GameVoided),
	Execute:      executeGameRescoreCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameRescoreCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)
	opts := i.ApplicationCommandData().Options

	// 1. Validate Options
	gameNumber := helpers.SubOptionInt(opts, "game")
	winnerTeam := helpers.SubOptionString(opts, "winner_team")
	mvpUser := helpers.SubOptionUser(s, opts, "mvp")
	if mvpUser == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameRescoreInvalidMvpEmbed(i))
	}
	if mvpUser.Bot {
		return helpers.EditInteractionEmbed(s, i, GameRescoreBotMvpEmbed(i))
	}

	// 2. Validate the Game
	game, ok, err := match.Games.ByNumberOrStored(i.GuildID, gameNumber)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("look up the game", err))
	}
	if !ok {
		return helpers.EditInteractionEmbed(s, i, GameRescoreNotFoundEmbed(i, gameNumber))
	}
	if game.State != match.GameCleanup && game.State != match.GameVoided {
		return helpers.EditInteractionEmbed(s, i, GameRescoreInvalidStateEmbed(i, gameNumber))
	}
	if _, isParticipant := game.Player(mvpUser.ID); !isParticipant {
		return helpers.EditInteractionEmbed(s, i, helpers.GameRescoreMvpNotInMatchEmbed(i, mvpUser))
	}

	// 3. Rescore the Game
	result, err := match.Games.Rescore(actor, i.GuildID, game.ID, winnerTeam, mvpUser.ID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("rescore the game", err))
	}

	winners, losers := match.SplitResultMentions(result.Results)
	return helpers.EditInteractionEmbed(s, i, GameRescoreSuccessEmbed(i, gameNumber, winners, losers, mvpUser))
}

// ============================================================================
// Helper functions
// ============================================================================

func GameRescoreBotMvpEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid MVP",
		Description: "Automated bot accounts cannot be the MVP. Select a real player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameRescoreNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameRescoreInvalidStateEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid State",
		Description: fmt.Sprintf("Game **#%d** has not finished yet. Only completed or voided games can be rescored; use `/score` for games that are still in progress.", gameNumber),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameRescoreSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, winners, losers string, mvp *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Game Rescored",
		Description: fmt.Sprintf("Game **#%d** has been rescored. This action has been logged for staff review.\n\n> **Winners:** %s\n> **Losers:** %s\n> **MVP:** <@%s>", gameNumber, winners, losers, mvp.ID),
		Thumbnail:   helpers.ThumbDoubleSwords,
	})
}
