package staff

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/tickets"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(TicketManageCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// TicketManageCommand is the support ticket system: dashboard panel publishing
// and claim, close, leaderboard and log tools for staff.
var TicketManageCommand = &types.Command{
	Name:         "ticket",
	Requirements: types.StaffRequirements,
	Description:  "Manages ticket panel publishing, claims, members, and logs.",
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "panel", Description: "Publish a ticket panel created in the dashboard", Type: discordgo.ApplicationCommandOptionSubCommand},
			{Name: "leaderboard", Description: "View the staff members who claimed the most tickets", Type: discordgo.ApplicationCommandOptionSubCommand},
			{
				Name: "stats", Description: "View a staff member's ticket statistics", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "Staff member to view (defaults to you)", Type: discordgo.ApplicationCommandOptionUser, Required: false},
				},
			},
			{
				Name: "logs", Description: "Browse recently closed tickets", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "Only show tickets opened by this user", Type: discordgo.ApplicationCommandOptionUser, Required: false},
				},
			},
			{Name: "claim", Description: "Claim the ticket you are in", Type: discordgo.ApplicationCommandOptionSubCommand},
			{
				Name: "close", Description: "Close the ticket you are in", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "reason", Description: "Why the ticket is being closed", Type: discordgo.ApplicationCommandOptionString, Required: false},
				},
			},
			{
				Name: "rename", Description: "Rename the ticket you are in", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "name", Description: "The new name", Type: discordgo.ApplicationCommandOptionString, Required: true, MaxLength: 100},
				},
			},
			{
				Name: "transfer", Description: "Forward the ticket you are in to the right category", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "category", Description: "The ticket panel category to forward it to", Type: discordgo.ApplicationCommandOptionString, Required: true, Autocomplete: true},
					{Name: "reason", Description: "Why it belongs there (shown to the new team)", Type: discordgo.ApplicationCommandOptionString, Required: false, MaxLength: 300},
				},
			},
			{Name: "lock", Description: "Lock the ticket you are in so the owner can no longer write", Type: discordgo.ApplicationCommandOptionSubCommand},
			{Name: "unlock", Description: "Unlock the ticket you are in", Type: discordgo.ApplicationCommandOptionSubCommand},
			{
				Name: "add", Description: "Add a user and/or a role to the ticket you are in", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "Discord user to add", Type: discordgo.ApplicationCommandOptionUser, Required: false},
					{Name: "role", Description: "Role to add (every member of it in a thread ticket)", Type: discordgo.ApplicationCommandOptionRole, Required: false},
				},
			},
			{
				Name: "remove", Description: "Remove a user and/or a role from the ticket you are in", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "Discord user to remove", Type: discordgo.ApplicationCommandOptionUser, Required: false},
					{Name: "role", Description: "Role to remove (every member of it in a thread ticket)", Type: discordgo.ApplicationCommandOptionRole, Required: false},
				},
			},
		},
	},
	Execute:      executeTicketManageCommand,
	Autocomplete: autocompleteTicketCategory,
}

// ============================================================================
// Command Execution
// ============================================================================

// executeTicketManageCommand routes the selected subcommand to its handler.
func executeTicketManageCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	sub, _ := types.SelectedSubcommand(i)
	switch sub {
	case "panel":
		return executeTicketPanel(s, i)
	case "leaderboard":
		return executeTicketLeaderboard(s, i)
	case "stats":
		return executeTicketStats(s, i)
	case "logs":
		return executeTicketLogs(s, i)
	case "claim":
		return executeTicketClaim(s, i)
	case "close":
		return executeTicketClose(s, i)
	case "rename":
		return executeTicketRename(s, i)
	case "transfer":
		return executeTicketTransfer(s, i)
	case "lock":
		return executeTicketLock(s, i, true)
	case "unlock":
		return executeTicketLock(s, i, false)
	case "add":
		return changeTicketMember(s, i, true)
	case "remove":
		return changeTicketMember(s, i, false)
	}
	return helpers.EditInteractionEmbed(s, i, TicketManageInvalidSubcommandEmbed(i))
}

