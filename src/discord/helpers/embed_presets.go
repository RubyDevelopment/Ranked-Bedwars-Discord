package helpers

import (
	"fmt"
	"net/url"
	"ranked-bedwars/src/discord/helpers/banner"
	"ranked-bedwars/src/types"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

const (
	ColorDefault     = 0x55FFFF
	ColorSuccess     = 0x69f569
	ColorCaution     = 0xe78432
	ColorInfo        = 0x393a41
	ColorError       = 0xdc4d4d
	ColorLoading     = 0xe7e74c
	ColorNeutral     = 0x393a41
	ColorTransparent = 0x393a41

	// EmbedBlendColor is the background of an embed in Discord's dark theme: a bar of this colour is not seen, so the embed
	// looks like one plain block. (A Components V2 container needs no such colour: without an accent it has no bar.)
	EmbedBlendColor = 0x2b2d31
)

var presetColors = map[string]int{
	"success":     ColorSuccess,
	"caution":     ColorCaution,
	"info":        ColorInfo,
	"error":       ColorError,
	"loading":     ColorLoading,
	"neutral":     ColorNeutral,
	"transparent": ColorTransparent,
}

// ============================================================================
// General Embed Preset Functions
// ============================================================================

// Preset generates a discordgo embed from a named preset style.
func Preset(presetType string, options types.PresetOptions) *discordgo.MessageEmbed {
	resolvedColor, exists := presetColors[presetType]
	if !exists {
		resolvedColor = ColorDefault
	}

	if options.Color != 0 {
		resolvedColor = options.Color
	}

	embed := &discordgo.MessageEmbed{Color: resolvedColor}

	// Apply Title & Description
	if title, ok := parseOptionalString(options.Title); ok {
		embed.Title = title
	}
	if desc, ok := parseOptionalString(options.Description); ok {
		embed.Description = desc
	}

	// Author configuration
	if options.Author != nil {
		name, showAuthor := parseOptionalString(options.Author.Name)
		if showAuthor && name != "" {
			embed.Author = &discordgo.MessageEmbedAuthor{Name: name}
			if icon, ok := parseOptionalString(options.Author.IconURL); ok {
				embed.Author.IconURL = icon
			}
		}
	}

	// Footer configuration
	footerText, showFooter := parseOptionalString(options.FooterText)
	if showFooter {
		if footerText == "" {
			footerText = "Ranked Bedwars"
		}
		embed.Footer = &discordgo.MessageEmbedFooter{Text: footerText}
		if icon, ok := parseOptionalString(options.FooterIcon); ok {
			embed.Footer.IconURL = icon
		}
	}

	// Timestamp handling
	if presetType != "transparent" {
		switch ts := options.Timestamp.(type) {
		case bool:
			if !ts {
				embed.Timestamp = ""
			}
		case time.Time:
			embed.Timestamp = ts.Format(time.RFC3339)
		case int64:
			embed.Timestamp = time.Unix(ts, 0).Format(time.RFC3339)
		default:
			embed.Timestamp = time.Now().Format(time.RFC3339)
		}
	}

	// Fields, Image, and Thumbnail
	if len(options.Fields) > 0 {
		embed.Fields = options.Fields
	} else {
		withGuidance(embed, presetType, embed.Title)
	}

	if img, ok := parseOptionalString(options.Image); ok && img != "" {
		embed.Image = &discordgo.MessageEmbedImage{URL: img}
	}

	if thumb, ok := parseOptionalString(options.Thumbnail); ok && thumb != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: thumb}
	}

	return embed
}

// Utility to cleanly parse string OR bool false options
func parseOptionalString(val any) (string, bool) {
	switch v := val.(type) {
	case string:
		return v, true
	case bool:
		return "", v
	default:
		return "", true
	}
}

// ============================================================================
// Middleware-Specific Embed Functions
// ============================================================================

// GuildNotRegisteredEmbed creates an embed for guild not registered errors.
func GuildNotRegisteredEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Guild Not Registered",
		Description: "This guild is not registered with Ranked Bedwars yet. An administrator must run `/setup init` to initialize it.",
		Thumbnail:   ThumbFlag,
	})
}

