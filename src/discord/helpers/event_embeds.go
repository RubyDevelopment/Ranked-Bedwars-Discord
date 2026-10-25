// This file holds every embed the bot posts on its own (not as the answer to a command): party and queue notices, the
// punishment log and alerts, ELO decay warnings, ticket messages, the plan notice and the hosted game's embeds, one
// function (or table entry) per embed, so they can be read and edited in one place. The embeds that answer a command are
// in command_embeds.go.
package helpers

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

// ============================================================================
// party
// ============================================================================

func UnexpectedErrorPartyEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing your party request. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyInvalidSubcommandEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "You must specify a valid subcommand. Select one from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You are not registered. Run `/register` to link a Minecraft account first.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyPlayerNotFoundEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "You must select a player to invite. Use the user selection menu to choose a registered player.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyBotTargetEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You cannot invite bot accounts to a party. Please select a registered player.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyTargetNotRegisteredEmbed(user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild. Ask them to run `/register` to link their Minecraft account before inviting them to a party.", user.ID),
		Thumbnail:   ThumbFlag,
	})
}

func PartyCreatedEmbed(inactivity int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Party Created",
		Description: fmt.Sprintf("Your party is ready! Invite players with `/party invite` to build your team.\n\n> **Leader:** You\n> **Members:** 1/4\n> **Invites:** Open\n> **Disbands After:** %d minutes of inactivity", inactivity),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyInviteSentEmbed(head, body string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       head,
		Description: body + "\n\nThe player can accept or decline the invitation from the party alerts channel.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyInviteSentNoAlertEmbed(head, body string) *discordgo.MessageEmbed {
	return Preset("caution", types.PresetOptions{
		Title:       head,
		Description: body + "\n\n> **Note:** I couldn't post the invitation to the party alerts channel. Ask the player to run `/party accept` with you as the leader to join manually.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyInvitationEmbed(targetID, leaderID string, members, maxSize int, expires time.Time) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title: "Party Invitation",
		Description: fmt.Sprintf(
			"<@%s>, <@%s> invited you to join their party!\n\n> **Party Size:** %d/%d\n> **Leader:** <@%s>\n> **Expires:** %s\n\nClick **Accept** to join or **Deny** to decline the invitation.",
			targetID, leaderID, members, maxSize, leaderID, DiscordRelativeTimestamp(expires),
		),
		Thumbnail: ThumbPlayers,
	})
}

func PartyInvitationDeclinedEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Invitation Declined",
		Description: fmt.Sprintf("<@%s> declined your party invitation. You can invite another player or ask them to request a new invitation.", userID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyDeclinedEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Invitation Declined",
		Description: "You declined the party invitation. If you change your mind, ask the leader to send you a new invitation.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyLeaderNotFoundEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "Select the party leader who invited you.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyJoinedEmbed(leaderID string, members, maxSize int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Party Joined",
		Description: fmt.Sprintf("You successfully joined <@%s>'s party!\n\n> **Party Size:** %d/%d\n> **Leader:** <@%s>\n\nYou can now queue together for matches.", leaderID, members, maxSize, leaderID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyLeftEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Left Party",
		Description: "You have successfully left the party. You can create a new party with `/party create` or join another party by accepting an invitation.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyPlayerLeftEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Player Left",
		Description: fmt.Sprintf("<@%s> has left your party. You can invite a new player to fill the slot with `/party invite`.", userID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyPlayerKickedEmbed(targetID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Player Removed",
		Description: fmt.Sprintf("<@%s> has been removed from your party. You can invite a new player to fill the slot with `/party invite`.", targetID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyLeadershipTransferredEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "You Are the Party Leader",
		Description: fmt.Sprintf("<@%s> has transferred party leadership to you. You can now manage the party, invite members, kick players, and disband the party.", userID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyLeadershipTransferredToLeaderEmbed(targetID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Leadership Transferred",
		Description: fmt.Sprintf("You have successfully transferred party leadership to <@%s>. They are now responsible for managing the party.", targetID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyDisbandedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been successfully disbanded. All members have been notified.\n\nYou can create a new party at any time with `/party create`.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyInvitesOpenedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Invites Open",
		Description: "Party invites have been reopened. Players can now be invited to your party using `/party invite`.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyInvitesClosedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Invites Closed",
		Description: "Party invites have been paused. No new players can be invited until you reopen invites with `/party invites`.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyNotInPartyEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in a Party",
		Description: "You are not currently in a party. Create a new party with `/party create` or accept a pending invitation from another player.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyTargetNotInPartyEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in a Party",
		Description: fmt.Sprintf("<@%s> is not currently in a party. They can create one with `/party create` or may have pending invitations to accept.", userID),
		Thumbnail:   ThumbFlag,
	})
}

func partyPlayerJoinedEmbed(userID string, members, maxSize int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Player Joined",
		Description: fmt.Sprintf("<@%s> has joined your party!\n\n> **Party Size:** %d/%d\n\nYour party is now ready to queue for matches.", userID, members, maxSize),
		Thumbnail:   ThumbPlayers,
	})
}

func partyDisbandedByLeaderEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "The party leader has disbanded the party. You can create a new party with `/party create` at any time.",
		Thumbnail:   ThumbPlayers,
	})
}

func partyDisbandedLastMemberEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded because all other members have left. You can create a new party with `/party create` at any time.",
		Thumbnail:   ThumbPlayers,
	})
}

func partyDisbandedInactivityEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded due to inactivity. Parties automatically disband after a period of no activity. You can create a new party with `/party create` at any time.",
		Thumbnail:   ThumbPlayers,
	})
}

func partyDisbandedSystemOffEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded because parties have been disabled on this server. Contact an administrator for more information.",
		Thumbnail:   ThumbPlayers,
	})
}

func partyKickedEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Removed from Party",
		Description: "You have been removed from the party by its leader. You can create a new party with `/party create` or accept invitations from other players.",
		Thumbnail:   ThumbFlag,
	})
}

func partyLeaderChangedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "You Are the Party Leader",
		Description: "The previous party leader has left, so you are now the party leader. You can manage the party, invite members, kick players, and disband the party.",
		Thumbnail:   ThumbPlayers,
	})
}

func partyNotYourInvitationEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not Your Invitation",
		Description: "Only the invited player can answer this invitation.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyErrorEmbed(err error) *discordgo.MessageEmbed {
	var d *EloDenial
	if errors.As(err, &d) {
		return Preset("error", types.PresetOptions{
			Title:       "Party ELO Limit",
			Description: fmt.Sprintf("This party would exceed the ELO limit: the **%s** ELO would be **%d**, above the limit of **%d**.", d.Method, d.Aggregated, d.Max),
			Thumbnail:   ThumbFlag,
		})
	}
	for _, e := range partyErrorTexts {
		if errors.Is(err, e.err) {
			return Preset("error", types.PresetOptions{Title: e.title, Description: e.text, Thumbnail: ThumbFlag})
		}
	}
	return nil
}

func PartyInfoEmbed(who string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Information",
		Description: fmt.Sprintf("View the current party details for <@%s>.", who),
		Fields:      fields,
		Thumbnail:   ThumbPlayers,
	})
}

// ============================================================================
// punishment
// ============================================================================

func punishmentSelfTargetEmbed(action string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: fmt.Sprintf("You cannot %s yourself. Select a different member to moderate.", action),
		Thumbnail:   ThumbFlag,
	})
}

func punishmentReasonRequiredEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Reason Required",
		Description: "You must provide a reason when issuing a punishment. Include a clear explanation in the reason field.",
		Thumbnail:   ThumbFlag,
	})
}

func punishmentInvalidDurationEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Duration",
		Description: "That duration format is not recognized. Use values like `30m`, `1d`, `2w`, or `perm`.",
		Thumbnail:   ThumbFlag,
	})
}

func punishmentHierarchyEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Hierarchy Error",
		Description: "That member cannot be moderated because their highest role is above or equal to mine. Adjust the role hierarchy or ask a higher-ranked moderator.",
		Thumbnail:   ThumbFlag,
	})
}

