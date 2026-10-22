package general

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
	helpers.Register(GameVoidCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameVoidCommand Requests a match void due to disconnects, bugs, or rule infractions.
var GameVoidCommand = &types.Command{
	Name:          "void",
	Description:   "Requests a match void due to disconnects, bugs, or rule infractions.",
	PrefixAliases: []string{"vg", "v"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "reason",
				Description: "Reason for requesting/performing the void",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
			{
				Name:        "match_id",
				Description: "Optional specific Match ID to void",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeGameVoidCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameVoidCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)

	// 1. Validate the Game Channel & Participant
	game, ok := helpers.Games.ByChannel(i.ChannelID)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, helpers.VoidMatchNotGameChannelEmbed(i))
	}
	if _, isParticipant := game.Player(actor); !isParticipant {
		return helpers.EditInteractionEmbed(s, i, helpers.VoidMatchNotInMatchEmbed(i))
	}

	// 2. Validate Staff Permissions
	if !helpers.MemberIsAdministrator(s, i) {
		return helpers.EditInteractionEmbed(s, i, helpers.VoidMatchStaffOnlyEmbed(i))
	}

	// 3. Resolve Reason from Option
	reasonInput := helpers.OptionString(i, "reason")
	reason := fmt.Sprintf("Voided by <@%s> via /game_void: %s", actor, reasonInput)

	// 4. Void the Game
	if err := helpers.Games.Void(actor, i.GuildID, game.ID, reason, helpers.VoidOptions{}); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("void the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, helpers.VoidMatchSuccessEmbed(i, game.Number))
}
