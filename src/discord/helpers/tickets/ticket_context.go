package tickets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	guildmodel "github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models/guild"
	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// ============================================================================
// Ticket Context
// ============================================================================

// TicketContext is defined in package types (tickets.go).
type TicketContext = types.TicketContext

var errNotTicketChannel = errors.New("not a ticket channel")

// LoadTicketContext resolves the ticket that lives in the given thread.
func LoadTicketContext(guildID, threadID string) (*TicketContext, error) {
	ip, err := Tickets.Load(guildID)
	if err != nil {
		return nil, err
	}
	ticket := ip.Tickets.Open[threadID]
	if ticket == nil {
		return nil, errNotTicketChannel
	}
	panel := ip.Panels[ticket.PanelID]
	if panel == nil || panel.Ticket == nil {
		return nil, ErrTicketPanelNotFound
	}
	category := FindTicketCategory(panel, ticket.CategoryID)
	if category == nil {
		category = &types.TicketCategory{ID: ticket.CategoryID, Label: ticket.CategoryLabel}
	}
	return &TicketContext{Ticket: ticket, Panel: panel, Category: category}, nil
}

// CanCloseTicket lets staff close any ticket, and owners close their own when the panel allows it.
func CanCloseTicket(i *discordgo.InteractionCreate, ctx *TicketContext) bool {
	if IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		return true
	}
	return ctx.Ticket.OwnerID == helpers.InteractionUserID(i) && ctx.Panel.Ticket.Settings.OwnerCanClose
}

func HandleTickets(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil || i.GuildID == "" {
		return
	}

	switch i.Type {
	case discordgo.InteractionMessageComponent:
		HandleTicketComponent(s, i, i.MessageComponentData().CustomID)
	}
}

// ── merged from ticket_rename_lock.go ──

// lockedSendPermissions are what a locked channel ticket takes from the people in it; they can still read it.
const lockedSendPermissions = int64(discordgo.PermissionSendMessages | discordgo.PermissionAddReactions | discordgo.PermissionAttachFiles | discordgo.PermissionEmbedLinks)