// PunishmentPlayerNotFoundEmbed is shown when the target has no player profile in this guild.
func PunishmentPlayerNotFoundEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "That player is not registered in this guild. They must register first before punishments can be managed.",
		Thumbnail:   ThumbFlag,
	})
}

func punishmentAlreadyActiveEmbed(adjective string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Punishment Already Active",
		Description: fmt.Sprintf("That player is already **%s**. Check `/punishments` for details.", adjective),
		Thumbnail:   ThumbFlag,
	})
}

func punishmentNotActiveEmbed(adjective string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Punishment Not Active",
		Description: fmt.Sprintf("That player is not currently **%s**. Verify their status with `/punishments`.", adjective),
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// queue
// ============================================================================

func queueDeniedEmbed(description string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Queue Entry Denied",
		Description: description,
		Thumbnail:   ThumbFlag,
	})
}

func queuePartyEmbed(title, description string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       title,
		Description: description,
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// guild
// ============================================================================

// unassignedGuildEmbed is the notice posted when the bot joins a guild that has no plan yet.
func unassignedGuildEmbed() *discordgo.MessageEmbed {
	return Preset("caution", types.PresetOptions{
		Title:       "Ranked Bedwars Is Connected",
		Description: "Ranked Bedwars has joined this guild and created its configuration. A plan is required before the bot can be used.",
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Getting Started",
				Value:  "1. An administrator opens the dashboard.\n2. Choose a plan, select this guild and request access.\n3. Once the request is approved, the bot switches on.",
				Inline: false,
			},
			{
				Name:   "Until Then",
				Value:  "Commands and buttons stay paused. The setup and configuration commands keep working, so the guild can be prepared in advance.",
				Inline: false,
			},
			{
				Name:   "Need Help?",
				Value:  "Contact support through the dashboard and include the name of this guild.",
				Inline: false,
			},
		},
		Thumbnail: ThumbGuild,
	})
}

// ============================================================================
// ticket
// ============================================================================

// TicketWelcomeMessage renders the configurable welcome and ticket details.
func TicketWelcomeMessage(ticket *types.Ticket, panel *types.InteractivePanel, category *types.TicketCategory, guildName string) *discordgo.MessageEmbed {
	welcome := category.Welcome
	color := welcome.Color
	if color == 0 {
		color = defaultPanelColor
	}
	if welcome.NoColor {
		color = EmbedBlendColor
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

// TicketLeaderboardMessage ranks the staff who have claimed the most tickets.
func TicketLeaderboardMessage(rows []TicketLeaderboardRow, viewerID string) *discordgo.MessageEmbed {
	if len(rows) == 0 {
		return &discordgo.MessageEmbed{
			Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: ThumbGuild},
			Title:       "Ticket Leaderboard",
			Description: "No tickets have been claimed yet. Claim a ticket to get on the board.",
			Color:       ColorInfo,
		}
	}

	medals := []string{"🥇", "🥈", "🥉"}
	var board strings.Builder
	shown := rows
	if len(shown) > 10 {
		shown = shown[:10]
	}
	for idx, row := range shown {
		place := fmt.Sprintf("`#%d`", idx+1)
		if idx < len(medals) {
			place = medals[idx]
		}
		fmt.Fprintf(&board, "%s <@%s> - **%d** claimed, **%d** closed\n", place, row.StaffID, row.Stats.Claimed, row.Stats.Closed)
	}

	embed := &discordgo.MessageEmbed{
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: ThumbGuild},
		Title:       "Ticket Leaderboard",
		Description: "The staff members who have claimed the most tickets.\n\n" + board.String(),
		Color:       ColorDefault,
	}
	for idx, row := range rows {
		if row.StaffID == viewerID && idx >= len(shown) {
			embed.Footer = &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Your rank: #%d with %d claimed", idx+1, row.Stats.Claimed)}
			break
		}
	}
	return embed
}