// executeTicketPanel opens the panel publishing flow for administrators.
func executeTicketPanel(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !tickets.InteractionIsAdmin(i) {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketPermissionDeniedEmbed("Only administrators can publish ticket panels. Create and edit panels in the dashboard."))
	}

	ip, err := tickets.Tickets.LoadOpen(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.UnexpectedErrorTicketEmbed())
	}
	return EditInteractionV2(s, i, tickets.TicketPublishPanelScreen(tickets.SortedTicketPanels(ip))...)
}

// executeTicketLeaderboard ranks staff by claimed tickets.
func executeTicketLeaderboard(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ip, err := tickets.Tickets.LoadOpen(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.UnexpectedErrorTicketEmbed())
	}
	return helpers.EditInteractionEmbed(s, i, TicketLeaderboardMessage(TicketLeaderboard(ip), helpers.InteractionUserID(i)))
}

// executeTicketStats shows one staff member's numbers.
func executeTicketStats(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	_, options := types.SelectedSubcommand(i)
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		target = i.Member.User
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketBotTargetEmbed())
	}

	ip, err := tickets.Tickets.LoadOpen(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.UnexpectedErrorTicketEmbed())
	}

	stats := types.TicketStaffStats{}
	if row, ok := ip.Tickets.Stats[target.ID]; ok && row != nil {
		stats = *row
	}
	board := TicketLeaderboard(ip)
	rank := 0
	for idx, row := range board {
		if row.StaffID == target.ID {
			rank = idx + 1
			break
		}
	}
	activeClaims := 0
	for _, ticket := range ip.Tickets.Open {
		if ticket.ClaimedBy == target.ID {
			activeClaims++
		}
	}
	return helpers.EditInteractionEmbed(s, i, TicketStatsMessage(target.ID, stats, rank, len(board), activeClaims))
}

// executeTicketLogs lists recently closed tickets for staff.
func executeTicketLogs(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ip, err := tickets.Tickets.Load(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.UnexpectedErrorTicketEmbed())
	}
	if !IsStaffOfAnyPanel(i, ip) {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketPermissionDeniedEmbed("Only support staff can browse ticket logs."))
	}

	_, options := types.SelectedSubcommand(i)
	target := helpers.SubOptionUser(s, options, "user")

	entries := make([]*types.Ticket, 0, len(ip.Tickets.History))
	for _, ticket := range ip.Tickets.History {
		if target == nil || ticket.OwnerID == target.ID {
			entries = append(entries, ticket)
		}
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].ClosedAt > entries[b].ClosedAt })
	if len(entries) > 10 {
		entries = entries[:10]
	}

	filter := ""
	if target != nil {
		filter = "Tickets opened by <@" + target.ID + ">"
	}
	return helpers.EditInteractionEmbed(s, i, tickets.TicketLogsMessage(entries, filter))
}

// executeTicketClaim claims the ticket the command is run in.
func executeTicketClaim(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ctx, err := tickets.LoadOrAdoptTicketContext(s, i.GuildID, i.ChannelID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketContextErrorEmbed(err))
	}
	if !tickets.IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketPermissionDeniedEmbed("Only support staff can claim tickets."))
	}

	if _, err := tickets.ClaimTicket(s, i.GuildID, i.ChannelID, helpers.InteractionUserID(i)); err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketActionErrorEmbed(err))
	}
	return helpers.EditInteractionEmbed(s, i, TicketClaimedEmbed(helpers.InteractionUserID(i)))
}

