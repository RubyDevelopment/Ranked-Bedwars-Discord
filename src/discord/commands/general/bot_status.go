package general

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

type botDisplayConfig struct {
	status string
	label  string
	emoji  string
}

var (
	botDisplayConfigs = []botDisplayConfig{
		{status: "Available", label: "Available", emoji: helpers.DotGreen},
		{status: "Starting", label: "Starting", emoji: helpers.DotYellow},
		{status: "Busy", label: "Busy", emoji: helpers.DotRed},
		{status: "Cooldown", label: "Cooldown", emoji: helpers.DotOrange},
		{status: "Offline", label: "Offline", emoji: helpers.DotGray},
	}
)

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
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorBotsEmbed())
	}

	// If no bots are registered, return an error embed
	if len(bots) == 0 {
		return helpers.EditInteractionEmbed(s, i, emptyBotsEmbed())
	}

	return helpers.EditInteractionEmbed(s, i, botsOverviewEmbed(bots))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorBotsEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the registered game bots for this guild. Please contact an administrator for assistance or try again later.",
	})
}

func emptyBotsEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Registered Bots",
		Description: "This guild has no registered game bots. If you think this is an error, please contact an administrator for assistance or try again later.",
	})
}

func botsOverviewEmbed(bots []types.Bot) *discordgo.MessageEmbed {
	statusGroups := make(map[string][]string, len(botDisplayConfigs))
	for _, cfg := range botDisplayConfigs {
		statusGroups[cfg.status] = []string{}
	}

	for _, bot := range bots {
		if strings.TrimSpace(bot.Username) == "" {
			continue
		}

		name := bot.Username
		if strings.TrimSpace(bot.Tier) != "" {
			name = fmt.Sprintf("%s", name)
		}

		status := bot.Status
		if !bot.IsEnabled {
			status = "Offline"
			name += " · disabled"
		}

		if _, ok := statusGroups[status]; ok {
			statusGroups[status] = append(statusGroups[status], name)
			continue
		}

		statusGroups["Offline"] = append(statusGroups["Offline"], name)
	}

	fields := make([]*discordgo.MessageEmbedField, 0, len(botDisplayConfigs))
	for _, cfg := range botDisplayConfigs {
		group := statusGroups[cfg.status]
		if len(group) == 0 {
			continue
		}

		value := strings.Join(group, ", ")
		if len(value) > 1024 {
			value = value[:1021] + "..."
		}

		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%s %s (%d)", cfg.emoji, cfg.label, len(group)),
			Value:  value,
			Inline: false,
		})
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Registered Bots Status",
		Description: "Displays the status and uptime metrics of all registered game bots for this guild.",
		Fields:      fields,
	})
}
