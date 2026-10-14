package staff

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
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
	Name:        "rescore",
	Description: "Recalculates ELO for a completed or voided game after a dispute or correction.",
	Ephemeral:   true,
	Cooldown:    5 * time.Second,
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
	Autocomplete: helpers.GameNumberAutocomplete(helpers.GameCleanup, helpers.GameVoided),
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
		return helpers.EditInteractionEmbed(s, i, gameRescoreInvalidMvpEmbed())
	}
	if mvpUser.Bot {
		return helpers.EditInteractionEmbed(s, i, gameRescoreBotMvpEmbed())
	}

	// 2. Validate the Game
	game, ok := helpers.Games.ByNumber(i.GuildID, gameNumber)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, gameRescoreNotFoundEmbed(gameNumber))
	}
	if game.State != helpers.GameCleanup && game.State != helpers.GameVoided {
		return helpers.EditInteractionEmbed(s, i, gameRescoreInvalidStateEmbed(gameNumber))
	}
	if _, isParticipant := game.Player(mvpUser.ID); !isParticipant {
		return helpers.EditInteractionEmbed(s, i, gameRescoreMvpNotInMatchEmbed(mvpUser))
	}

	// 3. Rescore the Game
	result, err := helpers.Games.Rescore(actor, i.GuildID, game.ID, winnerTeam, mvpUser.ID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("rescore the game", err))
	}

	winners, losers := splitResultMentions(result.Results)
	return helpers.EditInteractionEmbed(s, i, gameRescoreSuccessEmbed(gameNumber, winners, losers, mvpUser))
}

// ============================================================================
// Embed Builders
// ============================================================================

func gameRescoreInvalidMvpEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid MVP",
		Description: "You must specify the MVP of the game. Select a player from the command options.",
	})
}

func gameRescoreBotMvpEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid MVP",
		Description: "Automated bot accounts cannot be the MVP. Select a real player instead.",
	})
}

func gameRescoreNotFoundEmbed(gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
	})
}

func gameRescoreInvalidStateEmbed(gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid State",
		Description: fmt.Sprintf("Game **#%d** has not finished yet. Only completed or voided games can be rescored; use `/game_score` for games that are still in progress.", gameNumber),
	})
}

func gameRescoreMvpNotInMatchEmbed(mvp *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid MVP",
		Description: fmt.Sprintf("<@%s> was not a participant in this game. The MVP must be a player who took part in the match.", mvp.ID),
	})
}

func gameRescoreSuccessEmbed(gameNumber int, winners, losers string, mvp *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Game Rescored",
		Description: fmt.Sprintf("Game **#%d** has been rescored. This action has been logged for staff review.\n\n> **Winners:** %s\n> **Losers:** %s\n> **MVP:** <@%s>", gameNumber, winners, losers, mvp.ID),
	})
}
