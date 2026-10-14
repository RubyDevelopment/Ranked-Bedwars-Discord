package admin

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

var discordMessageLinkPattern = regexp.MustCompile(`^https?://(?:www\.)?(?:discord(?:app)?\.com)/channels/(.+?)/(\d+)/(\d+)/?$`)

func init() {
	helpers.Register(ServerEchoCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerEchoCommand Broadcasts an official announcement or echo message into a target channel.
var ServerEchoCommand = &types.Command{
	Name:        "server_echo",
	Description: "Broadcasts an official announcement or echo message into a target channel.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.ManageMessagesPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "message",
				Description: "Echoes a plain text message",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "content",
						Description: "The text to echo",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    true,
					},
					{
						Name:        "target",
						Description: "Optional target channel",
						Type:        discordgo.ApplicationCommandOptionChannel,
						Required:    false,
					},
				},
			},
			{
				Name:        "link",
				Description: "Echoes a Discord-related link",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "url",
						Description: "The Discord link to echo",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    true,
					},
					{
						Name:        "target",
						Description: "Optional target channel",
						Type:        discordgo.ApplicationCommandOptionChannel,
						Required:    false,
					},
				},
			},
		},
	},
	Execute: executeServerEchoCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeServerEchoCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	subcommand, options := types.SelectedSubcommand(i)

	switch subcommand {
	case "message":
		content := helpers.SubOptionString(options, "content")
		targetChannelID := resolveTargetChannel(i, options)

		if embedErr := ensureEchoChannelAccess(s, i, targetChannelID); embedErr != nil {
			return helpers.EditInteractionEmbed(s, i, embedErr)
		}

		if _, err := s.ChannelMessageSend(targetChannelID, content); err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorEchoEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, echoSuccessEmbed(targetChannelID))

	case "link":
		link := helpers.SubOptionString(options, "url")
		targetChannelID := resolveTargetChannel(i, options)

		parsed := parseDiscordMessageLink(link)
		if parsed == nil {
			return helpers.EditInteractionEmbed(s, i, echoInvalidLinkEmbed())
		}
		if i.GuildID != "" && parsed.guildID != i.GuildID {
			return helpers.EditInteractionEmbed(s, i, echoRestrictedDestinationEmbed())
		}

		if embedErr := ensureEchoChannelAccess(s, i, targetChannelID); embedErr != nil {
			return helpers.EditInteractionEmbed(s, i, embedErr)
		}

		sourceMessage, err := s.ChannelMessage(parsed.channelID, parsed.messageID)
		if err != nil || sourceMessage == nil {
			return helpers.EditInteractionEmbed(s, i, echoInvalidLinkEmbed())
		}

		send := &discordgo.MessageSend{
			Content: sourceMessage.Content,
			Embeds:  sourceMessage.Embeds,
		}
		if _, err := s.ChannelMessageSendComplex(targetChannelID, send); err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorEchoEmbed())
		}

		return helpers.EditInteractionEmbed(s, i, echoSuccessEmbed(targetChannelID))
	}

	return nil
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

func resolveTargetChannel(i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) string {
	if target := helpers.SubOptionChannelID(options, "target"); target != "" {
		return target
	}
	return i.ChannelID
}

func ensureEchoChannelAccess(s *discordgo.Session, i *discordgo.InteractionCreate, channelID string) *discordgo.MessageEmbed {
	if i.GuildID != "" {
		channel, err := s.Channel(channelID)
		if err != nil {
			return echoInvalidChannelEmbed()
		}
		if channel.GuildID != "" && channel.GuildID != i.GuildID {
			return echoRestrictedDestinationEmbed()
		}
	}

	perms, err := s.UserChannelPermissions(s.State.User.ID, channelID)
	if err != nil || perms&(discordgo.PermissionViewChannel|discordgo.PermissionSendMessages) != (discordgo.PermissionViewChannel|discordgo.PermissionSendMessages) {
		return echoInsufficientPermissionsEmbed()
	}
	return nil
}

type discordMessageLink struct {
	guildID   string
	channelID string
	messageID string
}

func parseDiscordMessageLink(value string) *discordMessageLink {
	matches := discordMessageLinkPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 4 || matches[1] == "@me" {
		return nil
	}
	return &discordMessageLink{
		guildID:   matches[1],
		channelID: matches[2],
		messageID: matches[3],
	}
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorEchoEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing the echo command. Please try again later or contact support.",
	})
}

func echoSuccessEmbed(channelID string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Message Dispatched",
		Description: fmt.Sprintf(
			"Your message was sent successfully. The message is now visible in the target channel <#%s>.",
			channelID,
		),
	})
}

func echoInvalidLinkEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Link",
		Description: "That does not look like a valid Discord message link. Right-click a message and choose **Copy Message Link**.",
	})
}

func echoRestrictedDestinationEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Restricted Destination",
		Description: "That message link points outside this server. Only links from this guild can be used.",
	})
}

func echoInvalidChannelEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Channel",
		Description: "The selected channel could not be used. Choose a text channel the bot can access.",
	})
}

func echoInsufficientPermissionsEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Insufficient Permissions",
		Description: "The bot does not have permission to send messages in that channel. Grant **Send Messages** to the bot role and try again.",
	})
}
