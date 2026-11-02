package general

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ModerationScreenshareCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationScreenshareCommand requests a screenshare for a player.
var ModerationScreenshareCommand = &types.Command{
	Name:          "screenshare",
	Description:   "Requests a screenshare for a player to keep competitive play fair.",
	PrefixAliases: []string{"ss"},
	Ephemeral:     true,
	Cooldown:      5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "user", Description: "Player to request a screenshare for", Type: discordgo.ApplicationCommandOptionUser, Required: true},
			{Name: "reason", Description: "Reason for the screenshare request", Type: discordgo.ApplicationCommandOptionString, Required: false},
		},
	},
	Execute: executeModerationScreenshareCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

// executeModerationScreenshareCommand files a screenshare review request for a player.
func executeModerationScreenshareCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	svc := database.Get()
	if svc == nil {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareDatabaseUnavailableEmbed(i))
	}

	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, ModerationScreenshareBotTargetEmbed(i))
	}

	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	username := target.Username

	if view, err := svc.Player.Get(i.GuildID, target.ID); err == nil && view.Config.Username != "" {
		username = view.Config.Username
	}

	return helpers.EditInteractionEmbed(s, i, ModerationScreenshareSubmittedEmbed(i, target, username, reason))
}

// ============================================================================
// Helper functions
// ============================================================================

func ModerationScreenshareSubmittedEmbed(i *discordgo.InteractionCreate, target *discordgo.User, username, reason string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Your screenshare request has been submitted. Staff will review the request and contact you shortly.\n\n> **Player:** <@%s> (**%s**)",
		target.ID, username,
	)
	if reason != "" {
		description += fmt.Sprintf("\n> **Reason:** %s", reason)
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Screenshare Request Submitted",
		Description: description,
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func ModerationScreenshareDatabaseUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to submit the screenshare request because the database service is unavailable. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationScreenshareInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to request a screenshare for. Select a member from the command options.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationScreenshareBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated application bot profiles cannot be screenshared. Target a real player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}
