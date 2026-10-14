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
	helpers.Register(QueueCallCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// QueueCallCommand grants another player access to the invoker's team voice channel.
var QueueCallCommand = &types.Command{
	Name:        "queue_call",
	Description: "Adds a player to your team voice channel.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Player to add",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Execute: executeQueueCallCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeQueueCallCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)

	// 1. Validate Target User
	target := helpers.SubOptionUser(s, i.ApplicationCommandData().Options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, queueCallInvalidTargetEmbed())
	}
	if target.ID == actor {
		return helpers.EditInteractionEmbed(s, i, queueCallSelfTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, queueCallBotTargetEmbed())
	}

	// 2. Fetch the Invoker's Voice State
	voiceState, err := s.State.VoiceState(i.GuildID, actor)
	if err != nil || voiceState == nil || voiceState.ChannelID == "" {
		return helpers.EditInteractionEmbed(s, i, queueCallNotInVoiceEmbed())
	}
	voiceChannelID := voiceState.ChannelID

	// 3. Validate the Game Behind the Voice Channel
	game, ok := helpers.Games.ByChannel(voiceChannelID)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, queueCallNotInGameEmbed())
	}
	if _, isParticipant := game.Player(actor); !isParticipant {
		return helpers.EditInteractionEmbed(s, i, queueCallNotInMatchEmbed())
	}
	if _, targetIsParticipant := game.Player(target.ID); targetIsParticipant {
		return helpers.EditInteractionEmbed(s, i, queueCallAlreadyInMatchEmbed(target))
	}
	if !isTeamVoiceChannel(game, voiceChannelID) {
		return helpers.EditInteractionEmbed(s, i, queueCallWrongChannelEmbed())
	}

	// 4. Verify Bot Permissions
	perms, err := s.State.UserChannelPermissions(s.State.User.ID, voiceChannelID)
	if err != nil || perms&discordgo.PermissionManageRoles == 0 {
		return helpers.EditInteractionEmbed(s, i, queueCallMissingPermissionsEmbed())
	}

	// 5. Grant the Target Access
	err = s.ChannelPermissionSet(voiceChannelID, target.ID, discordgo.PermissionOverwriteTypeMember,
		discordgo.PermissionVoiceConnect|discordgo.PermissionVoiceSpeak, 0)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorQueueCallEmbed())
	}

	return helpers.EditInteractionEmbed(s, i, queueCallSuccessEmbed(target, game.Number))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// isTeamVoiceChannel reports whether the channel is one of the game's team voice channels.
func isTeamVoiceChannel(game helpers.Game, channelID string) bool {
	for _, id := range game.Channels.Voice {
		if id == channelID {
			return true
		}
	}
	return false
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorQueueCallEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to add the player to your team voice channel. Please contact an administrator for assistance or try again later.",
	})
}

func queueCallInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to add to your team voice channel. Select a member from the command options.",
	})
}

func queueCallSelfTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You cannot add yourself to your own team voice channel. Target another player instead.",
	})
}

func queueCallBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be added to voice channels. Target a real player instead.",
	})
}

func queueCallNotInVoiceEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not in Voice",
		Description: "You must be connected to your team voice channel to use this command. Join it and try again.",
	})
}

func queueCallNotInGameEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not in Game",
		Description: "The voice channel you are in does not belong to an active game. Join your team voice channel and try again.",
	})
}

func queueCallNotInMatchEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not in Match",
		Description: "You are not a participant in this game, so you cannot add players to its voice channels.",
	})
}

func queueCallAlreadyInMatchEmbed(target *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already in Match",
		Description: fmt.Sprintf("<@%s> is already a participant in this game and cannot be added again.", target.ID),
	})
}

func queueCallWrongChannelEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Wrong Channel",
		Description: "You are not in your assigned team voice channel. Join it and try again.",
	})
}

func queueCallMissingPermissionsEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Missing Permissions",
		Description: "The bot does not have permission to manage permissions in this voice channel. Please contact an administrator for assistance.",
	})
}

func queueCallSuccessEmbed(target *discordgo.User, gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Voice Access Granted",
		Description: fmt.Sprintf("Successfully added the player to your team voice channel.\n\n> **Player:** <@%s>\n> **Game:** `#%d`", target.ID, gameNumber),
	})
}