// executeTicketRename renames the ticket the command is run in.
func executeTicketRename(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ctx, err := tickets.LoadOrAdoptTicketContext(s, i.GuildID, i.ChannelID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketContextErrorEmbed(err))
	}
	if !tickets.IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketPermissionDeniedEmbed("Only support staff can rename tickets."))
	}
	_, options := types.SelectedSubcommand(i)
	name := helpers.SubOptionString(options, "name")
	if name == "" {
		return helpers.EditInteractionEmbed(s, i, TicketAccessResultEmbed(false, "Give the ticket a name."))
	}
	applied, _, err := tickets.RenameTicket(s, i.GuildID, i.ChannelID, helpers.InteractionUserID(i), name)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketActionErrorEmbed(err))
	}
	return helpers.EditInteractionEmbed(s, i, tickets.TicketNoticeEmbed("Ticket Renamed", "This ticket is now called **"+applied+"**."))
}

// executeTicketTransfer forwards the ticket the command is run in to another category of its panel.
func executeTicketTransfer(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ctx, err := tickets.LoadOrAdoptTicketContext(s, i.GuildID, i.ChannelID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketContextErrorEmbed(err))
	}
	if !tickets.IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketPermissionDeniedEmbed("Only support staff can transfer tickets."))
	}
	_, options := types.SelectedSubcommand(i)
	category := helpers.SubOptionString(options, "category")
	reason := strings.TrimSpace(helpers.SubOptionString(options, "reason"))
	if category == "" {
		return helpers.EditInteractionEmbed(s, i, TicketAccessResultEmbed(false, "Choose the category to forward this ticket to."))
	}
	// The option's value is the category's id (from the suggestions); typing its name works too.
	for _, c := range TicketTransferTargets(ctx.Panel, ctx.Ticket.CategoryID) {
		if strings.EqualFold(c.Label, category) {
			category = c.ID
		}
	}
	ticket, _, err := tickets.TransferTicket(s, i.GuildID, i.ChannelID, helpers.InteractionUserID(i), category, reason)
	switch {
	case errors.Is(err, tickets.ErrTicketSameCategory):
		return helpers.EditInteractionEmbed(s, i, TicketAccessResultEmbed(false, "This ticket is already in that category."))
	case errors.Is(err, tickets.ErrTicketCategoryNotFound):
		return helpers.EditInteractionEmbed(s, i, TicketAccessResultEmbed(false, "That category does not exist on this ticket's panel. Pick one from the suggestions."))
	case err != nil:
		return helpers.EditInteractionEmbed(s, i, tickets.TicketActionErrorEmbed(err))
	}
	return helpers.EditInteractionEmbed(s, i, tickets.TicketNoticeEmbed("Ticket Transferred", "This ticket now belongs to **"+ticket.CategoryLabel+"**."))
}

// autocompleteTicketCategory suggests the panel categories the ticket of the channel can be forwarded to.
func autocompleteTicketCategory(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_, opts := types.SelectedSubcommand(i)
	typed := ""
	for _, o := range opts {
		if o.Focused && o.Name == "category" {
			typed = strings.ToLower(o.StringValue())
		}
	}
	var choices []*discordgo.ApplicationCommandOptionChoice
	if ctx, err := tickets.LoadOrAdoptTicketContext(s, i.GuildID, i.ChannelID); err == nil && tickets.IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		for _, c := range TicketTransferTargets(ctx.Panel, ctx.Ticket.CategoryID) {
			if strings.Contains(strings.ToLower(c.Label), typed) && len(choices) < 25 {
				choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: c.Label, Value: c.ID})
			}
		}
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}

// executeTicketLock locks or unlocks the ticket the command is run in.
func executeTicketLock(s *discordgo.Session, i *discordgo.InteractionCreate, locked bool) error {
	ctx, err := tickets.LoadOrAdoptTicketContext(s, i.GuildID, i.ChannelID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketContextErrorEmbed(err))
	}
	if !tickets.IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketPermissionDeniedEmbed("Only support staff can lock or unlock tickets."))
	}
	if _, err := tickets.SetTicketLocked(s, i.GuildID, i.ChannelID, helpers.InteractionUserID(i), locked); err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketActionErrorEmbed(err))
	}
	if locked {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketNoticeEmbed("Ticket Locked", "The owner can no longer write here until it is unlocked."))
	}
	return helpers.EditInteractionEmbed(s, i, tickets.TicketNoticeEmbed("Ticket Unlocked", "The owner can write in this ticket again."))
}

