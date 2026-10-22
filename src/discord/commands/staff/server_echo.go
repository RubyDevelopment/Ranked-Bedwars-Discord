package staff

import (
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
	Name:         "echo",
	Requirements: types.StaffRequirements,
	Description:  "Broadcasts an official announcement or echo message into a target channel.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
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
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorEchoEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.EchoSuccessEmbed(i, targetChannelID))

	case "link":
		link := helpers.SubOptionString(options, "url")
		targetChannelID := resolveTargetChannel(i, options)

		parsed := parseDiscordMessageLink(link)
		if parsed == nil {
			return helpers.EditInteractionEmbed(s, i, helpers.EchoInvalidLinkEmbed(i))
		}
		if i.GuildID != "" && parsed.guildID != i.GuildID {
			return helpers.EditInteractionEmbed(s, i, helpers.EchoRestrictedDestinationEmbed(i))
		}

		if embedErr := ensureEchoChannelAccess(s, i, targetChannelID); embedErr != nil {
			return helpers.EditInteractionEmbed(s, i, embedErr)
		}

		sourceMessage, err := s.ChannelMessage(parsed.channelID, parsed.messageID)
		if err != nil || sourceMessage == nil {
			return helpers.EditInteractionEmbed(s, i, helpers.EchoInvalidLinkEmbed(i))
		}

		send := &discordgo.MessageSend{
			Content: sourceMessage.Content,
			Embeds:  sourceMessage.Embeds,
		}
		if _, err := s.ChannelMessageSendComplex(targetChannelID, send); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorEchoEmbed(i))
		}

		return helpers.EditInteractionEmbed(s, i, helpers.EchoSuccessEmbed(i, targetChannelID))
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
			return helpers.EchoInvalidChannelEmbed(i)
		}
		if channel.GuildID != "" && channel.GuildID != i.GuildID {
			return helpers.EchoRestrictedDestinationEmbed(i)
		}
	}

	perms, err := s.UserChannelPermissions(s.State.User.ID, channelID)
	if err != nil || perms&(discordgo.PermissionViewChannel|discordgo.PermissionSendMessages) != (discordgo.PermissionViewChannel|discordgo.PermissionSendMessages) {
		return helpers.EchoInsufficientPermissionsEmbed(i)
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
