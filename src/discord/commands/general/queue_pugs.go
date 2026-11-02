package general

import (
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(QueuePugsCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// QueuePugsCommand Manages active pickup games (PUGs), lobby rotations, and queue statuses.
var QueuePugsCommand = &types.Command{
	Name:        "pugs",
	Description: "Manages active pickup games (PUGs), lobby rotations, and queue statuses.",
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
	Execute: executeQueuePugsCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeQueuePugsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	sub, opts := types.SelectedSubcommand(i)

	switch sub {
	case "list":
		return helpers.EditInteractionEmbed(s, i, PugsListUnavailableEmbed(i))
	case "vote":
		target := helpers.SubOptionUser(s, opts, "user")
		if target == nil || target.Bot {
			return helpers.EditInteractionEmbed(s, i, PugsInvalidTargetEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, PugsVoteUnavailableEmbed(i))
	default:
		return helpers.EditInteractionEmbed(s, i, PugsInvalidSubcommandEmbed(i))
	}
}

// ============================================================================
// Helper functions
// ============================================================================

func PugsListUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not Yet Implemented",
		Description: "The PUGS division list requires additional infrastructure (snapshot system, role management) and is not available yet.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PugsVoteUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not Yet Implemented",
		Description: "The PUGS voting system requires additional infrastructure (voting channels, role management) and is not available yet.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PugsInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a real player to vote for. Select a member from the command options.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PugsInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "Use `/pugs list` or `/pugs vote`.",
		Thumbnail:   helpers.ThumbFlag,
	})
}