// RenameTicket renames the ticket's thread or channel. A channel name follows Discord's channel rules (lower case,
// dashes); a thread keeps the name as typed. It returns the name that was applied.
func RenameTicket(s *discordgo.Session, guildID, threadID, staffID, name string) (string, *types.Ticket, error) {
	name = strings.TrimSpace(name)
	var ticket *types.Ticket
	var panel *types.InteractivePanel
	err := Tickets.Update(staffID, guildID, func(ip *types.InteractivePanels) error {
		found := ip.Tickets.Open[threadID]
		if found == nil {
			return ErrTicketNotFound
		}
		if found.Mode == types.TicketModeChannel {
			name = channelTicketName(name, found.Ref)
		}
		name = truncate(name, 100)
		panel = ip.Panels[found.PanelID]
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	if _, err := s.ChannelEdit(threadID, &discordgo.ChannelEdit{Name: name}); err != nil {
		return "", nil, err
	}
	err = Tickets.Update(staffID, guildID, func(ip *types.InteractivePanels) error {
		found := ip.Tickets.Open[threadID]
		if found == nil {
			return ErrTicketNotFound
		}
		found.ThreadName = name
		appendTicketLog(found, "renamed", staffID, "Ticket renamed to "+name+".", time.Now().Unix())
		copied := *found
		ticket = &copied
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	PostTicketLog(s, panel, TicketLogMessage(helpers.ColorDefault, "Ticket Renamed", "<@"+staffID+"> renamed a ticket.", []string{
		"- **Ticket:** <#" + ticket.ThreadID + ">",
		"- **Reference:** `#" + ticket.Ref + "`",
		"- **New name:** " + name,
	}), nil)
	return name, ticket, nil
}

// SetTicketLocked locks or unlocks a ticket. A thread uses Discord's own lock (only members who manage threads can
// write in it); a channel takes sending away from its owner and any member added by hand, and gives it back on unlock.
func SetTicketLocked(s *discordgo.Session, guildID, threadID, staffID string, locked bool) (*types.Ticket, error) {
	var ticket *types.Ticket
	var panel *types.InteractivePanel
	err := Tickets.Update(staffID, guildID, func(ip *types.InteractivePanels) error {
		found := ip.Tickets.Open[threadID]
		if found == nil {
			return ErrTicketNotFound
		}
		if found.Locked == locked {
			return ErrTicketLockUnchanged
		}
		panel = ip.Panels[found.PanelID]
		copied := *found
		ticket = &copied
		return nil
	})
	if err != nil {
		return nil, err
	}

	if ticket.Mode == types.TicketModeChannel {
		channel, err := s.Channel(threadID)
		if err != nil {
			return nil, err
		}
		for _, overwrite := range channel.PermissionOverwrites {
			if overwrite.Type != discordgo.PermissionOverwriteTypeMember {
				continue
			}
			allow, deny := overwrite.Allow, overwrite.Deny
			if locked {
				if allow&lockedSendPermissions == 0 {
					continue
				}
				allow &^= lockedSendPermissions
				deny |= lockedSendPermissions
			} else {
				if deny&lockedSendPermissions == 0 {
					continue
				}
				allow |= lockedSendPermissions
				deny &^= lockedSendPermissions
			}
			if err := s.ChannelPermissionSet(threadID, overwrite.ID, discordgo.PermissionOverwriteTypeMember, allow, deny); err != nil {
				return nil, err
			}
		}
	} else if _, err := s.ChannelEditComplex(threadID, &discordgo.ChannelEdit{Locked: &locked}); err != nil {
		return nil, err
	}

	err = Tickets.Update(staffID, guildID, func(ip *types.InteractivePanels) error {
		found := ip.Tickets.Open[threadID]
		if found == nil {
			return ErrTicketNotFound
		}
		found.Locked = locked
		message := "Ticket locked."
		if !locked {
			message = "Ticket unlocked."
		}
		appendTicketLog(found, map[bool]string{true: "locked", false: "unlocked"}[locked], staffID, message, time.Now().Unix())
		copied := *found
		ticket = &copied
		return nil
	})
	if err != nil {
		return nil, err
	}

	RefreshTicketWelcome(s, guildID, ticket, panel)
	title, color, verb := "Ticket Locked", helpers.ColorCaution, "locked"
	if !locked {
		title, color, verb = "Ticket Unlocked", helpers.ColorDefault, "unlocked"
	}
	PostTicketLog(s, panel, TicketLogMessage(color, title, "<@"+staffID+"> "+verb+" a ticket.", []string{
		"- **Ticket:** <#" + ticket.ThreadID + ">",
		"- **Reference:** `#" + ticket.Ref + "`",
		"- **Owner:** <@" + ticket.OwnerID + ">",
	}), nil)
	return ticket, nil
}

// Tickets live in two database tables: guild_tickets (each ticket with its activity log and its number, so the highest
// number is the counter) and guild_ticket_blacklist. The leaderboard is counted from the tickets. The bot still works on one in-memory types.TicketState per guild:
// it is loaded from the Database API before a change, the change runs, and only what differs is written back.

// ticketSnapshot remembers what a guild's ticket state looked like when it was loaded, so the write can be a diff.
type ticketSnapshot struct {
	tickets   map[string]string // thread id -> JSON of the ticket
	inOpen    map[string]bool   // thread ids that were live tickets
	blacklist string
	panels    string
}

func jsonString(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return ""
	}
	return buf.String()
}

func unixOf(t *time.Time) int64 {
	if t == nil || t.IsZero() || t.Unix() <= 0 {
		return 0
	}
	return t.Unix()
}

func timeOf(unix int64) *time.Time {
	if unix <= 0 {
		return nil
	}
	t := time.Unix(unix, 0).UTC()
	return &t
}

func ticketFromRow(row guildmodel.GuildTicketsModel) *types.Ticket {
	ticket := &types.Ticket{
		Ref:                    row.Ref,
		Number:                 int(row.Number),
		ThreadID:               row.ThreadID,
		ThreadName:             row.ThreadName,
		PanelID:                row.PanelID,
		CategoryID:             row.CategoryID,
		CategoryLabel:          row.CategoryLabel,
		OwnerID:                row.OwnerID,
		OwnerName:              row.OwnerName,
		Status:                 row.Status,
		ClaimedBy:              row.ClaimedBy,
		ClaimedAt:              unixOf(row.ClaimedAt),
		CreatedAt:              row.CreatedAt.Unix(),
		ClosedAt:               unixOf(row.ClosedAt),
		ClosedBy:               row.ClosedBy,
		CloseReason:            row.CloseReason,
		WelcomeMessageID:       row.WelcomeMessageID,
		Mode:                   row.Mode,
		Locked:                 row.Locked,
		LastActivityAt:         unixOf(row.LastActivityAt),
		LastInactivityNoticeAt: unixOf(row.LastInactivityNoticeAt),
		ArchiveURL:             row.ArchiveURL,
	}
	for _, entry := range row.Logs {
		ticket.Logs = append(ticket.Logs, types.TicketLogEntry{
			Type: entry.Type, ActorID: entry.ActorID, Message: entry.Message, CreatedAt: entry.CreatedAt.Unix(),
		})
	}
	return ticket
}

func ticketToRow(ticket *types.Ticket) guildmodel.GuildTicketsModel {
	status := ticket.Status
	if status == "" {
		status = types.TicketStatusOpen
	}
	mode := ticket.Mode
	if mode == "" {
		mode = types.TicketModeThread
	}
	row := guildmodel.GuildTicketsModel{
		Number:                 uint(max(ticket.Number, 0)),
		Ref:                    ticket.Ref,
		ThreadID:               ticket.ThreadID,
		ThreadName:             ticket.ThreadName,
		PanelID:                ticket.PanelID,
		CategoryID:             ticket.CategoryID,
		CategoryLabel:          ticket.CategoryLabel,
		OwnerID:                ticket.OwnerID,
		OwnerName:              ticket.OwnerName,
		Status:                 status,
		Mode:                   mode,
		ClaimedBy:              ticket.ClaimedBy,
		ClaimedAt:              timeOf(ticket.ClaimedAt),
		ClosedBy:               ticket.ClosedBy,
		ClosedAt:               timeOf(ticket.ClosedAt),
		CloseReason:            ticket.CloseReason,
		WelcomeMessageID:       ticket.WelcomeMessageID,
		Locked:                 ticket.Locked,
		LastActivityAt:         timeOf(ticket.LastActivityAt),
		LastInactivityNoticeAt: timeOf(ticket.LastInactivityNoticeAt),
		ArchiveURL:             ticket.ArchiveURL,
	}
	if created := timeOf(ticket.CreatedAt); created != nil {
		row.CreatedAt = *created
	}
	row.Logs = make(guildmodel.TicketLogs, 0, len(ticket.Logs))
	for _, entry := range ticket.Logs {
		log := guildmodel.TicketLog{Type: entry.Type, ActorID: entry.ActorID, Message: entry.Message}
		if at := timeOf(entry.CreatedAt); at != nil {
			log.CreatedAt = *at
		}
		row.Logs = append(row.Logs, log)
	}
	return row
}

// loadTicketState reads a guild's tickets from the Database API: every live ticket, its `history` most recent closed
// tickets (oldest first), the leaderboard, the blacklist and the counter.
func loadTicketState(guildID string, history int) (types.TicketState, error) {
	state := types.TicketState{
		Open:      map[string]*types.Ticket{},
		Stats:     map[string]*types.TicketStaffStats{},
		History:   []*types.Ticket{},
		Blacklist: []*types.TicketBlacklistEntry{},
	}
	wire, err := database.Must().Tickets.State(guildID, history)
	if err != nil {
		return state, fmt.Errorf("load tickets: %w", err)
	}
	state.Counter = int(wire.Counter)
	for _, row := range wire.Open {
		state.Open[row.ThreadID] = ticketFromRow(row)
	}
	for _, row := range wire.History {
		state.History = append(state.History, ticketFromRow(row))
	}
	for _, row := range wire.Stats {
		state.Stats[row.StaffID] = &types.TicketStaffStats{
			Claimed: row.Claimed, Closed: row.Closed, LastClaimedAt: unixOf(row.LastClaimedAt),
		}
	}
	for _, row := range wire.Blacklist {
		state.Blacklist = append(state.Blacklist, &types.TicketBlacklistEntry{
			UserID: row.UserID, Reason: row.Reason, AddedBy: row.AddedBy,
			CreatedAt: row.CreatedAt.Unix(), ExpiresAt: unixOf(row.ExpiresAt),
		})
	}
	return state, nil
}

// snapshotInteractivePanels records the loaded state so changesSince can tell what the caller changed.
func snapshotInteractivePanels(ip *types.InteractivePanels) ticketSnapshot {
	snap := ticketSnapshot{
		tickets:   map[string]string{},
		inOpen:    map[string]bool{},
		blacklist: jsonString(ip.Tickets.Blacklist),
		panels:    jsonString(ip),
	}
	for threadID, ticket := range ip.Tickets.Open {
		snap.tickets[threadID] = jsonString(ticket)
		snap.inOpen[threadID] = true
	}
	for _, ticket := range ip.Tickets.History {
		snap.tickets[ticket.ThreadID] = jsonString(ticket)
	}
	return snap
}

// panelsChanged reports whether the panel definitions differ from the snapshot.
func (snap ticketSnapshot) panelsChanged(ip *types.InteractivePanels) bool {
	return jsonString(ip) != snap.panels
}

// changesSince builds the write that turns the snapshot into the current state: tickets that are new or differ are
// upserted (live ones keyed by their map key, which is the thread id), live tickets that vanished without being
// archived are deleted, and a changed blacklist is included. The leaderboard is counted by the database. Closed tickets that merely
// fell out of the loaded window are left alone: the database keeps every ticket.
func (snap ticketSnapshot) changesSince(ip *types.InteractivePanels) models.TicketChanges {
	var changes models.TicketChanges
	present := map[string]bool{}

	upsert := func(threadID string, ticket *types.Ticket) {
		if ticket == nil {
			return
		}
		present[threadID] = true
		ticket.ThreadID = threadID
		if previous, ok := snap.tickets[threadID]; ok && previous == jsonString(ticket) {
			return
		}
		changes.Upsert = append(changes.Upsert, ticketToRow(ticket))
	}
	for threadID, ticket := range ip.Tickets.Open {
		upsert(threadID, ticket)
	}
	for _, ticket := range ip.Tickets.History {
		if ticket != nil {
			upsert(ticket.ThreadID, ticket)
		}
	}
	for threadID := range snap.inOpen {
		if !present[threadID] {
			changes.Delete = append(changes.Delete, threadID)
		}
	}

	if jsonString(ip.Tickets.Blacklist) != snap.blacklist {
		entries := make([]guildmodel.GuildTicketBlacklistModel, 0, len(ip.Tickets.Blacklist))
		for _, entry := range ip.Tickets.Blacklist {
			if entry == nil || strings.TrimSpace(entry.UserID) == "" {
				continue
			}
			row := guildmodel.GuildTicketBlacklistModel{
				UserID: entry.UserID, Reason: entry.Reason, AddedBy: entry.AddedBy, ExpiresAt: timeOf(entry.ExpiresAt),
			}
			if created := timeOf(entry.CreatedAt); created != nil {
				row.CreatedAt = *created
			}
			entries = append(entries, row)
		}
		changes.Blacklist = &entries
	}

	return changes
}
