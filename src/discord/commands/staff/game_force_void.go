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
	helpers.Register(GameForceVoidCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameForceVoidCommand voids any game by number without requiring participation.
var GameForceVoidCommand = &types.Command{
	Name:          "forcevoid",
	Description:   "Forcefully voids an ongoing or finished game by staff override.",
	Requirements:  []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}},
	PrefixAliases: []string{"fv", "fvg"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
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
	Autocomplete: helpers.GameNumberAutocomplete(),
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
	game, ok := helpers.Games.ByNumber(i.GuildID, gameNumber)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, gameForceVoidNotFoundEmbed(gameNumber))
	}

	// 3. Void the Game
	if err := helpers.Games.Void(actor, i.GuildID, game.ID, reason, helpers.VoidOptions{}); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("void the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, gameForceVoidSuccessEmbed(gameNumber, reason))
}

// ============================================================================
// Embed Builders
// ============================================================================

func gameForceVoidNotFoundEmbed(gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
	})
}

func gameForceVoidSuccessEmbed(gameNumber int, reason string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Game Force Voided",
		Description: fmt.Sprintf("Game **#%d** has been force voided. This action has been logged for staff review.\n\n> **Reason:** %s", gameNumber, reason),
	})
}
