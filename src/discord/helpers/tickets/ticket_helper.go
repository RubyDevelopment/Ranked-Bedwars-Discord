package tickets

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
)

// Ticket persistence, panel publishing, ticket interactions, and shared embeds.

// ============================================================================
// Constants & Configuration
// ============================================================================

const (
	// interactivePanelsField is the guild config section panels are stored in.
	interactivePanelsField = "interactive_panels"

	// MaxTicketHistory is how many of the most recent closed tickets are loaded for /ticket logs. Every ticket stays in
	// the database; this only limits what the bot reads at a time.
	MaxTicketHistory = 200

	refAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

	defaultPanelColor = 0x55FFFF

	defaultInactivityReminderHours = 12
	maxInactivityHours             = 8760
	// inactivityCloser labels tickets closed by the inactivity policy (a panel with auto-close turned on).
	inactivityCloser = "inactivity"
)

const defaultInactivityMessage = "{user} this ticket has been inactive for {inactive_hours} hours. Reply here if you still need help."

// legacyInactivityMessage is the old default that promised an automatic close; it is swapped for the new default.
const legacyInactivityMessage = "This ticket has been inactive for {inactive_hours} hours. It will be closed in {hours_until_close} hours if no new messages are sent."

// Ticket component custom IDs are namespaced `tkt:` and colon separated.
const (
	TicketIDPrefix = "tkt:"

	TicketIDOpen     = "tkt:open"      // tkt:open:<panel>:<category>
	TicketIDClaim    = "tkt:claim"     // in-ticket
	TicketIDUnclaim  = "tkt:unclaim"   // in-ticket
	TicketIDClose    = "tkt:close"     // in-ticket, asks for confirmation
	TicketIDCloseYes = "tkt:close:yes" // confirmation
	TicketIDCloseNo  = "tkt:close:no"  // confirmation
)

// ============================================================================
// Errors
// ============================================================================

var (
	ErrTicketPanelNotFound    = errors.New("ticket panel not found")
	ErrTicketCategoryNotFound = errors.New("ticket category not found")
	ErrTicketNotFound         = errors.New("not a ticket thread")
	ErrTicketAlreadyClaimed   = errors.New("ticket already claimed")
	ErrTicketNotClaimed       = errors.New("ticket not claimed")
	ErrTicketClaimDisabled    = errors.New("claiming is disabled")
	ErrTicketLockUnchanged    = errors.New("ticket is already in that state")
)

// TicketLimitError reports the tickets a member already has open.
type TicketLimitError struct {
	Limit   int
	Threads []string
}

func (e *TicketLimitError) Error() string {
	return fmt.Sprintf("ticket limit of %d reached", e.Limit)
}

// ============================================================================
// Ticket Store
// ============================================================================

// TicketStore serialises every read-modify-write of a guild's interactive
// panels so concurrent button clicks cannot overwrite one another.
type TicketStore struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// Tickets is the shared store used by the ticket commands and their handlers.
var Tickets = &TicketStore{locks: map[string]*sync.Mutex{}}

var ticketActivityIndex = struct {
	sync.RWMutex
	channels map[string]struct{}
}{channels: map[string]struct{}{}}

func ticketActivityKey(guildID, channelID string) string {
	return guildID + ":" + channelID
}

func setGuildTicketActivityChannels(guildID string, channels map[string]struct{}) {
	prefix := guildID + ":"
	ticketActivityIndex.Lock()
	defer ticketActivityIndex.Unlock()
	for key := range ticketActivityIndex.channels {
		if strings.HasPrefix(key, prefix) {
			delete(ticketActivityIndex.channels, key)
		}
	}
	for channelID := range channels {
		ticketActivityIndex.channels[ticketActivityKey(guildID, channelID)] = struct{}{}
	}
}

func ticketTracksActivity(guildID, channelID string) bool {
	ticketActivityIndex.RLock()
	_, ok := ticketActivityIndex.channels[ticketActivityKey(guildID, channelID)]
	ticketActivityIndex.RUnlock()
	return ok
}

func removeTicketActivityChannel(guildID, channelID string) {
	ticketActivityIndex.Lock()
	delete(ticketActivityIndex.channels, ticketActivityKey(guildID, channelID))
	ticketActivityIndex.Unlock()
}