// executeTicketClose asks for confirmation before closing the ticket the command is run in.
func executeTicketClose(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ctx, err := tickets.LoadOrAdoptTicketContext(s, i.GuildID, i.ChannelID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketContextErrorEmbed(err))
	}
	if !tickets.CanCloseTicket(i, ctx) {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketPermissionDeniedEmbed("Only support staff can close this ticket."))
	}

	_, options := types.SelectedSubcommand(i)
	if reason := strings.TrimSpace(helpers.SubOptionString(options, "reason")); reason != "" {
		if err := tickets.SetPendingCloseReason(helpers.InteractionUserID(i), i.GuildID, i.ChannelID, reason); err != nil {
			return helpers.EditInteractionEmbed(s, i, tickets.UnexpectedErrorTicketEmbed())
		}
	}
	confirm := tickets.TicketCloseConfirmMessage(ctx.Panel.Ticket.Settings.DeleteOnClose)
	if helpers.IsPrefixInteraction(i) {
		_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Embeds:     []*discordgo.MessageEmbed{confirm},
			Components: tickets.TicketCloseConfirmButtons(),
			Reference: &discordgo.MessageReference{
				MessageID: helpers.PrefixSourceMessageID(i),
				ChannelID: i.ChannelID,
				GuildID:   i.GuildID,
			},
		})
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, tickets.UnexpectedErrorTicketEmbed())
		}
		return nil
	}
	return helpers.EditInteractionComponents(s, i, confirm, tickets.TicketCloseConfirmButtons())
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// changeTicketMember adds or removes a user and/or a role of the ticket thread or channel. At least one of the two
// options is needed; when both are given both are changed.
func changeTicketMember(s *discordgo.Session, i *discordgo.InteractionCreate, add bool) error {
	ctx, err := tickets.LoadOrAdoptTicketContext(s, i.GuildID, i.ChannelID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketContextErrorEmbed(err))
	}
	if !tickets.IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketPermissionDeniedEmbed("Only support staff can change who is in a ticket."))
	}

	_, options := types.SelectedSubcommand(i)
	target := helpers.SubOptionUser(s, options, "user")
	roleID := helpers.SubOptionRoleID(s, options, "role")
	if target == nil && roleID == "" {
		return helpers.EditInteractionEmbed(s, i, TicketAccessResultEmbed(false, "Choose a user, a role, or both."))
	}
	if target != nil && target.Bot {
		return helpers.EditInteractionEmbed(s, i, tickets.TicketBotTargetEmbed())
	}
	if target != nil && !add && target.ID == ctx.Ticket.OwnerID {
		return helpers.EditInteractionEmbed(s, i, TicketRemoveOwnerEmbed())
	}

	verb, prep := "Added", "to"
	if !add {
		verb, prep = "Removed", "from"
	}
	var done []string
	var failed []string
	if target != nil {
		if err := changeTicketAccess(s, ctx, target.ID, discordgo.PermissionOverwriteTypeMember, add); err != nil {
			failed = append(failed, "<@"+target.ID+">")
		} else {
			done = append(done, "<@"+target.ID+">")
		}
	}
	if roleID != "" {
		n, err := changeTicketRoleAccess(s, i.GuildID, ctx, roleID, add)
		switch {
		case err != nil:
			failed = append(failed, "<@&"+roleID+">")
		case ctx.Ticket.Mode == types.TicketModeChannel:
			done = append(done, "<@&"+roleID+">")
		default:
			done = append(done, fmt.Sprintf("<@&%s> (%d members)", roleID, n))
		}
	}
	if len(done) == 0 {
		return helpers.EditInteractionEmbed(s, i, tickets.UnexpectedErrorTicketEmbed())
	}
	msg := fmt.Sprintf("%s %s %s this ticket.", verb, strings.Join(done, ", "), prep)
	if len(failed) > 0 {
		msg += "\n\nCould not change: " + strings.Join(failed, ", ")
	}
	return helpers.EditInteractionEmbed(s, i, TicketAccessResultEmbed(true, msg))
}

