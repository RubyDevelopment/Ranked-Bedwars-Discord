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
	helpers.Register(PlayerScreenshareCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerScreenshareCommand requests a screenshare for a player.
var PlayerScreenshareCommand = &types.Command{
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
	Execute: executePlayerScreenshareCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

// executePlayerScreenshareCommand files a screenshare review request for a player.
func executePlayerScreenshareCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	svc := database.Get()
	if svc == nil {
		return helpers.EditInteractionEmbed(s, i, playerScreenshareDatabaseUnavailableEmbed())
	}

	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, playerScreenshareInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, playerScreenshareBotTargetEmbed())
	}

	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	username := target.Username

	if view, err := svc.Player.Get(i.GuildID, target.ID); err == nil && view.Config.Username != "" {
		username = view.Config.Username
	}

	return helpers.EditInteractionEmbed(s, i, playerScreenshareSubmittedEmbed(target, username, reason))
}

// ============================================================================
// Embed Builders
// ============================================================================

func playerScreenshareSubmittedEmbed(target *discordgo.User, username, reason string) *discordgo.MessageEmbed {
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

func playerScreenshareDatabaseUnavailableEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to submit the screenshare request because the database service is unavailable. Please contact an administrator for assistance or try again later.",
	})
}

func playerScreenshareInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to request a screenshare for. Select a member from the command options.",
	})
}

func playerScreenshareBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated application bot profiles cannot be screenshared. Target a real player instead.",
	})
}
