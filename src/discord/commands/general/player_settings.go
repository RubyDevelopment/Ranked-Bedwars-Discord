package general

import (
	"fmt"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

// settingsToggle is one on/off preference a player can change for themselves.
type settingsToggle struct {
	key   string
	label string
}

var settingsToggles = []settingsToggle{
	{key: "is_elo_visible", label: "ELO Visibility"},
	{key: "is_stats_visible", label: "Stats Visibility"},
	{key: "is_party_invites_enabled", label: "Party Invites"},
	{key: "is_party_autowarp_enabled", label: "Party Autowarp"},
	{key: "is_ping_on_results_enabled", label: "Ping On Results"},
	{key: "is_ping_on_alerts_enabled", label: "Ping On Alerts"},
}

func init() {
	helpers.Register(PlayerSettingsCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerSettingsCommand shows or updates the caller's ranked profile settings.
var PlayerSettingsCommand = &types.Command{
	Name:        "settings",
	Description: "Views or updates your ranked profile settings.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "view",
				Description: "View your current settings",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "toggle",
				Description: "Toggle one of your ranked profile settings",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "option",
						Description: "Setting to change",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    true,
						Choices:     settingsToggleChoices(),
					},
					{
						Name:        "enabled",
						Description: "Whether the setting should be enabled",
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Required:    true,
					},
				},
			},
		},
	},
	Execute: executePlayerSettingsCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

// executePlayerSettingsCommand routes the selected subcommand to its handler.
func executePlayerSettingsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	sub, _ := types.SelectedSubcommand(i)
	switch sub {
	case "view":
		return executePlayerSettingsView(s, i)
	case "toggle":
		return executePlayerSettingsToggle(s, i)
	}
	return helpers.EditInteractionEmbed(s, i, playerSettingsInvalidSubcommandEmbed())
}

func executePlayerSettingsView(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, helpers.InteractionUserID(i))
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerSettingsRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerSettingsEmbed())
	}

	return helpers.EditInteractionEmbed(s, i, playerSettingsOverviewEmbed(view.Config))
}

func executePlayerSettingsToggle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := helpers.InteractionUserID(i)
	_, options := types.SelectedSubcommand(i)

	// 1. Validate Setting
	toggle := findSettingsToggle(helpers.SubOptionString(options, "option"))
	if toggle == nil {
		return helpers.EditInteractionEmbed(s, i, playerSettingsUnknownOptionEmbed())
	}
	enabled := helpers.SubOptionBool(options, "enabled")

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerSettingsRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerSettingsEmbed())
	}

	// 3. Update Setting in Database
	if _, err := database.Must().Player.UpdateConfig(userID, i.GuildID, userID, map[string]any{toggle.key: enabled}); err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerSettingsEmbed())
	}

	return helpers.EditInteractionEmbed(s, i, playerSettingsToggledEmbed(view.Config.Username, toggle.label, enabled))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

func settingsToggleChoices() []*discordgo.ApplicationCommandOptionChoice {
	choices := make([]*discordgo.ApplicationCommandOptionChoice, len(settingsToggles))
	for idx, toggle := range settingsToggles {
		choices[idx] = &discordgo.ApplicationCommandOptionChoice{Name: toggle.label, Value: toggle.key}
	}
	return choices
}

func findSettingsToggle(key string) *settingsToggle {
	for idx := range settingsToggles {
		if settingsToggles[idx].key == key {
			return &settingsToggles[idx]
		}
	}
	return nil
}

func settingsStateLabel(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerSettingsEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while updating your settings. Please contact an administrator for assistance or try again later.",
	})
}

func playerSettingsInvalidSubcommandEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "Use `/player_settings view` or `/player_settings toggle`.",
	})
}

func playerSettingsRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before you can manage your settings. Run `/player_register` to link your Minecraft account first.",
	})
}

func playerSettingsUnknownOptionEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unknown Setting",
		Description: "That is not a setting you can change. Pick one of the options listed in the command.",
	})
}

func playerSettingsOverviewEmbed(config *models.PlayerConfigsModel) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Player Settings — %s", config.Username),
		Description: "Your current ranked profile preferences. Use `/player_settings toggle` to change one.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(config.Username),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "ELO Visibility", Value: fmt.Sprintf("**%s**", settingsStateLabel(config.IsEloVisible)), Inline: true},
			{Name: "Stats Visibility", Value: fmt.Sprintf("**%s**", settingsStateLabel(config.IsStatsVisible)), Inline: true},
			{Name: "Party Invites", Value: fmt.Sprintf("**%s**", settingsStateLabel(config.IsPartyInvitesEnabled)), Inline: true},
			{Name: "Party Autowarp", Value: fmt.Sprintf("**%s**", settingsStateLabel(config.IsPartyAutowarpEnabled)), Inline: true},
			{Name: "Ping On Results", Value: fmt.Sprintf("**%s**", settingsStateLabel(config.IsPingOnResultsEnabled)), Inline: true},
			{Name: "Ping On Alerts", Value: fmt.Sprintf("**%s**", settingsStateLabel(config.IsPingOnAlertsEnabled)), Inline: true},
		},
	})
}

func playerSettingsToggledEmbed(username, label string, enabled bool) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Setting Updated",
		Description: fmt.Sprintf("Successfully updated your settings.\n\n> **Setting:** `%s`\n> **State:** `%s`", label, settingsStateLabel(enabled)),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
