package staff

import (
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

// TicketManageCommand is the support ticket system: a panel builder for administrators
// and claim, close, leaderboard and log tools for staff.
var TicketManageCommand = &types.Command{
	Name:         "ticket",
	Description:  "Manages the support ticket system: panels, claims, members, and logs.",
	Requirements: []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}},
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "panel", Description: "Create, customise, and send ticket panels", Type: discordgo.ApplicationCommandOptionSubCommand},
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
				Name: "add", Description: "Add a member to the ticket you are in", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "Discord user to add", Type: discordgo.ApplicationCommandOptionUser, Required: true},
				},
			},
			{
				Name: "remove", Description: "Remove a member from the ticket you are in", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "Discord user to remove", Type: discordgo.ApplicationCommandOptionUser, Required: true},
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
	case "add":
		return changeTicketMember(s, i, true)
	case "remove":
		return changeTicketMember(s, i, false)
	}
	return helpers.EditInteractionEmbed(s, i, ticketManageInvalidSubcommandEmbed())
}

// executeTicketPanel opens the panel manager for administrators.
func executeTicketPanel(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !helpers.InteractionIsAdmin(i) {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketPermissionDeniedEmbed("Only administrators can manage ticket panels."))
	}

	ip, err := helpers.Tickets.Load(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
	}
	return helpers.EditInteractionV2(s, i, helpers.TicketManagerScreen(helpers.SortedTicketPanels(ip))...)
}

// executeTicketLeaderboard ranks staff by claimed tickets.
func executeTicketLeaderboard(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ip, err := helpers.Tickets.Load(i.GuildID)
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

	ip, err := helpers.Tickets.Load(i.GuildID)
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

// changeTicketMember adds or removes a member of the ticket thread or channel.
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
	if target == nil || target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketBotTargetEmbed())
	}

	if ctx.Ticket.Mode == types.TicketModeChannel {
		// Channel-based tickets: use permission overwrites
		if add {
			allow := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory | discordgo.PermissionAttachFiles | discordgo.PermissionEmbedLinks)
			if err := s.ChannelPermissionSet(i.ChannelID, target.ID, discordgo.PermissionOverwriteTypeMember, allow, 0); err != nil {
				return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
			}
			return helpers.EditInteractionEmbed(s, i, helpers.TicketMemberAddedEmbed(target.ID))
		}
		if target.ID == ctx.Ticket.OwnerID {
			return helpers.EditInteractionEmbed(s, i, helpers.TicketRemoveOwnerEmbed())
		}
		deny := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages)
		if err := s.ChannelPermissionSet(i.ChannelID, target.ID, discordgo.PermissionOverwriteTypeMember, 0, deny); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, helpers.TicketMemberRemovedEmbed(target.ID))
	}

	// Thread-based tickets: use thread member management
	if add {
		if err := s.ThreadMemberAdd(i.ChannelID, target.ID); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, helpers.TicketMemberAddedEmbed(target.ID))
	}
	if target.ID == ctx.Ticket.OwnerID {
		return helpers.EditInteractionEmbed(s, i, helpers.TicketRemoveOwnerEmbed())
	}
	if err := s.ThreadMemberRemove(i.ChannelID, target.ID); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorTicketEmbed())
	}
	return helpers.EditInteractionEmbed(s, i, helpers.TicketMemberRemovedEmbed(target.ID))
}

// ============================================================================
// Embed Builders
// ============================================================================

func ticketManageInvalidSubcommandEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "That is not a valid ticket action. Choose one of `panel`, `leaderboard`, `stats`, `logs`, `claim`, `close`, `add`, or `remove`.",
	})
}
