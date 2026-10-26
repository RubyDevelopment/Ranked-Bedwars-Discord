package staff

import (
	"time"

	"github.com/bwmarrin/discordgo"

	cmdcfg "ranked-bedwars/src/discord/config"
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
	Requirements:  types.StaffRequirements,
	Description:   "Forcefully voids an ongoing or finished game by staff override.",
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
	Autocomplete: helpers.ForceVoidGameNumberAutocomplete(),
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
	game, ok, err := helpers.Games.FindByNumber(i.GuildID, gameNumber)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("find the game", err))
	}
	if !ok {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.GameForceVoidNotFoundEmbed(i, gameNumber))
	}

	// 3. Void the Game
	if err := helpers.Games.Void(actor, i.GuildID, game.ID, reason, helpers.VoidOptions{}); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("void the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, cmdcfg.GameForceVoidSuccessEmbed(i, gameNumber, reason))
}
