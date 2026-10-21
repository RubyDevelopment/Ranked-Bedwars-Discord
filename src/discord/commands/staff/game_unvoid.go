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
	helpers.Register(GameUnvoidCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameUnvoidCommand restores a voided game.
var GameUnvoidCommand = &types.Command{
	Name:          "unvoid",
	Requirements:  types.StaffRequirements,
	Description:   "Restores a previously voided game.",
	PrefixAliases: []string{"uvg", "uv"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
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
	Autocomplete: helpers.GameNumberAutocomplete(helpers.GameVoided),
	Execute:      executeGameUnvoidCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameUnvoidCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)
	gameNumber := helpers.SubOptionInt(i.ApplicationCommandData().Options, "game")

	// 1. Validate the Game
	game, ok := helpers.Games.ByNumber(i.GuildID, gameNumber)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, gameUnvoidNotFoundEmbed(gameNumber))
	}
	if game.State != helpers.GameVoided {
		return helpers.EditInteractionEmbed(s, i, gameUnvoidNotVoidedEmbed(gameNumber))
	}

	// 2. Restore the Game
	result, err := helpers.Games.Unvoid(actor, i.GuildID, game.ID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("unvoid the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, gameUnvoidSuccessEmbed(gameNumber, len(result.Results)))
}

// ============================================================================
// Embed Builders
// ============================================================================

func gameUnvoidNotFoundEmbed(gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
	})
}

func gameUnvoidNotVoidedEmbed(gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Voided",
		Description: fmt.Sprintf("Game **#%d** has not been voided, so there is nothing to restore.", gameNumber),
	})
}

func gameUnvoidSuccessEmbed(gameNumber, restored int) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Game Restored",
		Description: fmt.Sprintf("Game **#%d** has been restored from its voided state. This action has been logged for staff review.\n\n> **Players Restored:** `%d`", gameNumber, restored),
	})
}
