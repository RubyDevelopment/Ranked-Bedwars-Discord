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
	helpers.Register(GameForceVoidCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameForceVoidCommand voids any game by number without requiring participation.
var GameForceVoidCommand = &types.Command{
	Name:          "forcevoid",
	Requirements:  types.GameRequirements(),
	Description:   "Forcefully voids an ongoing or finished game by staff override.",
	PrefixAliases: []string{"fv", "fvg"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{

		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:         "game",
				Description:  "Game number to void",
				Type:         discordgo.ApplicationCommandOptionInteger,
				Required:     true,
				Autocomplete: true,
			},
			{
				Name:        "reason",
				Description: "Reason for voiding",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
				MaxLength:   200,
			},
		},
	},
	Autocomplete: match.ForceVoidGameNumberAutocomplete(),
	Execute:      executeGameForceVoidCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameForceVoidCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)
	opts := i.ApplicationCommandData().Options

	// 1. Parse Options
	gameNumber := helpers.SubOptionInt(opts, "game")
	reason := helpers.SubOptionString(opts, "reason")
	if reason == "" {
		reason = "No reason provided"
	}

	// 2. Fetch the Game
	game, ok, err := match.Games.FindByNumber(i.GuildID, gameNumber)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("find the game", err))
	}
	if !ok {
		return helpers.EditInteractionEmbed(s, i, GameForceVoidNotFoundEmbed(i, gameNumber))
	}

	// 3. Void the Game
	if err := match.Games.Void(actor, i.GuildID, game.ID, reason, match.VoidOptions{}); err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("void the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, GameForceVoidSuccessEmbed(i, gameNumber, reason))
}

// ============================================================================
// Helper functions
// ============================================================================

func GameForceVoidNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameForceVoidSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, reason string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Game Force Voided",
		Description: fmt.Sprintf("Game **#%d** has been force voided. This action has been logged for staff review.\n\n> **Reason:** %s", gameNumber, reason),
		Thumbnail:   helpers.ThumbTrashCan,
	})
}
