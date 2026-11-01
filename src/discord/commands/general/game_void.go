package general

import (
	"fmt"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
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
	Description:   "Vote to void your game (bugs, disconnects, rule breaks). 3/4 of the players must vote.",
	PrefixAliases: []string{"v"},
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

	// 2. Vote: any player of the game may, and three quarters of the players (rounded down) void it
	reasonInput := helpers.OptionString(i, "reason")
	votes, needed, already := helpers.CastVoidVote(game.ID, actor, len(game.Players))
	if votes < needed {
		embed := helpers.VoidVoteEmbed(actor, votes, needed, already, reasonInput)
		if !already && game.Channels.Text != "" && game.Channels.Text != i.ChannelID {
			_, _ = s.ChannelMessageSendEmbed(game.Channels.Text, embed)
		}
		return helpers.EditInteractionEmbed(s, i, embed)
	}

	// 3. Enough votes: void the game
	reason := fmt.Sprintf("Voted void by %d of %d players (last vote <@%s>): %s", votes, len(game.Players), actor, reasonInput)
	if err := helpers.Games.Void(actor, i.GuildID, game.ID, reason, helpers.VoidOptions{}); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("void the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, helpers.VoidMatchSuccessEmbed(i, game.Number))
}