// TicketStatsMessage shows one staff member's ticket numbers.
func TicketStatsMessage(staffID string, stats types.TicketStaffStats, rank, total, activeClaims int) *discordgo.MessageEmbed {
	rankText := "Unranked"
	if rank > 0 {
		rankText = fmt.Sprintf("#%d of %d", rank, total)
	}
	return &discordgo.MessageEmbed{
		Thumbnail: &discordgo.MessageEmbedThumbnail{URL: ThumbGuild},
		Title:     "Ticket Stats",
		Description: fmt.Sprintf(
			"<@%s>\n\n- **Claimed:** %d\n- **Closed:** %d\n- **Currently Claimed:** %d\n- **Leaderboard Rank:** %s",
			staffID, stats.Claimed, stats.Closed, activeClaims, rankText,
		),
		Color: ColorDefault,
	}
}

// TicketLogsMessage lists the most recently closed tickets.
func TicketLogsMessage(entries []*types.Ticket, filter string) *discordgo.MessageEmbed {
	if len(entries) == 0 {
		return &discordgo.MessageEmbed{
			Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: ThumbGuild},
			Title:       "Ticket Logs",
			Description: "No closed tickets matched your search.",
			Color:       ColorInfo,
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
	return &discordgo.MessageEmbed{Title: "Ticket Logs", Description: truncate(description, 4000), Color: ColorDefault}
}

func ticketCreatedEmbed(threadID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Ticket Created",
		Description: fmt.Sprintf("Your ticket was created: <#%s>. Continue the conversation there.", threadID),
		Thumbnail:   ThumbGuild,
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
		return Preset("caution", types.PresetOptions{
			Title: "Ticket Limit Reached",
			Description: fmt.Sprintf(
				"You can only have **%d** open ticket(s) for this panel. Continue in %s or close it before opening a new one.",
				limit.Limit, strings.Join(mentions, ", "),
			),
			Thumbnail: ThumbGuild,
		})
	case errors.Is(err, ErrTicketPanelNotFound), errors.Is(err, ErrTicketCategoryNotFound):
		return Preset("error", types.PresetOptions{
			Title:       "Panel Outdated",
			Description: "This panel is no longer available. Ask staff to send a fresh one.",
			Thumbnail:   ThumbFlag,
		})
	}
	return Preset("error", types.PresetOptions{
		Title:       "Ticket Not Created",
		Description: "Your ticket could not be created. The bot may be missing permission to create private threads in this channel. Please contact staff.",
		Thumbnail:   ThumbFlag,
	})
}

func TicketBotTargetEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be part of tickets. Select a member instead.",
		Thumbnail:   ThumbFlag,
	})
}

func TicketPermissionDeniedEmbed(reason string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Permission Denied",
		Description: reason,
		Thumbnail:   ThumbFlag,
	})
}

func ticketNotInChannelEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not A Ticket",
		Description: "This command must be run inside an open ticket thread.",
		Thumbnail:   ThumbFlag,
	})
}

func ticketPanelMissingEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Panel Not Found",
		Description: "The panel for this ticket no longer exists. Ask an administrator to check its configuration in the Ranked Bedwars Dashboard.",
		Thumbnail:   ThumbFlag,
	})
}

func TicketClaimedEmbed(staffID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Ticket Claimed",
		Description: fmt.Sprintf("Successfully claimed this ticket.\n\n> **Claimed By:** <@%s>", staffID),
		Thumbnail:   ThumbGuild,
	})
}

func TicketMemberAddedEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Member Added",
		Description: fmt.Sprintf("Successfully added <@%s> to this ticket.", userID),
		Thumbnail:   ThumbGuild,
	})
}

func TicketMemberRemovedEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Member Removed",
		Description: fmt.Sprintf("Successfully removed <@%s> from this ticket.", userID),
		Thumbnail:   ThumbGuild,
	})
}

// TicketAccessResultEmbed reports the outcome of /ticket add or /ticket remove.
func TicketAccessResultEmbed(ok bool, description string) *discordgo.MessageEmbed {
	kind, title := "success", "Ticket Access Updated"
	if !ok {
		kind, title = "error", "Nothing To Change"
	}
	return Preset(kind, types.PresetOptions{Title: title, Description: description, Thumbnail: ThumbGuild})
}

func TicketRemoveOwnerEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "The ticket owner cannot be removed from their own ticket. Close the ticket instead.",
		Thumbnail:   ThumbFlag,
	})
}

func UnexpectedErrorTicketEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

// TicketActionErrorEmbed explains why a claim or close could not be applied.
func TicketActionErrorEmbed(err error) *discordgo.MessageEmbed {
	switch {
	case errors.Is(err, ErrTicketNotFound):
		return ticketNotInChannelEmbed()
	case errors.Is(err, ErrTicketAlreadyClaimed):
		return Preset("error", types.PresetOptions{
			Title:       "Already Claimed",
			Description: "This ticket has already been claimed. Only the staff member who claimed it can release it.",
			Thumbnail:   ThumbFlag,
		})
	case errors.Is(err, ErrTicketNotClaimed):
		return Preset("error", types.PresetOptions{
			Title:       "Not Claimed",
			Description: "This ticket has not been claimed yet. Claim it first to take ownership.",
			Thumbnail:   ThumbFlag,
		})
	case errors.Is(err, ErrTicketClaimDisabled):
		return Preset("error", types.PresetOptions{
			Title:       "Claiming Disabled",
			Description: "Claiming is turned off for this panel. An administrator can enable it under the panel settings.",
			Thumbnail:   ThumbFlag,
		})
	}
	return UnexpectedErrorTicketEmbed()
}

// ============================================================================
// player
// ============================================================================

// BuildStrikeHistoryEmbed renders the strike history embed for one page.
func BuildStrikeHistoryEmbed(username string, entries []models.PlayerStrikesModel, page int) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** strike(s) on record.", len(entries))
	if len(entries) == 0 {
		description = "No strike history was found for this player."
	} else {
		description += "\n\n" + strings.Join(BuildStrikeHistoryDescriptionsDirect(entries, page), "\n\n")
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's Strike History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// BuildDecayHistoryEmbed renders the ELO decay history embed for one page.
func BuildDecayHistoryEmbed(username string, entries []models.PlayerDecaysModel, page int) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	totalDecayed, voided := 0, 0
	for _, entry := range entries {
		if entry.IsVoided {
			voided++
			continue
		}
		totalDecayed += entry.AmountDeducted
	}
	description := fmt.Sprintf("This player has lost **%d** ELO across **%d** decay(s) this season.", totalDecayed, len(entries)-voided)
	if voided > 0 {
		description += fmt.Sprintf(" **%d** voided decay(s) are not counted.", voided)
	}
	if len(entries) == 0 {
		description = "No decay history was found for this player."
	} else {
		description += "\n\n" + strings.Join(BuildDecayHistoryDescriptionsDirect(entries, page), "\n\n")
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's ELO Decay History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// BuildPunishmentHistoryEmbed renders the punishment history embed for one page.
func BuildPunishmentHistoryEmbed(username string, entries []models.PlayerPunishmentsModel, page int, activeOnly bool) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** punishment(s) on record.", len(entries))
	if len(entries) == 0 {
		description = "No punishment history was found for this player."
	} else {
		description += "\n\n" + strings.Join(BuildPunishmentHistoryDescriptionsDirect(entries, page), "\n\n")
	}
	titlePrefix := "Punishment History"
	if activeOnly {
		titlePrefix = "Active Punishments"
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's %s • Page %d of %d", username, titlePrefix, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// BuildGameHistoryEmbed renders the game history embed for one page.
func BuildGameHistoryEmbed(username string, entries []sdk.GameHistory, page int) *discordgo.MessageEmbed {
	return BuildGameHistoryEmbedWithPlayers(username, entries, page, nil)
}

// BuildGameHistoryEmbedWithPlayers renders game history with the full participant lists.
func BuildGameHistoryEmbedWithPlayers(username string, entries []sdk.GameHistory, page int, players map[uint][]GameHistoryPlayer) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** game(s) on record for the current season.", len(entries))
	if len(entries) == 0 {
		description = "No game history was found for this player."
	} else {
		description += "\n\n" + strings.Join(buildGameHistoryDescriptions(entries, page, players), "\n\n")
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's Game History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// ============================================================================
// plan
// ============================================================================

func inactiveEmbed(guildID string, disabled bool, expires *time.Time) *discordgo.MessageEmbed {
	if disabled {
		return &discordgo.MessageEmbed{Title: "Server Disabled", Description: "Ranked Bedwars has been disabled for this server. Contact support if you think this is a mistake.", Color: ColorError}
	}
	desc := "This server doesn't have an active plan, so Ranked Bedwars is paused."
	if expires != nil {
		desc = fmt.Sprintf("This server's plan ended on <t:%d:D>, so Ranked Bedwars is paused.", expires.Unix())
	}
	desc += " An administrator can request a plan or a renewal in the dashboard"
	if url := planPageURL(guildID); url != "" {
		desc += ": " + url
	}
	desc += ". Settings and setup commands still work meanwhile."
	return &discordgo.MessageEmbed{Title: "Plan Inactive", Description: desc, Color: ColorError}
}

// ============================================================================
// command
// ============================================================================

// optionalAvailableEmbed tells an administrator every required slot is now
// configured and lists cmdName's remaining optional ones, pairing with
// buildOptionalPromptComponents' Continue/Skip buttons.
func optionalAvailableEmbed(cmdName string, optional []types.Requirement) *discordgo.MessageEmbed {
	description := fmt.Sprintf("All required resources for `%s` are now configured, and it's ready to use.\n\n`%s` also has these optional resources available:\n", cmdName, cmdName)
	for _, req := range optional {
		description += fmt.Sprintf("- %s: %s\n", req.Type, req.Key)
	}
	description += "\nWould you like to configure them now, or skip for now? You're able to configure these at any time using `/config`."
	return Preset("caution", types.PresetOptions{
		Title:       "Optional Configuration Available",
		Description: description,
		Thumbnail:   ThumbFlag,
	})
}

func configurationCompleteEmbed(cmdName string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Configuration Complete",
		Description: fmt.Sprintf("`%s` is ready to use. Please re-run the command. You're able to configure it further at any time using `/config`.", cmdName),
		Thumbnail:   ThumbTada,
	})
}

// ============================================================================
// interaction
// ============================================================================

// PunishmentHierarchyEmbed is shown when role hierarchy blocks moderation.
func PunishmentHierarchyEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Hierarchy Error",
		Description: "That member cannot be moderated because their highest role is above or equal to mine. Adjust the role hierarchy or ask a higher-ranked moderator.",
	})
}

