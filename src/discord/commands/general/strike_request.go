package general

import (
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(StrikeRequestCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// StrikeRequestCommand requests a strike review for a player.
var StrikeRequestCommand = &types.Command{
	Name:        "strike_request",
	Description: "Requests a strike review for a player who broke the rules.",
	Ephemeral:   true,
	Cooldown:    5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to request a strike review for",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Why the strike should be reviewed",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
		},
	},
	Execute: executeStrikeRequestCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeStrikeRequestCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, strikeRequestInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, strikeRequestBotTargetEmbed())
	}

	// 2. Validate Reason
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	if reason == "" {
		return helpers.EditInteractionEmbed(s, i, strikeRequestReasonRequiredEmbed())
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, strikeRequestNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorStrikeRequestEmbed())
	}

	username := target.Username
	if view.Config != nil && view.Config.Username != "" {
		username = view.Config.Username
	}
	return helpers.EditInteractionEmbed(s, i, strikeRequestSubmittedEmbed(target, username, reason))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorStrikeRequestEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while submitting the strike review request. Please try again later or contact support.",
	})
}

func strikeRequestInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to request a strike review for. Select a registered player from the command options.",
	})
}

func strikeRequestBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have strikes to review. Target a registered player instead.",
	})
}

func strikeRequestReasonRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Reason Required",
		Description: "You must explain why the strike should be reviewed. Include a clear explanation in the reason field.",
	})
}

func strikeRequestNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can have a strike review requested.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func strikeRequestSubmittedEmbed(target *discordgo.User, username, reason string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Strike Review Requested",
		Description: fmt.Sprintf(
			"Your strike review request has been submitted. Staff will review the strike and respond on this ticket.\n\n> **Player:** <@%s> (**%s**)\n> **Reason:** %s",
			target.ID, username, reason,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}