// SimpleErrorEmbed creates a simple error embed.
func SimpleErrorEmbed(message string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{Title: "Error", Description: message, Thumbnail: ThumbFlag})
}

// CommandErrorEmbed creates an embed for command execution errors.
func CommandErrorEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Command Error",
		Description: "An unexpected error occurred while executing this command. Please try again later or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

// CommandErrorSlashEmbed creates an embed for slash command execution errors.
func CommandErrorSlashEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Command Error",
		Description: "An unexpected error occurred while executing this command. Please try again later or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

// ConfigurationErrorEmbed creates an embed for configuration errors.
func ConfigurationErrorEmbed(description string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{Title: "Configuration Error", Description: description, Thumbnail: ThumbFlag})
}

// AccessDeniedEmbed creates an embed for access denied errors.
func AccessDeniedEmbed(title, message string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{Title: title, Description: message, Thumbnail: ThumbFlag})
}

// ============================================================================
// Error Formatting Functions
// ============================================================================

// FormatConfigurationErrorAdmin formats configuration errors for
// administrators, listing both what's blocking the command (missing) and
// what's merely available (optional) — either may be empty.
func FormatConfigurationErrorAdmin(cmdName string, missing, optional []types.Requirement) string {
	var description string
	if len(missing) > 0 {
		description += fmt.Sprintf("The command `%s` requires the following resources to be configured:\n", cmdName)
		for _, req := range missing {
			description += fmt.Sprintf("- %s: %s\n", req.Type, req.Key)
		}
	}
	if len(optional) > 0 {
		if description != "" {
			description += "\n"
		}
		description += fmt.Sprintf("`%s` also has these optional resources available:\n", cmdName)
		for _, req := range optional {
			description += fmt.Sprintf("- %s: %s\n", req.Type, req.Key)
		}
	}
	description += fmt.Sprintf("\nYou're able to configure these at any time using `/config role|channel|category set command:%s`.", cmdName)
	return description
}

// FormatConfigurationErrorUser formats configuration errors for regular users.
func FormatConfigurationErrorUser(cmdName string, missing []types.Requirement) string {
	description := fmt.Sprintf("The command `%s` cannot be used because required resources are not configured.\n", cmdName)
	description += "Please contact a server administrator to resolve this issue."
	return description
}

// ============================================================================
// Component Building Functions
// ============================================================================

// MinecraftBustAvatarURL returns the bust render URL used for player thumbnails.
func MinecraftBustAvatarURL(username string) string {
	return "https://nmsr.nickac.dev/bust/" + url.PathEscape(banner.SkinName(username))
}

// The old bot's thumbnail icons that earn their place (white glyphs, made for Discord's dark theme). Its candle icons,
// the clock/timer duplicate, the yellow/green/red logo tiles and the empty `bot` fallback were left out: they say
// nothing a command could use. The doubleSwords, timer, tada, sprout, flag and trashCan constants are in game_helper.go.
const (
	ThumbHammer  = "https://i.imgur.com/2dLIqF6.png"
	ThumbClock   = "https://i.imgur.com/i2JnqZm.png"
	ThumbLoad    = "https://i.imgur.com/KowXZbQ.png"
	ThumbPlayer  = "https://i.imgur.com/E0l2g2j.png"
	ThumbPlayers = "https://i.imgur.com/5yrykSY.png"
	ThumbBed     = "https://i.imgur.com/BBlmpYu.png"
	ThumbGuild   = "https://i.imgur.com/LvOizEZ.png"
	ThumbBot     = "https://i.imgur.com/U0dRZnV.png"
)

// DiscordRelativeTimestamp formats a Discord relative timestamp (`<t:unix:R>`).
func DiscordRelativeTimestamp(t time.Time) string {
	return fmt.Sprintf("<t:%d:R>", t.Unix())
}

// ============================================================================
// The catalogue of the built-in responses