// InvalidTimeFilterEmbed is shown when a time filter string is invalid.
func InvalidTimeFilterEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Time Filter",
		Description: "That time filter format is not recognized. Use values like `1d`, `7d`, `2w`, or omit the filter to show all history.",
	})
}

// ============================================================================
// The hosted game's embeds (game_helper.go renders them; a guild's changes are laid over these defaults)
// ============================================================================

// The old bot's thumbnails.
const (
	ThumbDoubleSwords = "https://i.imgur.com/YPxilj8.png"
	ThumbTimer        = "https://i.imgur.com/Ixj8JKk.png"
	ThumbTada         = "https://i.imgur.com/M5yz7dL.png"
	ThumbSprout       = "https://i.imgur.com/XHnyZh9.png"
	ThumbFlag         = "https://i.imgur.com/C2W5O9t.png"
	ThumbTrashCan     = "https://i.imgur.com/bKH6mWU.png"
)

func thumb(t embedTemplate, url string) embedTemplate   { t.Thumbnail = &url; return t }
func colored(t embedTemplate, hex string) embedTemplate { t.Color = &hex; return t }

// statusFields is how the old party embeds listed who was still missing and who had joined.
var statusFields = []embedField{
	{Name: DotRed + " Waiting ({waitingCount})", Value: "{waiting}", Inline: true},
	{Name: DotGreen + " Joined ({joinedCount}/{playerCount})", Value: "{joined}", Inline: true},
}

var teamStatusField = embedField{Name: "Team {team}", Value: "{teamPlayers}", Inline: true, Each: "team"}

var submitYourWin = thumb(tpl("info", "Submit Your Win",
	"When your match ends, send a **screenshot of the win screen** in this channel.\n\nYou can submit **3 minutes** after the match starts.\n\nRun **`/submit`** and attach the image, or send **`=submit`** with the screenshot attached to the same message."), ThumbSprout)

