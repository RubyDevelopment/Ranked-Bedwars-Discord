package general

import (
	"strconv"
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
	status      string
	labelLayout string // layout keys: a guild renames a status or swaps its dot
	emojiLayout string
}

var (
	botDisplayConfigs = []botDisplayConfig{
		botStatus("Available", helpers.DotGreen),
		botStatus("Starting", helpers.DotYellow),
		botStatus("Busy", helpers.DotRed),
		botStatus("Cooldown", helpers.DotOrange),
		botStatus("Offline", helpers.DotGray),
	}
)

// botStatus defines the layouts that name a status and its dot.
func botStatus(status, emoji string) botDisplayConfig {
	key := "bots.status_" + strings.ToLower(status)
	return botDisplayConfig{
		status:      status,
		labelLayout: helpers.DefineLayout(key+"_label", status+" label", status),
		emojiLayout: helpers.DefineLayout(key+"_emoji", status+" dot", emoji),
	}
}

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
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorBotsEmbed(i))
	}

	// If no bots are registered, return an error embed
	if len(bots) == 0 {
		return helpers.EditInteractionEmbed(s, i, helpers.EmptyBotsEmbed(i))
	}

	return helpers.EditInteractionEmbed(s, i, botsOverviewEmbed(i.GuildID, bots))
}

// ============================================================================
// Embed Builders
// ============================================================================

func botsOverviewEmbed(guildID string, bots []types.Bot) *discordgo.MessageEmbed {
	statusGroups := make(map[string][]string, len(botDisplayConfigs))
	for _, cfg := range botDisplayConfigs {
		statusGroups[cfg.status] = []string{}
	}

	for _, bot := range bots {
		if strings.TrimSpace(bot.Username) == "" {
			continue
		}

		status, note := bot.Status, ""
		if !bot.IsEnabled {
			status = "Offline"
			note = helpers.FillLayout(guildID, helpers.LayoutBotsDisabled, nil)
		}

		// If bot is "Available", treat it as "Offline" for display
		// Only show active hosting states (Starting, Busy, Cooldown)
		if status == "Available" {
			status = "Offline"
		}
		if _, ok := statusGroups[status]; !ok {
			status = "Offline"
		}
		statusGroups[status] = append(statusGroups[status], helpers.FillLayout(guildID, helpers.LayoutBotsBot, map[string]string{
			"name": bot.Username, "tier": bot.Tier, "status": status, "note": note,
		}))
	}

	separator := helpers.LayoutSeparator(guildID, helpers.LayoutBotsSeparator)
	fields := make([]*discordgo.MessageEmbedField, 0, len(botDisplayConfigs))
	for _, cfg := range botDisplayConfigs {
		group := statusGroups[cfg.status]
		if len(group) == 0 {
			continue
		}

		value := strings.Join(group, separator)
		if len(value) > 1024 {
			value = value[:1021] + "..."
		}

		fields = append(fields, &discordgo.MessageEmbedField{
			Name: helpers.FillLayout(guildID, helpers.LayoutBotsGroupTitle, map[string]string{
				"emoji": helpers.FillLayout(guildID, cfg.emojiLayout, nil), "label": helpers.FillLayout(guildID, cfg.labelLayout, nil),
				"count": strconv.Itoa(len(group)), "status": cfg.status,
			}),
			Value:  value,
			Inline: false,
		})
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Registered Bots Status",
		Description: "Displays the status and uptime metrics of all registered game bots for this guild.",
		Fields:      fields,
		Thumbnail:   helpers.ThumbBot,
	})
}
