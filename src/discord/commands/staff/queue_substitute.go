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
	helpers.Register(QueueSubstituteCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// QueueSubstituteCommand swaps a player in a running hosted game for a substitute on the same team.
var QueueSubstituteCommand = &types.Command{
	Name:         "substitute",
	Requirements: types.GameRequirements(),
	Description:  "Swaps a player in a hosted game for a substitute on the same team.",
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
	Autocomplete: match.GameNumberAutocomplete(),
	Execute:      executeQueueSubstituteCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeQueueSubstituteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	opts := i.ApplicationCommandData().Options

	// 1. Validate Players
	outgoing := helpers.SubOptionUser(s, opts, "out")
	incoming := helpers.SubOptionUser(s, opts, "in")
	if outgoing == nil || incoming == nil {
		return helpers.EditInteractionEmbed(s, i, QueueSubstituteInvalidTargetEmbed(i))
	}
	if outgoing.Bot || incoming.Bot {
		return helpers.EditInteractionEmbed(s, i, QueueSubstituteBotTargetEmbed(i))
	}
	if outgoing.ID == incoming.ID {
		return helpers.EditInteractionEmbed(s, i, QueueSubstituteSamePlayerEmbed(i))
	}

	// 2. Fetch the Game
	gameNumber := helpers.SubOptionInt(opts, "game")
	game, ok := match.Games.ByNumber(i.GuildID, gameNumber)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, QueueSubstituteGameNotFoundEmbed(i, gameNumber))
	}

	// 3. Swap the Players
	if err := match.Games.ReplacePlayer(i.GuildID, game.ID, outgoing.ID, incoming.ID, incoming.Username); err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("substitute the player", err))
	}

	embed := QueueSubstituteSuccessEmbed(i, gameNumber, outgoing, incoming)
	helpers.LogCommandResult(s, i.GuildID, "queue_substitutes", helpers.InteractionUserID(i), embed)
	return helpers.EditInteractionEmbed(s, i, embed)
}

// ============================================================================
// Helper functions
// ============================================================================

func QueueSubstituteInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify both the player leaving and the substitute taking their place.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func QueueSubstituteBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot play in games. Select real players instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func QueueSubstituteSamePlayerEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "The outgoing player and the substitute must be different players.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func QueueSubstituteGameNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func QueueSubstituteSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, outgoing, incoming *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Substitute Assigned",
		Description: fmt.Sprintf(
			"Successfully swapped the players. This action has been logged for staff review.\n\n> **Game:** `#%d`\n> **Leaving:** <@%s>\n> **Substitute:** <@%s>",
			gameNumber, outgoing.ID, incoming.ID,
		),
		Thumbnail: helpers.ThumbPlayers,
	})
}
