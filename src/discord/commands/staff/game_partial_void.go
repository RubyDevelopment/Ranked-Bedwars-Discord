package staff

import (
	"fmt"
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
	helpers.Register(GamePartialVoidCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GamePartialVoidCommand reverses only the losing team's stats for a game.
var GamePartialVoidCommand = &types.Command{
	Name:        "partialvoid",
	Description: "Partially voids a game by reversing only the losing team's stats.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:         "game",
				Description:  "Game number to partially void",
				Type:         discordgo.ApplicationCommandOptionInteger,
				Required:     true,
				Autocomplete: true,
			},
			{
				Name:        "winning_team",
				Description: "Winning team (to preserve their stats)",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Autocomplete: helpers.GameNumberAutocomplete(helpers.GameCleanup, helpers.GameVoided),
	Execute:      executeGamePartialVoidCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGamePartialVoidCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)
	opts := i.ApplicationCommandData().Options

	// 1. Parse Options
	gameNumber := helpers.SubOptionInt(opts, "game")
	winningTeam := helpers.SubOptionString(opts, "winning_team")

	// 2. Fetch the Game
	game, ok := helpers.Games.ByNumber(i.GuildID, gameNumber)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, gamePartialVoidNotFoundEmbed(gameNumber))
	}

	// 3. Partially Void the Game
	loserIDs, err := helpers.Games.PartialVoid(actor, i.GuildID, game.ID, winningTeam)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("partially void the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, gamePartialVoidSuccessEmbed(gameNumber, loserIDs))
}

// ============================================================================
// Embed Builders
// ============================================================================

func gamePartialVoidNotFoundEmbed(gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
	})
}

func gamePartialVoidSuccessEmbed(gameNumber int, loserIDs []string) *discordgo.MessageEmbed {
	mentions := make([]string, len(loserIDs))
	for idx, id := range loserIDs {
		mentions[idx] = fmt.Sprintf("<@%s>", id)
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Game Partially Voided",
		Description: fmt.Sprintf("Game **#%d** has been partially voided. The winning team's stats are preserved. This action has been logged for staff review.\n\n> **Affected Players:** %s", gameNumber, strings.Join(mentions, " ")),
	})
}
