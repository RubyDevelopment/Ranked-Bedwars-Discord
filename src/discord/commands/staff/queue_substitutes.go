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
	helpers.Register(QueueSubstitutesCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// QueueSubstitutesCommand swaps a player in a running hosted game for a substitute on the same team.
var QueueSubstitutesCommand = &types.Command{
	Name:         "substitute",
	Requirements: types.StaffRequirements,
	Description:  "Swaps a player in a hosted game for a substitute on the same team.",
	Ephemeral:    true,
	Cooldown:     5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:         "game",
				Description:  "Game number",
				Type:         discordgo.ApplicationCommandOptionInteger,
				Required:     true,
				Autocomplete: true,
			},
			{
				Name:        "out",
				Description: "Player leaving the game",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "in",
				Description: "Substitute taking their place",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Autocomplete: helpers.GameNumberAutocomplete(),
	Execute:      executeQueueSubstitutesCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeQueueSubstitutesCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	opts := i.ApplicationCommandData().Options

	// 1. Validate Players
	outgoing := helpers.SubOptionUser(s, opts, "out")
	incoming := helpers.SubOptionUser(s, opts, "in")
	if outgoing == nil || incoming == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.QueueSubstitutesInvalidTargetEmbed(i))
	}
	if outgoing.Bot || incoming.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.QueueSubstitutesBotTargetEmbed(i))
	}
	if outgoing.ID == incoming.ID {
		return helpers.EditInteractionEmbed(s, i, helpers.QueueSubstitutesSamePlayerEmbed(i))
	}

	// 2. Fetch the Game
	gameNumber := helpers.SubOptionInt(opts, "game")
	game, ok := helpers.Games.ByNumber(i.GuildID, gameNumber)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, helpers.QueueSubstitutesGameNotFoundEmbed(i, gameNumber))
	}

	// 3. Swap the Players
	if err := helpers.Games.ReplacePlayer(i.GuildID, game.ID, outgoing.ID, incoming.ID, incoming.Username); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("substitute the player", err))
	}

	embed := helpers.QueueSubstitutesSuccessEmbed(i, gameNumber, outgoing, incoming)
	helpers.LogCommandResult(s, i.GuildID, "queue_substitutes", helpers.InteractionUserID(i), embed)
	return helpers.EditInteractionEmbed(s, i, embed)
}