// changeTicketAccess gives or takes ticket access for one user (member) or role (overwrite type). Channel tickets use
// permission overwrites; thread tickets can only add or remove single users.
func changeTicketAccess(s *discordgo.Session, ctx *types.TicketContext, id string, kind discordgo.PermissionOverwriteType, add bool) error {
	if ctx.Ticket.Mode == types.TicketModeChannel {
		allow := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory | discordgo.PermissionAttachFiles | discordgo.PermissionEmbedLinks)
		if add {
			return s.ChannelPermissionSet(ctx.Ticket.ThreadID, id, kind, allow, 0)
		}
		return s.ChannelPermissionSet(ctx.Ticket.ThreadID, id, kind, 0, int64(discordgo.PermissionViewChannel|discordgo.PermissionSendMessages))
	}
	if add {
		return s.ThreadMemberAdd(ctx.Ticket.ThreadID, id)
	}
	return s.ThreadMemberRemove(ctx.Ticket.ThreadID, id)
}

// maxTicketRoleMembers bounds how many people one role change touches in a thread ticket (each is an API call).
const maxTicketRoleMembers = 200

// changeTicketRoleAccess applies a role to the ticket. In a channel ticket that is one permission overwrite; a thread
// has no role access, so every member holding the role is added or removed instead (the owner is never removed).
// It returns how many members were changed.
func changeTicketRoleAccess(s *discordgo.Session, guildID string, ctx *types.TicketContext, roleID string, add bool) (int, error) {
	if ctx.Ticket.Mode == types.TicketModeChannel {
		return 0, changeTicketAccess(s, ctx, roleID, discordgo.PermissionOverwriteTypeRole, add)
	}
	changed, after := 0, ""
	for scanned := 0; scanned < 10000; {
		members, err := s.GuildMembers(guildID, after, 1000)
		if err != nil {
			return changed, err
		}
		if len(members) == 0 {
			break
		}
		for _, m := range members {
			after = m.User.ID
			scanned++
			if m.User.Bot || !slices.Contains(m.Roles, roleID) || (!add && m.User.ID == ctx.Ticket.OwnerID) {
				continue
			}
			if changed >= maxTicketRoleMembers {
				return changed, nil
			}
			if err := changeTicketAccess(s, ctx, m.User.ID, discordgo.PermissionOverwriteTypeMember, add); err == nil {
				changed++
			}
		}
		if len(members) < 1000 {
			break
		}
	}
	return changed, nil
}

// ============================================================================
// Helper functions
// ============================================================================

func TicketManageInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "That is not a valid ticket action. Choose one of `panel`, `leaderboard`, `stats`, `logs`, `claim`, `close`, `add`, or `remove`.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

// TicketLeaderboardMessage ranks the staff who have claimed the most tickets.
func TicketLeaderboardMessage(rows []tickets.TicketLeaderboardRow, viewerID string) *discordgo.MessageEmbed {
	if len(rows) == 0 {
		return &discordgo.MessageEmbed{
			Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: helpers.ThumbGuild},
			Title:       "Ticket Leaderboard",
			Description: "No tickets have been claimed yet. Claim a ticket to get on the board.",
			Color:       helpers.ColorInfo,
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
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: helpers.ThumbGuild},
		Title:       "Ticket Leaderboard",
		Description: "The staff members who have claimed the most tickets.\n\n" + board.String(),
		Color:       helpers.ColorDefault,
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
		Thumbnail: &discordgo.MessageEmbedThumbnail{URL: helpers.ThumbGuild},
		Title:     "Ticket Stats",
		Description: fmt.Sprintf(
			"<@%s>\n\n- **Claimed:** %d\n- **Closed:** %d\n- **Currently Claimed:** %d\n- **Leaderboard Rank:** %s",
			staffID, stats.Claimed, stats.Closed, activeClaims, rankText,
		),
		Color: helpers.ColorDefault,
	}
}

