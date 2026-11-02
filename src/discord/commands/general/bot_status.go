package general

import (
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(BotStatusCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// BotStatusCommand Displays the bot's current operational status, latency, uptime, and system info.
var BotStatusCommand = &types.Command{
	Name:        "bots",
	Description: "Displays the bot's current operational status, latency, uptime, and system info.",
	Cooldown:    3 * time.Second,
	Data:        &discordgo.ApplicationCommand{},
	Execute:     executeBotStatusCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeBotStatusCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// Fetch the registered bots for this guild
	bots, err := helpers.GuildMetaBots(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorBotsEmbed(i))
	}

	// If no bots are registered, return an error embed
	if len(bots) == 0 {
		return helpers.EditInteractionEmbed(s, i, EmptyBotsEmbed(i))
	}

	return helpers.EditInteractionEmbed(s, i, helpers.BotsOverviewEmbed(i.GuildID, bots))
}

// ============================================================================
// Embed Builders
// ============================================================================

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorBotsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the registered game bots for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func EmptyBotsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Registered Bots",
		Description: "This guild has no registered game bots. If you think this is an error, please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}
