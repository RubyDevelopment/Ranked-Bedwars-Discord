package general

import (
	"fmt"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(QueueLfgCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// QueueLfgCommand posts a looking-for-group ping to the guild's configured LFG
// channel (guild_configs.lfg — see LoadLFGConfig/SaveLFGConfig).
var QueueLfgCommand = &types.Command{
	Name:        "lfg",
	Description: "Posts that you are looking for a ranked game.",
	Ephemeral:   true,
	Cooldown:    60 * time.Second,
	Data:        &discordgo.ApplicationCommand{},
	Execute:     executeQueueLfgCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeQueueLfgCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// Identify the user invoking the command
	target := helpers.InteractionUserID(i)
	if target == "" {
		return helpers.EditInteractionEmbed(s, i, queueLfgInvalidUserEmbed())
	}

	// Load the LFG configuration for this guild
	lfgConfig, err := helpers.LoadLFGConfig(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorQueueLfgEmbed())
	}

	// Validate LFG configuration
	if lfgConfig.LFGChannelID == "" {
		return helpers.EditInteractionEmbed(s, i, queueLfgNotConfiguredEmbed())
	}
	if lfgConfig.QueuesPaused {
		return helpers.EditInteractionEmbed(s, i, queueLfgQueuesPausedEmbed())
	}

	// Verify the user is registered in the ranked system
	view, err := database.Must().Player.Get(i.GuildID, target)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, queueLfgNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorQueueLfgEmbed())
	}

	// Use the configured username if available, otherwise use the user ID
	username := target
	if view.Config.Username != "" {
		username = view.Config.Username
	}

	// Send the LFG post to the configured channel
	if _, err := s.ChannelMessageSendComplex(lfgConfig.LFGChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{queueLfgPostEmbed(username, target)},
	}); err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorQueueLfgEmbed())
	}

	// Assign the LFG role if configured
	if lfgConfig.LFGRoleID != "" {
		_ = s.GuildMemberRoleAdd(i.GuildID, target, lfgConfig.LFGRoleID)
	}

	return helpers.EditInteractionEmbed(s, i, queueLfgSuccessEmbed(username, lfgConfig.LFGChannelID))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorQueueLfgEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to post your looking-for-group request. Please contact an administrator for assistance or try again later.",
	})
}

func queueLfgInvalidUserEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid User",
		Description: "Your user account could not be identified. Please try again later or contact an administrator for assistance.",
	})
}

func queueLfgNotConfiguredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "LFG Not Configured",
		Description: "The looking-for-group system is not configured in this guild. Please contact an administrator for assistance to configure the LFG channel and role settings.",
	})
}

func queueLfgQueuesPausedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Queues Paused",
		Description: "Looking-for-group posting is currently paused by an administrator. Please try again later when queues are reopened.",
	})
}

func queueLfgNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register with the ranked system to use the looking-for-group feature. Use `/player_register` to link your account and track your seasonal progress.",
	})
}

func queueLfgSuccessEmbed(username, channelID string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "LFG Posted",
		Description: fmt.Sprintf("Your looking-for-group post has been sent. Good luck finding a game!\n\n> **Player:** `%s`\n> **Channel:** <#%s>", username, channelID),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func queueLfgPostEmbed(username, userID string) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Looking for Group",
		Description: fmt.Sprintf("<@%s> (**%s**) is looking for a ranked game! React or message them to join their party.", userID, username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
