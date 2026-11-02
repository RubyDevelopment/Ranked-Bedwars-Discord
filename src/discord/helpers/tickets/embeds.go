package tickets

import (
	"errors"
	"fmt"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// TicketWelcomeMessage renders the configurable welcome and ticket details.
func TicketWelcomeMessage(ticket *types.Ticket, panel *types.InteractivePanel, category *types.TicketCategory, guildName string) *discordgo.MessageEmbed {
	welcome := category.Welcome
	color := welcome.Color
	if color == 0 {
		color = defaultPanelColor
	}
	if welcome.NoColor {
		color = helpers.EmbedBlendColor
	}

	title := TicketPlaceholders(welcome.Title, ticket, panel, category, guildName)
	body := TicketPlaceholders(welcome.Body, ticket, panel, category, guildName)

	fields := []*discordgo.MessageEmbedField{}
	switch {
	case welcome.Fields != nil: // the fields the category's welcome message defines
		for _, f := range *welcome.Fields {
			name := truncate(strings.TrimSpace(TicketPlaceholders(f.Name, ticket, panel, category, guildName)), 256)
			value := truncate(strings.TrimSpace(TicketPlaceholders(f.Value, ticket, panel, category, guildName)), 1024)
			if name == "" || value == "" || len(fields) == 25 { // Discord refuses an empty field, and more than 25
				continue
			}
			fields = append(fields, &discordgo.MessageEmbedField{Name: name, Value: value, Inline: f.Inline})
		}
	case !welcome.HideDetails:
		fields = append(fields, ticketDetailFields(ticket)...)
	}

	footer := ""
	if welcome.Footer != "" {
		footer = TicketPlaceholders(welcome.Footer, ticket, panel, category, guildName)
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: body,
		Color:       color,
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: footer},
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: welcome.ThumbnailURL},
	}

	return embed
}

// TicketLogMessage renders one entry of the ticket log channel (and the
// owner's DM). The transcript, when any, is attached to the message itself.
func TicketLogMessage(color int, title, description string, lines []string) *discordgo.MessageEmbed {
	body := description
	if len(lines) > 0 {
		body += "\n\n" + strings.Join(lines, "\n")
	}
	return &discordgo.MessageEmbed{Title: title, Description: body, Color: color}
}

// TicketLogsMessage lists the most recently closed tickets.
func TicketLogsMessage(entries []*types.Ticket, filter string) *discordgo.MessageEmbed {
	if len(entries) == 0 {
		return &discordgo.MessageEmbed{
			Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: helpers.ThumbGuild},
			Title:       "Ticket Logs",
			Description: "No closed tickets matched your search.",
			Color:       helpers.ColorInfo,
		}
	}
	var list strings.Builder
	for _, ticket := range entries {
		claimed := ""
		if ticket.ClaimedBy != "" {
			claimed = fmt.Sprintf(" - claimed by <@%s>", ticket.ClaimedBy)
		}
		closer := "<@" + ticket.ClosedBy + ">"
		if ticket.ClosedBy == inactivityCloser {
			closer = "inactivity policy"
		}
		fmt.Fprintf(&list, "**#%s** %s - <@%s> - closed <t:%d:R> by %s%s\n", ticket.Ref, ticket.CategoryLabel, ticket.OwnerID, ticket.ClosedAt, closer, claimed)
	}
	description := list.String()
	if filter != "" {
		description = filter + "\n\n" + description
	}
	return &discordgo.MessageEmbed{Title: "Ticket Logs", Description: truncate(description, 4000), Color: helpers.ColorDefault}
}

func ticketCreatedEmbed(threadID string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Ticket Created",
		Description: fmt.Sprintf("Your ticket was created: <#%s>. Continue the conversation there.", threadID),
		Thumbnail:   helpers.ThumbGuild,
	})
}

// ticketOpenErrorEmbed explains why a ticket could not be opened.
func ticketOpenErrorEmbed(err error) *discordgo.MessageEmbed {
	var limit *TicketLimitError
	switch {
	case errors.As(err, &limit):
		mentions := make([]string, len(limit.Threads))
		for idx, id := range limit.Threads {
			mentions[idx] = "<#" + id + ">"
		}
		return helpers.Preset("caution", types.PresetOptions{
			Title: "Ticket Limit Reached",
			Description: fmt.Sprintf(
				"You can only have **%d** open ticket(s) for this panel. Continue in %s or close it before opening a new one.",
				limit.Limit, strings.Join(mentions, ", "),
			),
			Thumbnail: helpers.ThumbGuild,
		})
	case errors.Is(err, ErrTicketPanelNotFound), errors.Is(err, ErrTicketCategoryNotFound):
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Panel Outdated",
			Description: "This panel is no longer available. Ask staff to send a fresh one.",
			Thumbnail:   helpers.ThumbFlag,
		})
	}
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Ticket Not Created",
		Description: "Your ticket could not be created. The bot may be missing permission to create private threads in this channel. Please contact staff.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func TicketBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be part of tickets. Select a member instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func TicketPermissionDeniedEmbed(reason string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Permission Denied",
		Description: reason,
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ticketNotInChannelEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not A Ticket",
		Description: "This command must be run inside an open ticket thread.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ticketPanelMissingEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Panel Not Found",
		Description: "The panel for this ticket no longer exists. Ask an administrator to check its configuration in the Ranked Bedwars Dashboard.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

// TicketNoticeEmbed is the staff-only confirmation of a ticket change (rename, lock, unlock).
func TicketNoticeEmbed(title, description string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{Title: title, Description: description, Thumbnail: helpers.ThumbGuild})
}

func UnexpectedErrorTicketEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred. Please try again later or contact support.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

// TicketActionErrorEmbed explains why a claim or close could not be applied.
func TicketActionErrorEmbed(err error) *discordgo.MessageEmbed {
	switch {
	case errors.Is(err, ErrTicketNotFound):
		return ticketNotInChannelEmbed()
	case errors.Is(err, ErrTicketAlreadyClaimed):
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Already Claimed",
			Description: "This ticket has already been claimed. Only the staff member who claimed it can release it.",
			Thumbnail:   helpers.ThumbFlag,
		})
	case errors.Is(err, ErrTicketNotClaimed):
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Not Claimed",
			Description: "This ticket has not been claimed yet. Claim it first to take ownership.",
			Thumbnail:   helpers.ThumbFlag,
		})
	case errors.Is(err, ErrTicketLockUnchanged):
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Nothing To Change",
			Description: "The ticket is already in that state.",
			Thumbnail:   helpers.ThumbFlag,
		})
	case errors.Is(err, ErrTicketClaimDisabled):
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Claiming Disabled",
			Description: "Claiming is turned off for this panel. An administrator can enable it under the panel settings.",
			Thumbnail:   helpers.ThumbFlag,
		})
	}
	return UnexpectedErrorTicketEmbed()
}

// TicketContextErrorEmbed explains why a ticket could not be resolved.
func TicketContextErrorEmbed(err error) *discordgo.MessageEmbed {
	switch {
	case errors.Is(err, errNotTicketChannel):
		return ticketNotInChannelEmbed()
	case errors.Is(err, ErrTicketPanelNotFound):
		return ticketPanelMissingEmbed()
	}
	return UnexpectedErrorTicketEmbed()
}
