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
	helpers.Register(QueuePugsCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// QueuePugsCommand Manages active pickup games (PUGs), lobby rotations, and queue statuses.
var QueuePugsCommand = &types.Command{
	Name:        "pugs",
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
	Execute: executeQueuePugsCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeQueuePugsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	sub, opts := types.SelectedSubcommand(i)

	switch sub {
	case "list":
		return helpers.EditInteractionEmbed(s, i, helpers.PugsListUnavailableEmbed(i))
	case "vote":
		target := helpers.SubOptionUser(s, opts, "user")
		if target == nil || target.Bot {
			return helpers.EditInteractionEmbed(s, i, helpers.PugsInvalidTargetEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.PugsVoteUnavailableEmbed(i))
	default:
		return helpers.EditInteractionEmbed(s, i, helpers.PugsInvalidSubcommandEmbed(i))
	}
}
