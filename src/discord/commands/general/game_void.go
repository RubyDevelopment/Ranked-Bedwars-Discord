package general

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
	game, ok := match.Games.ByChannel(i.ChannelID)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, VoidMatchNotGameChannelEmbed(i))
	}
	if _, isParticipant := game.Player(actor); !isParticipant {
		return helpers.EditInteractionEmbed(s, i, VoidMatchNotInMatchEmbed(i))
	}

	// 2. Vote: three quarters of the players (rounded down) void the game; everyone in the channel sees the count
	votes, needed, _ := match.CastVoidVote(game.ID, actor, len(game.Players))
	if votes < needed {
		// The vote is a message with Agree and Disagree buttons: nobody has to run /void again.
		return helpers.EditInteractionComponents(s, i, match.VoidVotePanel(game), match.VoidVoteButtons(game.ID))
	}

	// 3. Enough votes: void the game
	reason := fmt.Sprintf("Void vote passed (%d of %d players)", votes, len(game.Players))
	if err := match.Games.Void(actor, i.GuildID, game.ID, reason, match.VoidOptions{}); err != nil {
		return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("void the game", err))
	}
	return helpers.EditInteractionEmbed(s, i, VoidMatchSuccessEmbed(i, game.Number))
}

// ============================================================================
// Helper functions
// ============================================================================

func VoidMatchNotGameChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not a Game Channel",
		Description: "This command can only be used in a game text channel. Run it in the text channel of the match you want to void.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func VoidMatchNotInMatchEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not in Match",
		Description: "You must be a participant in this game to void it.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func VoidMatchSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Void Vote Passed",
		Description: fmt.Sprintf("The void vote passed: game **#%d** has been voided. No ELO or stats were recorded.", gameNumber),
		Thumbnail:   helpers.ThumbTrashCan,
	})
}