func TicketClaimedEmbed(staffID string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Ticket Claimed",
		Description: fmt.Sprintf("Successfully claimed this ticket.\n\n> **Claimed By:** <@%s>", staffID),
		Thumbnail:   helpers.ThumbGuild,
	})
}

// TicketAccessResultEmbed reports the outcome of /ticket add or /ticket remove.
func TicketAccessResultEmbed(ok bool, description string) *discordgo.MessageEmbed {
	kind, title := "success", "Ticket Access Updated"
	if !ok {
		kind, title = "error", "Nothing To Change"
	}
	return helpers.Preset(kind, types.PresetOptions{Title: title, Description: description, Thumbnail: helpers.ThumbGuild})
}

func TicketRemoveOwnerEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "The ticket owner cannot be removed from their own ticket. Close the ticket instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

// EditInteractionV2 fills a slash command's deferred reply with a V2 message.
// Prefix invocations have no interaction token, so they get a normal message.
func EditInteractionV2(s *discordgo.Session, i *discordgo.InteractionCreate, components ...discordgo.MessageComponent) error {
	if helpers.IsPrefixInteraction(i) {
		_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Flags:      discordgo.MessageFlagsIsComponentsV2,
			Components: components,
			Reference: &discordgo.MessageReference{
				MessageID: helpers.PrefixSourceMessageID(i),
				ChannelID: i.ChannelID,
				GuildID:   i.GuildID,
			},
		})
		return err
	}
	body := map[string]any{
		"flags":      discordgo.MessageFlagsIsComponentsV2,
		"components": components,
	}
	_, err := s.RequestWithBucketID(
		"PATCH",
		discordgo.EndpointInteractionResponseActions(s.State.User.ID, i.Interaction.Token),
		body,
		discordgo.EndpointInteractionResponseActions(s.State.User.ID, ""),
	)
	return err
}

// IsStaffOfAnyPanel reports whether the member is staff for at least one ticket panel.
func IsStaffOfAnyPanel(i *discordgo.InteractionCreate, ip *types.InteractivePanels) bool {
	if tickets.InteractionIsAdmin(i) {
		return true
	}
	for _, panel := range tickets.SortedTicketPanels(ip) {
		if tickets.IsTicketStaff(i.Member, panel, nil) {
			return true
		}
		for _, category := range panel.Ticket.Categories {
			if tickets.IsTicketStaff(i.Member, panel, category) {
				return true
			}
		}
	}
	return false
}

// TicketLeaderboard ranks staff by claimed tickets, then closed tickets.
func TicketLeaderboard(ip *types.InteractivePanels) []tickets.TicketLeaderboardRow {
	rows := make([]tickets.TicketLeaderboardRow, 0, len(ip.Tickets.Stats))
	for staffID, stats := range ip.Tickets.Stats {
		if stats == nil || (stats.Claimed == 0 && stats.Closed == 0) {
			continue
		}
		rows = append(rows, tickets.TicketLeaderboardRow{StaffID: staffID, Stats: *stats})
	}
	sort.Slice(rows, func(a, b int) bool {
		if rows[a].Stats.Claimed != rows[b].Stats.Claimed {
			return rows[a].Stats.Claimed > rows[b].Stats.Claimed
		}
		if rows[a].Stats.Closed != rows[b].Stats.Closed {
			return rows[a].Stats.Closed > rows[b].Stats.Closed
		}
		return rows[a].StaffID < rows[b].StaffID
	})
	return rows
}

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