// The defaults are the old bot's embeds, word for word. Embeds the old bot never had are switched off.
var defaultEmbeds = map[string]embedTemplate{
	// Pre-game
	"introduction": func() embedTemplate {
		t := thumb(tpl("success", "Welcome to Game #{game}", "Welcome to **Standard {mode}** teams have been automatically assigned. Defend your bed, destroy enemy beds, and fight until one team remains. Your team voice channel is ready, join it to coordinate with your team!", teamField), ThumbDoubleSwords)
		t.Ping = yes(true)
		return t
	}(),
	"teams":            thumb(tpl("success", "Drafting Complete", "Captain drafting is finished. Team voice channels are being prepared.", teamField), ThumbTada),
	"botConnecting":    off(tpl("pending", "Host Joining", "`{bot}` is joining the server to host your match.")),
	"privateGameReady": off(tpl("info", "Private Game Ready", "`{bot}` opened the private game. Party invites are next.")),
	"partyInvitation": thumb(tpl("pending", "Party Invitation",
		"The host is sending party invites now. Accept the in-game invite to join.\n\n**Not getting an invite?**\n1. Make sure your status is **Online**.\n2. Make sure **Party Invites** are **Enabled**.\n3. Make sure you are using the correct username.\n\nYou can also join manually with `/party join {bot}`.",
		append([]embedField{
			{Name: "Host", Value: "`{bot}`", Inline: true},
			{Name: "Ending", Value: "<t:{endsAt}:R>", Inline: true},
			{Name: "​", Value: "​", Inline: true},
		}, statusFields...)...), ThumbTimer),
	"partyReady": thumb(tpl("success", "Party Ready", "Everyone has joined the party. The match is about to begin, and the party will be warped to **{mode}** shortly.\n\nDo not leave the party.",
		append([]embedField{{Name: "Host", Value: "`{bot}`"}}, statusFields...)...), ThumbTada),
	"partyTimeout": thumb(tpl("error", "Party Timeout", "The game was **voided** because not everyone joined the party in time.\n\nPlayers who did not join were penalized automatically.",
		statusFields...), ThumbTrashCan),
	// Not in the old bot, but only posted when a step is stuck: it tells whoever set the server up what to fix.
	"hostProblem": thumb(tpl("error", "Hosting Problem", "The hosting bot got stuck on **{step}**.\n\n> **What happened:** {reason}\n\n{hint}",
		embedField{Name: "What the bot last saw", Value: "```{diagnosis}```"},
		embedField{Name: "For whoever set this server up", Value: "Lines starting with ⚠ had no rule that reacted to them. Fix it in the dashboard under Game Server → Minecraft Server → Server automations (Rules and Commands), then host again."}), ThumbFlag),
	"mapSelected": thumb(tpl("success", "Map Voting Ended", "The map selection has concluded! The winning map is **{map}**. Please wait while the game prepares the lobby."), ThumbTada),
	"teamSelection": thumb(tpl("pending", "Team Selection",
		"The bot is checking that every player is on their assigned team.\n\n**What to do:**\n1. Open the **Team Selector** item.\n2. Join your assigned **Team Color**.\n\n**Ends:** <t:{endsAt}:R>",
		teamStatusField), ThumbTimer),
	"teamsVerified": thumb(tpl("success", "Teams Verified", "Every player is on their assigned team. The game is ready to start.", teamStatusField), ThumbTada),
	"teamTimeout":   thumb(tpl("error", "Team Selection Timeout", "Not every player was on their assigned team in time. Check that each player is on the correct team before starting."), ThumbFlag),

	// In-game
	"matchStarted":       thumb(tpl("success", "Match Started", "The game has started and the Team Selector is closed. The rosters below are the teams for this match.", teamStatusField), ThumbTada),
	"matchStartedManual": submitYourWin,
	"liveTracker": tpl("info", "Game #{game} Live Match Tracker",
		"Here is a real-time team rosters and elimination status for the `{mode}` match!\n\n**Time Elapsed:** <t:{startedAt}:R>",
		embedField{Name: "{team} {bed}", Value: "{teamPlayers}", Inline: true, Each: "team"}),
	"bedDestroyed":   off(tpl("caution", "Bed Destroyed", "**{team}**'s bed was destroyed by {destroyer}.")),
	"finalKill":      off(tpl("info", "Final Kill", "{killer} eliminated {victim}.")),
	"teamEliminated": off(tpl("caution", "Team Eliminated", "**{team}** has been eliminated.")),
	"phaseChanged":   off(tpl("info", "{phaseName}", "The match moved on to phase {phase}.")),
	"statTracked":    off(tpl("info", "{stat}", "{player} +{amount} ({total} total)")),

	// Post-game
	"matchEnded": off(tpl("info", "Game #{game} • Match Over", "**{winner}** won the match. The result is being scored.",
		embedField{Name: "Match stats", Value: "{statTable}"})),
	"scored":     thumb(colored(tpl("default", "Match Finished", "**Game #{game}**\n\n**Winners:** {winnerMentions}\n**Losers:** {loserMentions}"), "#dc4d4d"), ThumbTada),
	"casualDone": thumb(colored(tpl("success", "Casual Match Logged • Game #{game}", "**Result:** Tied · no team won.\n\nThe match is complete and this game room is closing."), "#57f287"), ThumbTada),
	"staffPending": thumb(colored(tpl("pending", "Win Submitted • Game #{game}",
		"**Status:** Pending staff scoring\n**Claimed winner:** {winner}\n\nThe match was moved to the review category. Staff will score it shortly."), "#fee75c"), ThumbTimer),
	"trackingLost": submitYourWin,
	"voided":       thumb(tpl("error", "Game Voided", "This game was voided and will not count towards your stats.\n\n**Reason:** {reason}"), ThumbFlag),
}

