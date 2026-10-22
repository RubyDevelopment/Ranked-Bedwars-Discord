package general

import (
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
	Name:        "pick",
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
		return helpers.EditInteractionEmbed(s, i, helpers.QueuePickNotGameChannelEmbed(i))
	}
	draftView, err := helpers.Games.Draft(game.ID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.QueuePickNoActiveDraftEmbed(i))
	}
	if draftView.Turn < 0 {
		return helpers.EditInteractionEmbed(s, i, helpers.QueuePickDraftCompleteEmbed(i))
	}

	// 2. Resolve the Target Player
	var targetUserID string
	if mentioned := helpers.SubOptionUser(s, opts, "player"); mentioned != nil {
		targetUserID = mentioned.ID
	} else if name := helpers.SubOptionString(opts, "name"); name != "" {
		targetUserID = findPlayerByUsername(game, draftView.Pool, name)
		if targetUserID == "" {
			return helpers.EditInteractionEmbed(s, i, helpers.QueuePickPlayerNotFoundEmbed(i, name))
		}
	} else {
		return helpers.EditInteractionEmbed(s, i, helpers.QueuePickMissingOptionEmbed(i))
	}
	if !slices.Contains(draftView.Pool, targetUserID) {
		return helpers.EditInteractionEmbed(s, i, helpers.QueuePickInvalidSelectionEmbed(i))
	}

	// 3. Make the Pick
	if err := helpers.Games.DraftPick(game.ID, actor, targetUserID); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("complete the pick", err))
	}

	targetUsername := targetUserID
	if picked, ok := game.Player(targetUserID); ok {
		targetUsername = picked.Username
	}
	return helpers.EditInteractionEmbed(s, i, helpers.QueuePickSuccessEmbed(i, targetUsername, actor))
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
