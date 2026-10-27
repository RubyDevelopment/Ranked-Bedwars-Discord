package staff

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
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
	Ephemeral:    true,
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
	Execute: executeTicketManageCommand,
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
	case "lock":
		return executeTicketLock(s, i, true)
	case "unlock":
		return executeTicketLock(s, i, false)
	case "add":
		return changeTicketMember(s, i, true)
	case "remove":
		return changeTicketMember(s, i, false)
	}
	return helpers.EditInteractionEmbed(s, i, helpers.TicketManageInvalidSubcommandEmbed(i))
}

// executeTicketPanel opens the panel publishing flow for administrators.
func executeTicketPanel(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !helpers.InteractionIsAdmin(i) {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketPermissionDeniedEmbed("Only administrators can publish ticket panels. Create and edit panels in the dashboard."))
	}

	ip, err := helpers.Tickets.LoadOpen(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
	}
	return helpers.EditInteractionV2(s, i, helpers.TicketPublishPanelScreen(helpers.SortedTicketPanels(ip))...)
}

// executeTicketLeaderboard ranks staff by claimed tickets.
func executeTicketLeaderboard(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ip, err := helpers.Tickets.LoadOpen(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
	}
	return helpers.EditInteractionEmbed(s, i, helpers.TicketLeaderboardMessage(helpers.TicketLeaderboard(ip), helpers.InteractionUserID(i)))
}

// executeTicketStats shows one staff member's numbers.
func executeTicketStats(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	_, options := types.SelectedSubcommand(i)
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		target = i.Member.User
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketBotTargetEmbed())
	}

	ip, err := helpers.Tickets.LoadOpen(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
	}

	stats := types.TicketStaffStats{}
	if row, ok := ip.Tickets.Stats[target.ID]; ok && row != nil {
		stats = *row
	}
	board := helpers.TicketLeaderboard(ip)
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
	return helpers.EditInteractionEmbed(s, i, helpers.TicketStatsMessage(target.ID, stats, rank, len(board), activeClaims))
}

// executeTicketLogs lists recently closed tickets for staff.
func executeTicketLogs(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ip, err := helpers.Tickets.Load(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
	}
	if !helpers.IsStaffOfAnyPanel(i, ip) {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketPermissionDeniedEmbed("Only support staff can browse ticket logs."))
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
	return helpers.EditInteractionEmbed(s, i, helpers.TicketLogsMessage(entries, filter))
}

// executeTicketClaim claims the ticket the command is run in.
func executeTicketClaim(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ctx, err := helpers.LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketContextErrorEmbed(err))
	}
	if !helpers.IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketPermissionDeniedEmbed("Only support staff can claim tickets."))
	}

	if _, err := helpers.ClaimTicket(s, i.GuildID, i.ChannelID, helpers.InteractionUserID(i)); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketActionErrorEmbed(err))
	}
	return helpers.EditInteractionEmbed(s, i, helpers.TicketClaimedEmbed(helpers.InteractionUserID(i)))
}

// executeTicketRename renames the ticket the command is run in.
func executeTicketRename(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ctx, err := helpers.LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketContextErrorEmbed(err))
	}
	if !helpers.IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketPermissionDeniedEmbed("Only support staff can rename tickets."))
	}
	_, options := types.SelectedSubcommand(i)
	name := helpers.SubOptionString(options, "name")
	if name == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketAccessResultEmbed(false, "Give the ticket a name."))
	}
	applied, _, err := helpers.RenameTicket(s, i.GuildID, i.ChannelID, helpers.InteractionUserID(i), name)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketActionErrorEmbed(err))
	}
	return helpers.EditInteractionEmbed(s, i, helpers.TicketNoticeEmbed("Ticket Renamed", "This ticket is now called **"+applied+"**."))
}

// executeTicketLock locks or unlocks the ticket the command is run in.
func executeTicketLock(s *discordgo.Session, i *discordgo.InteractionCreate, locked bool) error {
	ctx, err := helpers.LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketContextErrorEmbed(err))
	}
	if !helpers.IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketPermissionDeniedEmbed("Only support staff can lock or unlock tickets."))
	}
	if _, err := helpers.SetTicketLocked(s, i.GuildID, i.ChannelID, helpers.InteractionUserID(i), locked); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketActionErrorEmbed(err))
	}
	if locked {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketNoticeEmbed("Ticket Locked", "The owner can no longer write here until it is unlocked."))
	}
	return helpers.EditInteractionEmbed(s, i, helpers.TicketNoticeEmbed("Ticket Unlocked", "The owner can write in this ticket again."))
}

// executeTicketClose asks for confirmation before closing the ticket the command is run in.
func executeTicketClose(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ctx, err := helpers.LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketContextErrorEmbed(err))
	}
	if !helpers.CanCloseTicket(i, ctx) {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketPermissionDeniedEmbed("Only support staff can close this ticket."))
	}

	_, options := types.SelectedSubcommand(i)
	if reason := strings.TrimSpace(helpers.SubOptionString(options, "reason")); reason != "" {
		if err := helpers.SetPendingCloseReason(helpers.InteractionUserID(i), i.GuildID, i.ChannelID, reason); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
		}
	}
	confirm := helpers.TicketCloseConfirmMessage(ctx.Panel.Ticket.Settings.DeleteOnClose)
	if helpers.IsPrefixInteraction(i) {
		_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Embeds:     []*discordgo.MessageEmbed{confirm},
			Components: helpers.TicketCloseConfirmButtons(),
			Reference: &discordgo.MessageReference{
				MessageID: helpers.PrefixSourceMessageID(i),
				ChannelID: i.ChannelID,
				GuildID:   i.GuildID,
			},
		})
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
		}
		return nil
	}
	return helpers.EditInteractionComponents(s, i, confirm, helpers.TicketCloseConfirmButtons())
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// changeTicketMember adds or removes a user and/or a role of the ticket thread or channel. At least one of the two
// options is needed; when both are given both are changed.
func changeTicketMember(s *discordgo.Session, i *discordgo.InteractionCreate, add bool) error {
	ctx, err := helpers.LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketContextErrorEmbed(err))
	}
	if !helpers.IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketPermissionDeniedEmbed("Only support staff can change who is in a ticket."))
	}

	_, options := types.SelectedSubcommand(i)
	target := helpers.SubOptionUser(s, options, "user")
	roleID := helpers.SubOptionRoleID(s, options, "role")
	if target == nil && roleID == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketAccessResultEmbed(false, "Choose a user, a role, or both."))
	}
	if target != nil && target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketBotTargetEmbed())
	}
	if target != nil && !add && target.ID == ctx.Ticket.OwnerID {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketRemoveOwnerEmbed())
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
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
	}
	msg := fmt.Sprintf("%s %s %s this ticket.", verb, strings.Join(done, ", "), prep)
	if len(failed) > 0 {
		msg += "\n\nCould not change: " + strings.Join(failed, ", ")
	}
	return helpers.EditInteractionEmbed(s, i, helpers.TicketAccessResultEmbed(true, msg))
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
