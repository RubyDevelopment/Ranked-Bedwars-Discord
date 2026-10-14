package general

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
	helpers.Register(GamePugsCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GamePugsCommand Manages active pickup games (PUGs), lobby rotations, and queue statuses.
var GamePugsCommand = &types.Command{
	Name:        "game_pugs",
	Description: "Manages active pickup games (PUGs), lobby rotations, and queue statuses.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "list",
				Description: "List PUGS division members",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "vote",
				Description: "Start a vote to add a player to PUGS",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "user",
						Description: "The user to vote for",
						Type:        discordgo.ApplicationCommandOptionUser,
						Required:    true,
					},
				},
			},
		},
	},
	Execute: executeGamePugsCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGamePugsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	sub, opts := types.SelectedSubcommand(i)

	switch sub {
	case "list":
		return helpers.EditInteractionEmbed(s, i, pugsListUnavailableEmbed())
	case "vote":
		target := helpers.SubOptionUser(s, opts, "user")
		if target == nil || target.Bot {
			return helpers.EditInteractionEmbed(s, i, pugsInvalidTargetEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, pugsVoteUnavailableEmbed())
	default:
		return helpers.EditInteractionEmbed(s, i, pugsInvalidSubcommandEmbed())
	}
}

// ============================================================================
// Embed Builders
// ============================================================================

func pugsListUnavailableEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not Yet Implemented",
		Description: "The PUGS division list requires additional infrastructure (snapshot system, role management) and is not available yet.",
	})
}

func pugsVoteUnavailableEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not Yet Implemented",
		Description: "The PUGS voting system requires additional infrastructure (voting channels, role management) and is not available yet.",
	})
}

func pugsInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a real player to vote for. Select a member from the command options.",
	})
}

func pugsInvalidSubcommandEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "Use `/game_pugs list` or `/game_pugs vote`.",
	})
}
