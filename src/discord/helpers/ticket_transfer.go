package helpers

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"ranked-bedwars/src/types"
)

// /ticket transfer: a ticket opened in the wrong category is forwarded to the right one. The ticket keeps its number, owner, history and
// channel; it takes the new category's staff, welcome message and name, and its claim is cleared because the new team has not taken it.

var ErrTicketSameCategory = errors.New("the ticket is already in that category")

const ticketStaffAllow = int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory | discordgo.PermissionAttachFiles | discordgo.PermissionEmbedLinks | discordgo.PermissionManageMessages)

// TicketTransferTargets are the categories a ticket can be forwarded to: the other categories of its panel.
func TicketTransferTargets(panel *types.InteractivePanel, current string) []*types.TicketCategory {
	var out []*types.TicketCategory
	if panel == nil || panel.Ticket == nil {
		return nil
	}
	for i := range panel.Ticket.Categories {
		if c := panel.Ticket.Categories[i]; c != nil && c.ID != current {
			out = append(out, c)
		}
	}
	return out
}

// TransferTicket forwards the ticket in threadID to the category categoryID of the same panel and returns it with the category it left.
func TransferTicket(s *discordgo.Session, guildID, threadID, staffID, categoryID, reason string) (*types.Ticket, *types.TicketCategory, error) {
	var ticket *types.Ticket
	var panel *types.InteractivePanel
	var from, to *types.TicketCategory
	err := Tickets.Update(staffID, guildID, func(ip *types.InteractivePanels) error {
		found := ip.Tickets.Open[threadID]
		if found == nil {
			return ErrTicketNotFound
		}
		panel = ip.Panels[found.PanelID]
		if panel == nil || panel.Ticket == nil {
			return ErrTicketPanelNotFound
		}
		if found.CategoryID == categoryID {
			return ErrTicketSameCategory
		}
		if to = FindTicketCategory(panel, categoryID); to == nil {
			return ErrTicketCategoryNotFound
		}
		from = FindTicketCategory(panel, found.CategoryID)
		if from == nil {
			from = &types.TicketCategory{ID: found.CategoryID, Label: found.CategoryLabel}
		}
		copied := *found
		ticket = &copied
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// The name follows the new category ("{username} - Ticket: {category}"), as a freshly opened ticket's would.
	moved := *ticket
	moved.CategoryID, moved.CategoryLabel = to.ID, to.Label
	format := to.ThreadNameFormat
	if ticket.Mode == types.TicketModeChannel {
		format = to.ChannelNameFormat
	}
	if format == "" {
		format = to.NameFormat
	}
	if format == "" {
		format = "{username} - Ticket: {category}"
	}
	name := strings.TrimSpace(TicketPlaceholders(format, &moved, panel, to, GuildDisplayName(s, guildID)))
	if name == "" {
		name = ticket.ThreadName
	}
	if ticket.Mode == types.TicketModeChannel {
		name = channelTicketName(name, ticket.Ref)
	}
	name = truncate(name, 100)

	edit := &discordgo.ChannelEdit{Name: name}
	if ticket.Mode == types.TicketModeChannel && to.CategoryID != "" {
		edit.ParentID = to.CategoryID
	}
	if _, err := s.ChannelEditComplex(threadID, edit); err != nil {
		return nil, nil, err
	}
	if ticket.Mode == types.TicketModeChannel {
		// The old category's own staff lose the channel (unless they are staff of the new one or of the whole panel); the new category's get it.
		keep := staffRoleIDs(panel, to)
		for _, roleID := range staffRoleIDs(panel, from) {
			if !slices.Contains(keep, roleID) {
				_ = s.ChannelPermissionDelete(threadID, roleID)
			}
		}
		for _, roleID := range keep {
			_ = s.ChannelPermissionSet(threadID, roleID, discordgo.PermissionOverwriteTypeRole, ticketStaffAllow, 0)
		}
	}

	now := time.Now().Unix()
	err = Tickets.Update(staffID, guildID, func(ip *types.InteractivePanels) error {
		found := ip.Tickets.Open[threadID]
		if found == nil {
			return ErrTicketNotFound
		}
		found.CategoryID, found.CategoryLabel, found.ThreadName = to.ID, to.Label, name
		found.ClaimedBy, found.ClaimedAt = "", 0
		message := "Ticket transferred from " + from.Label + " to " + to.Label + "."
		if reason != "" {
			message += " Reason: " + reason
		}
		appendTicketLog(found, "transferred", staffID, message, now)
		copied := *found
		ticket = &copied
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	RefreshTicketWelcome(s, guildID, ticket, panel)
	notice := "<@" + staffID + "> moved this ticket from **" + from.Label + "** to **" + to.Label + "**."
	if reason != "" {
		notice += "\n**Reason:** " + reason
	}
	ping := ""
	roles := pingRoleIDs(to)
	if to.PingStaff {
		for _, id := range roles {
			ping += "<@&" + id + "> "
		}
	}
	_, _ = s.ChannelMessageSendComplex(threadID, &discordgo.MessageSend{
		Content:         strings.TrimSpace(ping),
		Embeds:          []*discordgo.MessageEmbed{TicketNoticeEmbed("Ticket Transferred", notice)},
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}, Roles: roles},
	})
	PostTicketLog(s, panel, TicketLogMessage(ColorCaution, "Ticket Transferred", fmt.Sprintf("<@%s> transferred a ticket.", staffID), []string{
		"- **Ticket:** <#" + ticket.ThreadID + ">",
		"- **Reference:** `#" + ticket.Ref + "`",
		"- **From:** " + from.Label,
		"- **To:** " + to.Label,
		"- **Owner:** <@" + ticket.OwnerID + ">",
	}), nil)
	return ticket, from, nil
}