func (st *TicketStore) guildLock(guildID string) *sync.Mutex {
	st.mu.Lock()
	defer st.mu.Unlock()
	lock, ok := st.locks[guildID]
	if !ok {
		lock = &sync.Mutex{}
		st.locks[guildID] = lock
	}
	return lock
}

// Load returns the guild's panels together with its ticket state: the live tickets, the most recent closed tickets
// (MaxTicketHistory), the leaderboard, the blacklist and the counter. A guild without any resolves to an empty,
// ready-to-use value.
func (st *TicketStore) Load(guildID string) (*types.InteractivePanels, error) {
	return loadInteractivePanels(guildID, MaxTicketHistory)
}

// LoadOpen is Load without the closed-ticket history, for hot paths that only look at live tickets.
func (st *TicketStore) LoadOpen(guildID string) (*types.InteractivePanels, error) {
	return loadInteractivePanels(guildID, 0)
}

// Update loads the guild's panels and tickets, lets fn change them, and saves what changed: ticket changes go to the
// ticket tables in one atomic write, panel changes to the guild config. fn returning an error aborts the save.
func (st *TicketStore) Update(actorID, guildID string, fn func(ip *types.InteractivePanels) error) error {
	return st.update(actorID, guildID, MaxTicketHistory, fn)
}

// UpdateOpen is Update without the closed-ticket history. fn must not look at or change ip.Tickets.History.
func (st *TicketStore) UpdateOpen(actorID, guildID string, fn func(ip *types.InteractivePanels) error) error {
	return st.update(actorID, guildID, 0, fn)
}

func (st *TicketStore) update(actorID, guildID string, history int, fn func(ip *types.InteractivePanels) error) error {
	lock := st.guildLock(guildID)
	lock.Lock()
	defer lock.Unlock()

	ip, err := loadInteractivePanels(guildID, history)
	if err != nil {
		return err
	}
	snapshot := snapshotInteractivePanels(ip)
	if err := fn(ip); err != nil {
		return err
	}
	db := database.Must()
	if err := db.Tickets.Sync(actorID, guildID, snapshot.changesSince(ip)); err != nil {
		return err
	}
	if snapshot.panelsChanged(ip) {
		return db.GuildConfig.SaveField(actorID, guildID, interactivePanelsField, ip)
	}
	return nil
}

// loadInteractivePanels reads the panel definitions from the guild config and the ticket state from the ticket tables.
func loadInteractivePanels(guildID string, history int) (*types.InteractivePanels, error) {
	ip := &types.InteractivePanels{}
	raw, err := database.Must().GuildConfig.Field(guildID, interactivePanelsField)
	if err != nil && !sdk.IsNotFound(err) {
		return nil, err
	}
	if err == nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, ip); err != nil {
			return nil, err
		}
	}
	state, err := loadTicketState(guildID, history)
	if err != nil {
		return nil, err
	}
	ip.Tickets = state
	normalizeInteractivePanels(ip)
	return ip, nil
}

// normalizeInteractivePanels fills the maps a fresh `{}` section leaves nil.
func normalizeInteractivePanels(ip *types.InteractivePanels) {
	if ip.Panels == nil {
		ip.Panels = map[string]*types.InteractivePanel{}
	}
	if ip.Tickets.Open == nil {
		ip.Tickets.Open = map[string]*types.Ticket{}
	}
	if ip.Tickets.Stats == nil {
		ip.Tickets.Stats = map[string]*types.TicketStaffStats{}
	}
	if ip.Tickets.Blacklist == nil {
		ip.Tickets.Blacklist = []*types.TicketBlacklistEntry{}
	}
	for _, panel := range ip.Panels {
		if panel.Type == types.PanelTypeTicket && panel.Ticket == nil {
			panel.Ticket = &types.TicketPanelConfig{}
		}
		if panel.Type == types.PanelTypeTicket && panel.Ticket != nil {
			settings := &panel.Ticket.Settings
			if settings.InactivityReminderHours == 0 {
				settings.InactivityEnabled = true
				settings.InactivityReminderHours = defaultInactivityReminderHours
			}
			if settings.InactivityMessage == "" || settings.InactivityMessage == legacyInactivityMessage {
				settings.InactivityMessage = defaultInactivityMessage
			}
		}
	}
}

// ============================================================================
// Panel Helpers
