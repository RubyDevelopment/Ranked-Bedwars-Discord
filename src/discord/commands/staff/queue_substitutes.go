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
	helpers.Register(QueueSubstitutesCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// QueueSubstitutesCommand swaps a player in a running hosted game for a substitute on the same team.
var QueueSubstitutesCommand = &types.Command{
	Name:        "queue_substitutes",
	Description: "Swaps a player in a hosted game for a substitute on the same team.",
	Ephemeral:   true,
	Cooldown:    5 * time.Second,
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
		return helpers.EditInteractionEmbed(s, i, queueSubstitutesInvalidTargetEmbed())
	}
	if outgoing.Bot || incoming.Bot {
		return helpers.EditInteractionEmbed(s, i, queueSubstitutesBotTargetEmbed())
	}
	if outgoing.ID == incoming.ID {
		return helpers.EditInteractionEmbed(s, i, queueSubstitutesSamePlayerEmbed())
	}

	// 2. Fetch the Game
	gameNumber := helpers.SubOptionInt(opts, "game")
	game, ok := helpers.Games.ByNumber(i.GuildID, gameNumber)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, queueSubstitutesGameNotFoundEmbed(gameNumber))
	}

	// 3. Swap the Players
	if err := helpers.Games.ReplacePlayer(i.GuildID, game.ID, outgoing.ID, incoming.ID, incoming.Username); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("substitute the player", err))
	}

	embed := queueSubstitutesSuccessEmbed(gameNumber, outgoing, incoming)
	helpers.LogCommandResult(s, i.GuildID, "queue_substitutes", helpers.InteractionUserID(i), embed)
	return helpers.EditInteractionEmbed(s, i, embed)
}

// ============================================================================
// Embed Builders
// ============================================================================

func queueSubstitutesInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify both the player leaving and the substitute taking their place.",
	})
}

func queueSubstitutesBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot play in games. Select real players instead.",
	})
}

func queueSubstitutesSamePlayerEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "The outgoing player and the substitute must be different players.",
	})
}

func queueSubstitutesGameNotFoundEmbed(gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
	})
}

func queueSubstitutesSuccessEmbed(gameNumber int, outgoing, incoming *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Substitute Assigned",
		Description: fmt.Sprintf(
			"Successfully swapped the players. This action has been logged for staff review.\n\n> **Game:** `#%d`\n> **Leaving:** <@%s>\n> **Substitute:** <@%s>",
			gameNumber, outgoing.ID, incoming.ID,
		),
	})
}
