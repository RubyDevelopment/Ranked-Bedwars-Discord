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
		return helpers.EditInteractionEmbed(s, i, cmdcfg.GameUnvoidNotFoundEmbed(i, gameNumber))
	}
	if game.State != helpers.GameVoided {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.GameUnvoidNotVoidedEmbed(i, gameNumber))
	}

	// 2. Restore the Game
	result, err := helpers.Games.Unvoid(actor, i.GuildID, game.ID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("unvoid the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, cmdcfg.GameUnvoidSuccessEmbed(i, gameNumber, len(result.Results)))
}
