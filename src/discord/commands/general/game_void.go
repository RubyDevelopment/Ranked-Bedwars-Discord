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

// GameVoidCommand lets the players of a game vote to void it, in the game's own channel. No reason or match id is asked for.
var GameVoidCommand = &types.Command{
	Name:          "void",
	Description:   "Vote to void this game. 3/4 of the players must vote.",
	PrefixAliases: []string{"v"},
	Cooldown:      3 * time.Second,
	Data:          &discordgo.ApplicationCommand{},
	Execute:       executeGameVoidCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameVoidCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)

	// 1. Only in a game's channel, and only for its players
	game, ok := helpers.Games.ByChannel(i.ChannelID)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, helpers.VoidMatchNotGameChannelEmbed(i))
	}
	if _, isParticipant := game.Player(actor); !isParticipant {
		return helpers.EditInteractionEmbed(s, i, helpers.VoidMatchNotInMatchEmbed(i))
	}

	// 2. Vote: three quarters of the players (rounded down) void the game; everyone in the channel sees the count
	votes, needed, already := helpers.CastVoidVote(game.ID, actor, len(game.Players))
	if votes < needed {
		return helpers.EditInteractionEmbed(s, i, helpers.VoidVoteEmbed(actor, votes, needed, already))
	}

	// 3. Enough votes: void the game
	reason := fmt.Sprintf("Void vote passed (%d of %d players)", votes, len(game.Players))
	if err := helpers.Games.Void(actor, i.GuildID, game.ID, reason, helpers.VoidOptions{}); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("void the game", err))
	}
	return helpers.EditInteractionEmbed(s, i, helpers.VoidMatchSuccessEmbed(i, game.Number))
}
