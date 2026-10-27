package staff

import (
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
	Name:          "partialvoid",
	Requirements:  types.StaffRequirements,
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
		return helpers.EditInteractionEmbed(s, i, helpers.GamePartialVoidNotFoundEmbed(i, gameNumber))
	}

	// 3. Partially Void the Game
	loserIDs, err := helpers.Games.PartialVoid(actor, i.GuildID, game.ID, winningTeam)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("partially void the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, helpers.GamePartialVoidSuccessEmbed(i, gameNumber, loserIDs))
}