// ============================================================================
// punishments and ELO decay (posted by the trackers)
// ============================================================================

// PunishmentExpiredLogEmbed is the line the moderation log gets when a punishment runs out.
func PunishmentExpiredLogEmbed(userID string, p models.PlayerPunishmentsModel) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "Punishment Expired",
		Description: fmt.Sprintf("> **Player:** <@%s>\n> **Type:** `%s`\n> **Reason:** %s", userID, p.Type, p.Reason),
		Timestamp:   time.Now().Format(time.RFC3339),
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: ThumbHammer},
	}
}

// PunishmentAlertEmbed is the alert a punishment that was applied, revoked or expired posts to the alerts channel.
func PunishmentAlertEmbed(action string, user *discordgo.User, username, pType, duration, reason string) *discordgo.MessageEmbed {
	title := "Punishment Applied"
	if action == "revoked" {
		title = "Punishment Revoked"
	} else if action == "expired" {
		title = "Punishment Expired"
	}

	description := fmt.Sprintf("> **Player:** <@%s> (**%s**)\n> **Type:** `%s`\n", user.ID, username, pType)
	if duration != "" && duration != "removed" {
		description += fmt.Sprintf("> **Duration:** `%s`\n", duration)
	}
	description += fmt.Sprintf("> **Reason:** %s", reason)

	return &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Timestamp:   time.Now().Format(time.RFC3339),
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: ThumbHammer},
	}
}

// DecayWarningEmbed warns a player that their ELO is about to decay.
func DecayWarningEmbed(n DecayNotice) *discordgo.MessageEmbed {
	return Preset("caution", types.PresetOptions{
		Title:       "ELO Decay Incoming",
		Description: fmt.Sprintf("You have been inactive for a while. Unless you play a ranked game before <t:%d:R>, you will lose **%d ELO** (%d → %d).", n.At.Unix(), n.Amount, n.Before, n.After),
		Thumbnail:   ThumbClock,
	})
}

// DecayAppliedEmbed tells a player their ELO decayed.
func DecayAppliedEmbed(n DecayNotice) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "ELO Decayed",
		Description: fmt.Sprintf("%d days without a ranked game cost you **%d ELO** (%d → %d). Play a ranked game to stop the decay.", n.InactivityDays, n.Amount, n.Before, n.After),
		Thumbnail:   ThumbFlag,
	})
}
