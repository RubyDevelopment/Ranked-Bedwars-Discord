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
	helpers.Register(GameUnvoidCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameUnvoidCommand restores a voided game.
var GameUnvoidCommand = &types.Command{
	Name:          "unvoid",
	Requirements:  types.GameRequirements(),
	Description:   "Restores a previously voided game.",
	PrefixAliases: []string{"uvg", "uv"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:         "game",
				Description:  "Game number to unvoid",
				Type:         discordgo.ApplicationCommandOptionInteger,
				Required:     true,
				Autocomplete: true,
			},
		},
	},
	Autocomplete: match.GameNumberAutocomplete(match.GameVoided),
	Execute:      executeGameUnvoidCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameUnvoidCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)
	gameNumber := helpers.SubOptionInt(i.ApplicationCommandData().Options, "game")

	// 1. Validate the Game
	game, ok, err := match.Games.ByNumberOrStored(i.GuildID, gameNumber)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("look up the game", err))
	}
	if !ok {
		return helpers.EditInteractionEmbed(s, i, GameUnvoidNotFoundEmbed(i, gameNumber))
	}
	if game.State != match.GameVoided {
		return helpers.EditInteractionEmbed(s, i, GameUnvoidNotVoidedEmbed(i, gameNumber))
	}

	// 2. Restore the Game
	result, err := match.Games.Unvoid(actor, i.GuildID, game.ID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("unvoid the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, GameUnvoidSuccessEmbed(i, gameNumber, len(result.Results)))
}

// ============================================================================
// Helper functions
// ============================================================================

func GameUnvoidNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameUnvoidNotVoidedEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Voided",
		Description: fmt.Sprintf("Game **#%d** has not been voided, so there is nothing to restore.", gameNumber),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameUnvoidSuccessEmbed(i *discordgo.InteractionCreate, gameNumber, restored int) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Game Restored",
		Description: fmt.Sprintf("Game **#%d** has been restored from its voided state. This action has been logged for staff review.\n\n> **Players Restored:** `%d`", gameNumber, restored),
		Thumbnail:   helpers.ThumbLoad,
	})
}
