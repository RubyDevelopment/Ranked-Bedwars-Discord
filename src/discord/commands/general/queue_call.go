package general

import (
	"time"

	"github.com/bwmarrin/discordgo"

	cmdcfg "ranked-bedwars/src/discord/config"
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
	Name:        "call",
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
		return helpers.EditInteractionEmbed(s, i, cmdcfg.QueueCallInvalidTargetEmbed(i))
	}
	if target.ID == actor {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.QueueCallSelfTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.QueueCallBotTargetEmbed(i))
	}

	// 2. Fetch the Invoker's Voice State
	voiceState, err := s.State.VoiceState(i.GuildID, actor)
	if err != nil || voiceState == nil || voiceState.ChannelID == "" {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.QueueCallNotInVoiceEmbed(i))
	}
	voiceChannelID := voiceState.ChannelID

	// 3. Validate the Game Behind the Voice Channel
	game, ok := helpers.Games.ByChannel(voiceChannelID)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.QueueCallNotInGameEmbed(i))
	}
	if _, isParticipant := game.Player(actor); !isParticipant {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.QueueCallNotInMatchEmbed(i))
	}
	if _, targetIsParticipant := game.Player(target.ID); targetIsParticipant {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.QueueCallAlreadyInMatchEmbed(i, target))
	}
	if !isTeamVoiceChannel(game, voiceChannelID) {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.QueueCallWrongChannelEmbed(i))
	}

	// 4. Verify Bot Permissions
	perms, err := s.State.UserChannelPermissions(s.State.User.ID, voiceChannelID)
	if err != nil || perms&discordgo.PermissionManageRoles == 0 {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.QueueCallMissingPermissionsEmbed(i))
	}

	// 5. Grant the Target Access
	err = s.ChannelPermissionSet(voiceChannelID, target.ID, discordgo.PermissionOverwriteTypeMember,
		discordgo.PermissionVoiceConnect|discordgo.PermissionVoiceSpeak, 0)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorQueueCallEmbed(i))
	}

	return helpers.EditInteractionEmbed(s, i, cmdcfg.QueueCallSuccessEmbed(i, target, game.Number))
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
