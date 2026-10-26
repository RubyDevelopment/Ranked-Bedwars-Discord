package helpers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	guildmodel "github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models/guild"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
)

// Tickets live in their own database tables (guild_tickets, guild_ticket_logs, guild_ticket_staff_stats,
// guild_ticket_blacklist and guild_ticket_counters). The bot still works on one in-memory types.TicketState per guild:
// it is loaded from the Database API before a change, the change runs, and only what differs is written back.

// ticketSnapshot remembers what a guild's ticket state looked like when it was loaded, so the write can be a diff.
type ticketSnapshot struct {
	tickets   map[string]string // thread id -> JSON of the ticket
	inOpen    map[string]bool   // thread ids that were live tickets
	stats     map[string]string // staff id -> JSON of the leaderboard row
	blacklist string
	counter   int
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
	row.Logs = make([]guildmodel.GuildTicketLogsModel, 0, len(ticket.Logs))
	for _, entry := range ticket.Logs {
		log := guildmodel.GuildTicketLogsModel{Type: entry.Type, ActorID: entry.ActorID, Message: entry.Message}
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
		stats:     map[string]string{},
		blacklist: jsonString(ip.Tickets.Blacklist),
		counter:   ip.Tickets.Counter,
		panels:    jsonString(ip),
	}
	for threadID, ticket := range ip.Tickets.Open {
		snap.tickets[threadID] = jsonString(ticket)
		snap.inOpen[threadID] = true
	}
	for _, ticket := range ip.Tickets.History {
		snap.tickets[ticket.ThreadID] = jsonString(ticket)
	}
	for staffID, stats := range ip.Tickets.Stats {
		snap.stats[staffID] = jsonString(stats)
	}
	return snap
}

// panelsChanged reports whether the panel definitions differ from the snapshot.
func (snap ticketSnapshot) panelsChanged(ip *types.InteractivePanels) bool {
	return jsonString(ip) != snap.panels
}

// changesSince builds the write that turns the snapshot into the current state: tickets that are new or differ are
// upserted (live ones keyed by their map key, which is the thread id), live tickets that vanished without being
// archived are deleted, and changed leaderboard rows, blacklist and counter are included. Closed tickets that merely
// fell out of the loaded window are left alone: the database keeps every ticket.
func (snap ticketSnapshot) changesSince(ip *types.InteractivePanels) guildmodel.TicketChanges {
	var changes guildmodel.TicketChanges
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

	for staffID, stats := range ip.Tickets.Stats {
		if stats == nil {
			continue
		}
		if previous, ok := snap.stats[staffID]; ok && previous == jsonString(stats) {
			continue
		}
		changes.Stats = append(changes.Stats, guildmodel.GuildTicketStaffStatsModel{
			StaffID: staffID, Claimed: stats.Claimed, Closed: stats.Closed, LastClaimedAt: timeOf(stats.LastClaimedAt),
		})
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

	if ip.Tickets.Counter > snap.counter {
		counter := uint(ip.Tickets.Counter)
		changes.Counter = &counter
	}
	return changes
}
