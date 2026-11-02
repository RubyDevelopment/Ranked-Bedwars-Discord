package staff

import (
	"fmt"
	"strings"
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
	helpers.Register(GamePartialVoidCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GamePartialVoidCommand reverses only the losing team's stats for a game.
var GamePartialVoidCommand = &types.Command{
	Name:          "partialvoid",
	Requirements:  types.GameRequirements(),
	Description:   "Partially voids a game by reversing only the losing team's stats.",
	PrefixAliases: []string{"pvg", "pv"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
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
	Autocomplete: match.GameNumberAutocomplete(match.GameCleanup, match.GameVoided),
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
	game, ok, err := match.Games.ByNumberOrStored(i.GuildID, gameNumber)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("look up the game", err))
	}
	if !ok {
		return helpers.EditInteractionEmbed(s, i, GamePartialVoidNotFoundEmbed(i, gameNumber))
	}

	// 3. Partially Void the Game
	loserIDs, err := match.Games.PartialVoid(actor, i.GuildID, game.ID, winningTeam)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("partially void the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, GamePartialVoidSuccessEmbed(i, gameNumber, loserIDs))
}

// ============================================================================
// Helper functions
// ============================================================================

func GamePartialVoidNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GamePartialVoidSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, loserIDs []string) *discordgo.MessageEmbed {
	mentions := make([]string, len(loserIDs))
	for idx, id := range loserIDs {
		mentions[idx] = fmt.Sprintf("<@%s>", id)
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Game Partially Voided",
		Description: fmt.Sprintf("Game **#%d** has been partially voided. The winning team's stats are preserved. This action has been logged for staff review.\n\n> **Affected Players:** %s", gameNumber, strings.Join(mentions, " ")),
		Thumbnail:   helpers.ThumbTrashCan,
	})
}
