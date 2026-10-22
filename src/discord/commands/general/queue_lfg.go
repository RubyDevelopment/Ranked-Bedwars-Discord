package general

import (
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
		return helpers.EditInteractionEmbed(s, i, helpers.QueueLfgInvalidUserEmbed(i))
	}

	// Load the LFG configuration for this guild
	lfgConfig, err := helpers.LoadLFGConfig(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorQueueLfgEmbed(i))
	}

	// Validate LFG configuration
	if lfgConfig.LFGChannelID == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.QueueLfgNotConfiguredEmbed(i))
	}
	if lfgConfig.QueuesPaused {
		return helpers.EditInteractionEmbed(s, i, helpers.QueueLfgQueuesPausedEmbed(i))
	}

	// Verify the user is registered in the ranked system
	view, err := database.Must().Player.Get(i.GuildID, target)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.QueueLfgNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorQueueLfgEmbed(i))
	}

	// Use the configured username if available, otherwise use the user ID
	username := target
	if view.Config.Username != "" {
		username = view.Config.Username
	}

	// Send the LFG post to the configured channel
	if _, err := s.ChannelMessageSendComplex(lfgConfig.LFGChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{helpers.QueueLfgPostEmbed(i, username, target)},
	}); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorQueueLfgEmbed(i))
	}

	// Assign the LFG role if configured
	if lfgConfig.LFGRoleID != "" {
		_ = s.GuildMemberRoleAdd(i.GuildID, target, lfgConfig.LFGRoleID)
	}

	return helpers.EditInteractionEmbed(s, i, helpers.QueueLfgSuccessEmbed(i, username, lfgConfig.LFGChannelID))
}
