package general

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(QueuePickCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// QueuePickCommand picks a player from the pool during a captain draft.
var QueuePickCommand = &types.Command{
	Name:        "queue_pick",
	Description: "Picks a player during the captain draft phase (captains only).",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "player",
				Description: "Player to pick by mention",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    false,
			},
			{
				Name:        "name",
				Description: "Username of the player to pick",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeQueuePickCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeQueuePickCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)
	opts := i.ApplicationCommandData().Options

	// 1. Validate the Game Channel & Draft
	game, ok := helpers.Games.ByChannel(i.ChannelID)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, queuePickNotGameChannelEmbed())
	}
	draftView, err := helpers.Games.Draft(game.ID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, queuePickNoActiveDraftEmbed())
	}
	if draftView.Turn < 0 {
		return helpers.EditInteractionEmbed(s, i, queuePickDraftCompleteEmbed())
	}

	// 2. Resolve the Target Player
	var targetUserID string
	if mentioned := helpers.SubOptionUser(s, opts, "player"); mentioned != nil {
		targetUserID = mentioned.ID
	} else if name := helpers.SubOptionString(opts, "name"); name != "" {
		targetUserID = findPlayerByUsername(game, draftView.Pool, name)
		if targetUserID == "" {
			return helpers.EditInteractionEmbed(s, i, queuePickPlayerNotFoundEmbed(name))
		}
	} else {
		return helpers.EditInteractionEmbed(s, i, queuePickMissingOptionEmbed())
	}
	if !slices.Contains(draftView.Pool, targetUserID) {
		return helpers.EditInteractionEmbed(s, i, queuePickInvalidSelectionEmbed())
	}

	// 3. Make the Pick
	if err := helpers.Games.DraftPick(game.ID, actor, targetUserID); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("complete the pick", err))
	}

	targetUsername := targetUserID
	if picked, ok := game.Player(targetUserID); ok {
		targetUsername = picked.Username
	}
	return helpers.EditInteractionEmbed(s, i, queuePickSuccessEmbed(targetUsername, actor))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// findPlayerByUsername returns the user ID of the pool player with the given username, or "" when none match.
func findPlayerByUsername(game helpers.Game, pool []string, username string) string {
	input := strings.TrimSpace(username)
	for _, p := range game.Players {
		if slices.Contains(pool, p.UserID) && strings.EqualFold(p.Username, input) {
			return p.UserID
		}
	}
	return ""
}

// ============================================================================
// Embed Builders
// ============================================================================

func queuePickNotGameChannelEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not a Game Channel",
		Description: "This command can only be used in a game text channel. Run it in the text channel of the game you are drafting for.",
	})
}

func queuePickNoActiveDraftEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Draft",
		Description: "There is no active captain draft for this game. If you think this is an error, please contact an administrator for assistance.",
	})
}

func queuePickDraftCompleteEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Draft Complete",
		Description: "The captain draft for this game is already complete. No more players can be picked.",
	})
}

func queuePickMissingOptionEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Missing Option",
		Description: "You must provide either a player mention or a username to pick.",
	})
}

func queuePickPlayerNotFoundEmbed(name string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: fmt.Sprintf("Could not find a player in the pick pool matching **%s**. Check the spelling or mention the player instead.", name),
	})
}

func queuePickInvalidSelectionEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Selection",
		Description: "The selected player is not in the available pick pool. Choose a player who has not been picked yet.",
	})
}

func queuePickSuccessEmbed(username, captainID string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Player Picked",
		Description: fmt.Sprintf("Successfully drafted the player onto the team.\n\n> **Player:** `%s`\n> **Captain:** <@%s>", username, captainID),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
