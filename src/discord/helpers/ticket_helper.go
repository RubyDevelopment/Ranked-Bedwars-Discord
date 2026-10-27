package helpers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	guildmodel "github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models/guild"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

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

	idAlphabet  = "abcdefghijklmnopqrstuvwxyz0123456789"
	refAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

	defaultPanelColor = 0x55FFFF

	defaultInactivityReminderHours = 12
	maxInactivityHours             = 8760
	// inactivityCloser only labels tickets that older versions closed automatically; nothing closes tickets for
	// inactivity any more.
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
	ErrTicketActivityChanged  = errors.New("ticket activity changed before it could be closed")
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
// ============================================================================

// SortedTicketPanels returns the guild's ticket panels, oldest first.
func SortedTicketPanels(ip *types.InteractivePanels) []*types.InteractivePanel {
	panels := make([]*types.InteractivePanel, 0, len(ip.Panels))
	for _, panel := range ip.Panels {
		if panel != nil && panel.Type == types.PanelTypeTicket {
			panels = append(panels, panel)
		}
	}
	sort.Slice(panels, func(a, b int) bool {
		if panels[a].CreatedAt != panels[b].CreatedAt {
			return panels[a].CreatedAt < panels[b].CreatedAt
		}
		return panels[a].ID < panels[b].ID
	})
	return panels
}

// FindTicketCategory returns the category with the given ID, or nil.
func FindTicketCategory(panel *types.InteractivePanel, categoryID string) *types.TicketCategory {
	if panel == nil || panel.Ticket == nil {
		return nil
	}
	for _, category := range panel.Ticket.Categories {
		if category.ID == categoryID {
			return category
		}
	}
	return nil
}

func OpenTicketsFor(ip *types.InteractivePanels, userID string) []*types.Ticket {
	var owned []*types.Ticket
	for _, ticket := range ip.Tickets.Open {
		if ticket.OwnerID == userID {
			owned = append(owned, ticket)
		}
	}
	return owned
}

// TicketBlacklistEntryFor returns an active blacklist record for userID.
func TicketBlacklistEntryFor(ip *types.InteractivePanels, userID string) *types.TicketBlacklistEntry {
	now := time.Now().Unix()
	for _, entry := range ip.Tickets.Blacklist {
		if entry != nil && entry.UserID == userID && (entry.ExpiresAt == 0 || entry.ExpiresAt > now) {
			return entry
		}
	}
	return nil
}

// StaffStatsFor returns (creating if needed) a staff member's leaderboard row.
func StaffStatsFor(ip *types.InteractivePanels, staffID string) *types.TicketStaffStats {
	stats, ok := ip.Tickets.Stats[staffID]
	if !ok {
		stats = &types.TicketStaffStats{}
		ip.Tickets.Stats[staffID] = stats
	}
	return stats
}

// TicketLeaderboardRow is defined in package types (tickets.go).
type TicketLeaderboardRow = types.TicketLeaderboardRow

// TicketLeaderboard ranks staff by claimed tickets, then closed tickets.
func TicketLeaderboard(ip *types.InteractivePanels) []TicketLeaderboardRow {
	rows := make([]TicketLeaderboardRow, 0, len(ip.Tickets.Stats))
	for staffID, stats := range ip.Tickets.Stats {
		if stats == nil || (stats.Claimed == 0 && stats.Closed == 0) {
			continue
		}
		rows = append(rows, TicketLeaderboardRow{StaffID: staffID, Stats: *stats})
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

// ArchiveTicket moves a closed ticket out of Open and into the history. Nothing is trimmed: the ticket tables keep
// every closed ticket, and only the most recent MaxTicketHistory are loaded.
func ArchiveTicket(ip *types.InteractivePanels, ticket *types.Ticket) {
	delete(ip.Tickets.Open, ticket.ThreadID)
	ip.Tickets.History = append(ip.Tickets.History, ticket)
}

// NextTicketIdentity reserves the next ticket number and a unique reference.
func NextTicketIdentity(ip *types.InteractivePanels) (number int, ref string) {
	ip.Tickets.Counter++
	number = ip.Tickets.Counter
	for {
		ref = randomID(refAlphabet, 6)
		if !ticketRefTaken(ip, ref) {
			return number, ref
		}
	}
}

func ticketRefTaken(ip *types.InteractivePanels, ref string) bool {
	for _, ticket := range ip.Tickets.Open {
		if ticket.Ref == ref {
			return true
		}
	}
	for _, ticket := range ip.Tickets.History {
		if ticket.Ref == ref {
			return true
		}
	}
	return false
}

// randomID returns n random characters drawn from alphabet.
func randomID(alphabet string, n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%0*d", n, time.Now().UnixNano()%1_000_000)[:n]
	}
	var out strings.Builder
	for _, b := range buf {
		out.WriteByte(alphabet[int(b)%len(alphabet)])
	}
	return out.String()
}

// ============================================================================
// Ticket Lifecycle: Open
// ============================================================================

// OpenTicket creates a private thread or text channel for the member, records the ticket,
// posts the welcome message and logs the new ticket.
func OpenTicket(s *discordgo.Session, guildID, panelID, categoryID string, owner *discordgo.User, fallbackChannelID string) (*types.Ticket, error) {
	ip, err := Tickets.Load(guildID)
	if err != nil {
		return nil, err
	}
	panel := ip.Panels[panelID]
	if panel == nil || panel.Ticket == nil {
		return nil, ErrTicketPanelNotFound
	}
	category := FindTicketCategory(panel, categoryID)
	if category == nil {
		return nil, ErrTicketCategoryNotFound
	}
	if entry := TicketBlacklistEntryFor(ip, owner.ID); entry != nil {
		if entry.Reason != "" {
			return nil, fmt.Errorf("you cannot open tickets: %s", entry.Reason)
		}
		return nil, errors.New("you are not allowed to open tickets")
	}

	if limit := panel.Ticket.Settings.MaxOpenPerUser; limit > 0 {
		var threads []string
		for _, ticket := range OpenTicketsFor(ip, owner.ID) {
			if ticket.PanelID == panel.ID {
				threads = append(threads, ticket.ThreadID)
			}
		}
		if len(threads) >= limit {
			return nil, &TicketLimitError{Limit: limit, Threads: threads}
		}
	}

	// Determine ticket mode (thread or channel)
	mode := panel.Ticket.Settings.Mode
	if mode == "" {
		mode = types.TicketModeThread // Default to thread mode
	}

	createdAt := time.Now().Unix()
	ticket := &types.Ticket{
		PanelID:        panel.ID,
		CategoryID:     category.ID,
		CategoryLabel:  category.Label,
		OwnerID:        owner.ID,
		OwnerName:      owner.Username,
		Status:         types.TicketStatusOpen,
		CreatedAt:      createdAt,
		LastActivityAt: createdAt,
		Mode:           mode,
		Logs: []types.TicketLogEntry{{
			Type: "opened", ActorID: owner.ID, Message: "Ticket opened.", CreatedAt: createdAt,
		}},
	}

	// Reserve the number and reference first so the thread name can use them.
	if err := Tickets.Update(owner.ID, guildID, func(ip *types.InteractivePanels) error {
		ticket.Number, ticket.Ref = NextTicketIdentity(ip)
		return nil
	}); err != nil {
		return nil, err
	}

	guildName := GuildDisplayName(s, guildID)
	nameFormat := category.ThreadNameFormat
	if mode == types.TicketModeChannel {
		nameFormat = category.ChannelNameFormat
	}
	if nameFormat == "" {
		nameFormat = category.NameFormat
	}
	if nameFormat == "" {
		nameFormat = "{username} - Ticket: {category}"
	}
	ticket.ThreadName = strings.TrimSpace(TicketPlaceholders(nameFormat, ticket, panel, category, guildName))
	if ticket.ThreadName == "" {
		ticket.ThreadName = fmt.Sprintf("%s - Ticket: %s", owner.Username, category.Label)
	}
	if mode == types.TicketModeChannel {
		ticket.ThreadName = channelTicketName(ticket.ThreadName, ticket.Ref)
	}
	ticket.ThreadName = truncate(ticket.ThreadName, 100)

	var channelID string
	if mode == types.TicketModeChannel {
		// Create a text channel in the specified category
		parentID := category.CategoryID
		if parentID == "" {
			return nil, fmt.Errorf("category_id must be set for channel-based tickets")
		}

		// Build permission overwrites: deny @everyone, allow owner
		overwrites := []*discordgo.PermissionOverwrite{
			{
				Type: discordgo.PermissionOverwriteTypeRole,
				ID:   guildID, // @everyone role
				Deny: int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages),
			},
			{
				Type:  discordgo.PermissionOverwriteTypeMember,
				ID:    owner.ID,
				Allow: int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory | discordgo.PermissionAttachFiles | discordgo.PermissionEmbedLinks),
			},
		}

		channel, err := s.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
			Name:                 ticket.ThreadName,
			Type:                 discordgo.ChannelTypeGuildText,
			ParentID:             parentID,
			PermissionOverwrites: overwrites,
		})
		if err != nil {
			return nil, err
		}
		channelID = channel.ID
		ticket.ThreadID = channel.ID
	} else {
		// Create a thread (default behavior)
		parentID := panel.Ticket.Settings.ThreadChannelID
		if parentID == "" {
			parentID = fallbackChannelID
		}

		thread, err := s.ThreadStartComplex(parentID, &discordgo.ThreadStart{
			Name:                ticket.ThreadName,
			AutoArchiveDuration: 10080,
			Type:                discordgo.ChannelTypeGuildPrivateThread,
			Invitable:           false,
		})
		if err != nil {
			return nil, err
		}
		channelID = thread.ID
		ticket.ThreadID = thread.ID

		if err := s.ThreadMemberAdd(thread.ID, owner.ID); err != nil {
			_, _ = s.ChannelDelete(thread.ID)
			return nil, err
		}
	}

	embed := TicketWelcomeMessage(ticket, panel, category, guildName)
	ping := "<@" + ticket.OwnerID + ">"
	if category.PingStaff && ticket.Status != types.TicketStatusClosed {
		for _, roleID := range pingRoleIDs(category) {
			ping += " <@&" + roleID + ">"
		}
	}
	buttons := ticketWelcomeButtons(ticket, panel)
	message, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:         ping,
		Embeds:          []*discordgo.MessageEmbed{embed},
		Components:      buttons,
		AllowedMentions: ticketMentions(ticket, panel, category),
		Flags:           ticketWelcomeFlags(category.Welcome),
	})
	if err != nil {
		_, _ = s.ChannelDelete(channelID)
		return nil, err
	}
	ticket.WelcomeMessageID = message.ID

	// For channel-based tickets, add staff role permissions after welcome message is sent
	if mode == types.TicketModeChannel {
		for _, roleID := range staffRoleIDs(panel, category) {
			_ = s.ChannelPermissionSet(channelID, roleID, discordgo.PermissionOverwriteTypeRole,
				int64(discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionReadMessageHistory|discordgo.PermissionAttachFiles|discordgo.PermissionEmbedLinks|discordgo.PermissionManageMessages),
				0)
		}
	}

	if err := Tickets.Update(owner.ID, guildID, func(ip *types.InteractivePanels) error {
		ip.Tickets.Open[ticket.ThreadID] = ticket
		return nil
	}); err != nil {
		return nil, err
	}
	ticketActivityIndex.Lock()
	ticketActivityIndex.channels[ticketActivityKey(guildID, ticket.ThreadID)] = struct{}{}
	ticketActivityIndex.Unlock()

	PostTicketLog(s, panel, TicketLogMessage(ColorSuccess, "Ticket Opened", "A new ticket was opened.", []string{
		"- **Thread:** <#" + ticket.ThreadID + ">",
		"- **Reference:** `#" + ticket.Ref + "`",
		"- **Category:** " + ticket.CategoryLabel,
		"- **Owner:** <@" + ticket.OwnerID + ">",
	}), nil)
	return ticket, nil
}

var (
	invalidChannelNameChars = regexp.MustCompile(`[^a-z0-9-]+`)
	repeatedChannelHyphens  = regexp.MustCompile(`-+`)
)

func channelTicketName(name, reference string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.Map(func(char rune) rune {
		if unicode.IsSpace(char) {
			return '-'
		}
		return char
	}, name)
	name = invalidChannelNameChars.ReplaceAllString(name, "")
	name = repeatedChannelHyphens.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "ticket-" + strings.ToLower(reference)
	}
	name = truncate(name, 100)
	return strings.TrimRight(name, "-")
}

// ticketMentions limits pings to the ticket's owner and (when enabled) its staff roles.
func ticketMentions(ticket *types.Ticket, panel *types.InteractivePanel, category *types.TicketCategory) *discordgo.MessageAllowedMentions {
	config := category.Welcome.AllowedMentions
	if config == nil {
		mentions := &discordgo.MessageAllowedMentions{
			Parse: []discordgo.AllowedMentionType{},
			Users: []string{ticket.OwnerID},
		}
		if category.PingStaff {
			mentions.Roles = pingRoleIDs(category)
		}
		return mentions
	}
	users := validMentionIDs(config.Users)
	roles := validMentionIDs(config.Roles)
	parse := make([]discordgo.AllowedMentionType, 0, 3)
	seen := map[string]bool{}
	for _, kind := range config.Parse {
		if kind != "everyone" && kind != "users" && kind != "roles" || seen[kind] {
			continue
		}
		if kind == "users" && len(users) > 0 || kind == "roles" && len(roles) > 0 {
			continue
		}
		seen[kind] = true
		parse = append(parse, discordgo.AllowedMentionType(kind))
	}
	if !seen["users"] {
		users = validMentionIDs(append([]string{ticket.OwnerID}, users...))
	}
	if !seen["roles"] && category.PingStaff {
		roles = validMentionIDs(append(pingRoleIDs(category), roles...))
	}
	return &discordgo.MessageAllowedMentions{
		Parse:       parse,
		Users:       users,
		Roles:       roles,
		RepliedUser: config.RepliedUser,
	}
}

func ticketWelcomeFlags(welcome types.TicketWelcome) discordgo.MessageFlags {
	var flags discordgo.MessageFlags
	if welcome.SuppressEmbeds {
		flags |= discordgo.MessageFlagsSuppressEmbeds
	}
	if welcome.SuppressNotifications {
		flags |= discordgo.MessageFlagsSuppressNotifications
	}
	return flags
}

// GuildDisplayName returns the guild's name, or a neutral fallback.
func GuildDisplayName(s *discordgo.Session, guildID string) string {
	if guild, err := s.State.Guild(guildID); err == nil && guild != nil && guild.Name != "" {
		return guild.Name
	}
	if guild, err := s.Guild(guildID); err == nil && guild != nil && guild.Name != "" {
		return guild.Name
	}
	return "this server"
}

// ============================================================================
// Ticket Lifecycle: Claim
// ============================================================================

// ClaimTicket assigns the ticket to the staff member and refreshes the welcome message.
func ClaimTicket(s *discordgo.Session, guildID, threadID, staffID string) (*types.Ticket, error) {
	return setTicketClaim(s, guildID, threadID, staffID, true, false)
}

// UnclaimTicket releases a claimed ticket. Administrators may release anyone's claim.
func UnclaimTicket(s *discordgo.Session, guildID, threadID, staffID string, isAdmin bool) (*types.Ticket, error) {
	return setTicketClaim(s, guildID, threadID, staffID, false, isAdmin)
}

func setTicketClaim(s *discordgo.Session, guildID, threadID, staffID string, claim, isAdmin bool) (*types.Ticket, error) {
	var ticket *types.Ticket
	var panel *types.InteractivePanel
	err := Tickets.Update(staffID, guildID, func(ip *types.InteractivePanels) error {
		found := ip.Tickets.Open[threadID]
		if found == nil {
			return ErrTicketNotFound
		}
		panel = ip.Panels[found.PanelID]
		if panel == nil || panel.Ticket == nil || !panel.Ticket.Settings.ClaimEnabled {
			return ErrTicketClaimDisabled
		}

		if claim {
			if found.Status == types.TicketStatusClaimed {
				return ErrTicketAlreadyClaimed
			}
			found.Status = types.TicketStatusClaimed
			found.ClaimedBy = staffID
			found.ClaimedAt = time.Now().Unix()
			appendTicketLog(found, "claimed", staffID, "Ticket claimed.", found.ClaimedAt)
			stats := StaffStatsFor(ip, staffID)
			stats.Claimed++
			stats.LastClaimedAt = found.ClaimedAt
		} else {
			if found.Status != types.TicketStatusClaimed {
				return ErrTicketNotClaimed
			}
			if found.ClaimedBy != staffID && !isAdmin {
				return ErrTicketAlreadyClaimed
			}
			if stats := StaffStatsFor(ip, found.ClaimedBy); stats.Claimed > 0 {
				stats.Claimed--
			}
			found.Status = types.TicketStatusOpen
			found.ClaimedBy = ""
			found.ClaimedAt = 0
			appendTicketLog(found, "unclaimed", staffID, "Ticket claim released.", time.Now().Unix())
		}
		copied := *found
		ticket = &copied
		return nil
	})
	if err != nil {
		return nil, err
	}

	RefreshTicketWelcome(s, guildID, ticket, panel)

	title, color := "Ticket Claimed", ColorDefault
	description := fmt.Sprintf("<@%s> claimed this ticket.", staffID)
	if !claim {
		title, color = "Ticket Unclaimed", ColorCaution
		description = fmt.Sprintf("<@%s> released this ticket.", staffID)
	}
	PostTicketLog(s, panel, TicketLogMessage(color, title, description, []string{
		"- **Thread:** <#" + ticket.ThreadID + ">",
		"- **Reference:** `#" + ticket.Ref + "`",
		"- **Owner:** <@" + ticket.OwnerID + ">",
	}), nil)
	return ticket, nil
}

// RefreshTicketWelcome re-renders the welcome message so its status and buttons match the ticket.
func RefreshTicketWelcome(s *discordgo.Session, guildID string, ticket *types.Ticket, panel *types.InteractivePanel) {
	if ticket == nil || panel == nil || ticket.WelcomeMessageID == "" {
		return
	}
	category := FindTicketCategory(panel, ticket.CategoryID)
	if category == nil {
		return
	}
	embed := TicketWelcomeMessage(ticket, panel, category, GuildDisplayName(s, guildID))
	buttons := ticketWelcomeButtons(ticket, panel)
	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:              ticket.WelcomeMessageID,
		Channel:         ticket.ThreadID,
		Embeds:          &[]*discordgo.MessageEmbed{embed},
		Components:      &buttons,
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
		Flags:           ticketWelcomeFlags(category.Welcome),
	})
}

// ============================================================================
// Ticket Lifecycle: Close
// ============================================================================

// SetPendingCloseReason remembers the reason typed into /ticket close until it is confirmed.
func SetPendingCloseReason(actorID, guildID, threadID, reason string) error {
	return Tickets.Update(actorID, guildID, func(ip *types.InteractivePanels) error {
		ticket := ip.Tickets.Open[threadID]
		if ticket == nil {
			return ErrTicketNotFound
		}
		ticket.CloseReason = truncate(reason, 500)
		return nil
	})
}

// CloseTicket archives the ticket, saves its transcript to the log channel (and
// the owner's DMs, when enabled), then deletes or locks the thread.
func CloseTicket(s *discordgo.Session, guildID, threadID, closerID string, closerIsStaff bool) (*types.Ticket, error) {
	return closeTicket(s, guildID, threadID, closerID, closerIsStaff)
}

func closeTicket(s *discordgo.Session, guildID, threadID, closerID string, closerIsStaff bool) (*types.Ticket, error) {
	var ticket *types.Ticket
	var panel *types.InteractivePanel
	err := Tickets.Update(closerID, guildID, func(ip *types.InteractivePanels) error {
		found := ip.Tickets.Open[threadID]
		if found == nil {
			return ErrTicketNotFound
		}
		panel = ip.Panels[found.PanelID]
		found.Status = types.TicketStatusClosed
		found.ClosedAt = time.Now().Unix()
		found.ClosedBy = closerID
		appendTicketLog(found, "closed", closerID, "Ticket closed.", found.ClosedAt)
		if closerIsStaff {
			StaffStatsFor(ip, closerID).Closed++
		}
		copied := *found
		ticket = &copied
		ArchiveTicket(ip, found)
		return nil
	})
	if err != nil {
		return nil, err
	}
	removeTicketActivityChannel(guildID, threadID)

	settings := types.TicketSettings{}
	if panel != nil && panel.Ticket != nil {
		settings = panel.Ticket.Settings
	}

	transcriptName := fmt.Sprintf("%s.html", strings.ToLower(ticket.OwnerName))
	transcript, messages, transcriptErr := BuildTicketTranscript(s, ticket)
	if transcriptErr != nil {
		log.Printf("[Tickets] could not read the complete transcript for ticket %s in guild %s: %v", threadID, guildID, transcriptErr)
	}
	ticket.ArchiveURL = ""
	archive := struct {
		SchemaVersion      int                       `json:"schema_version"`
		Ticket             *types.Ticket             `json:"ticket"`
		Messages           []types.DiscordLogMessage `json:"messages"`
		TranscriptComplete bool                      `json:"transcript_complete"`
		ArchivedAt         int64                     `json:"archived_at"`
	}{
		SchemaVersion:      1,
		Ticket:             ticket,
		Messages:           messages,
		TranscriptComplete: transcriptErr == nil && len(messages) < 5000,
		ArchivedAt:         time.Now().Unix(),
	}
	if !Blobs.Enabled() {
		log.Printf("[Tickets] JSON archive not saved for ticket %s in guild %s: blob storage is not configured", threadID, guildID)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		archiveURL, archiveErr := Blobs.PutJSON(ctx, fmt.Sprintf("tickets/%s/%s.json", guildID, threadID), archive)
		cancel()
		if archiveErr != nil {
			log.Printf("[Tickets] could not save JSON archive for ticket %s in guild %s: %v", threadID, guildID, archiveErr)
		} else {
			ticket.ArchiveURL = archiveURL
			if updateErr := Tickets.Update(sdk.SystemActor, guildID, func(ip *types.InteractivePanels) error {
				for _, archived := range ip.Tickets.History {
					if archived.ThreadID == threadID {
						archived.ArchiveURL = archiveURL
						return nil
					}
				}
				return ErrTicketNotFound
			}); updateErr != nil {
				log.Printf("[Tickets] saved JSON archive for ticket %s but could not attach its URL to history: %v", threadID, updateErr)
			}
		}
	}
	file := func() *discordgo.File {
		return &discordgo.File{Name: transcriptName, ContentType: "text/html", Reader: bytes.NewReader(transcript)}
	}

	guildName := GuildDisplayName(s, guildID)
	lines := TicketClosedLines(ticket, guildName)
	closedLog := TicketTranscriptMessage(ColorError, "### 📄 Ticket Closed",
		"This ticket has been closed. The conversation transcript is attached for your records.", lines, transcriptName)
	PostTicketLogV2(s, panel, closedLog, []*discordgo.File{file()})

	if settings.DMTranscript {
		if dm, err := s.UserChannelCreate(ticket.OwnerID); err == nil {
			_, _ = s.ChannelMessageSendComplex(dm.ID, &discordgo.MessageSend{
				Flags: discordgo.MessageFlagsIsComponentsV2,
				Components: TicketTranscriptMessage(ColorError, "### 📄 Your Ticket Was Closed",
					"Your ticket has been closed. The conversation transcript is attached for your records.", lines, transcriptName),
				Files: []*discordgo.File{file()},
			})
		}
	}

	if settings.DeleteOnClose {
		time.AfterFunc(5*time.Second, func() { _, _ = s.ChannelDelete(threadID) })
	} else {
		archived, locked := true, true
		_, _ = s.ChannelEditComplex(threadID, &discordgo.ChannelEdit{Archived: &archived, Locked: &locked})
	}
	return ticket, nil
}

func ticketLastActivity(ticket *types.Ticket) int64 {
	if ticket.LastActivityAt > 0 {
		return ticket.LastActivityAt
	}
	return ticket.CreatedAt
}

type ticketInactivityAction uint8

const (
	ticketInactivityNone ticketInactivityAction = iota
	ticketInactivityRemind
)

func ticketInactivityActionFor(settings types.TicketSettings, ticket *types.Ticket, now time.Time) ticketInactivityAction {
	if ticket == nil || !settings.InactivityEnabled || !validInactivitySettings(settings) {
		return ticketInactivityNone
	}
	lastActive := ticketLastActivity(ticket)
	idle := now.Sub(time.Unix(lastActive, 0))
	if idle < time.Duration(settings.InactivityReminderHours)*time.Hour {
		return ticketInactivityNone
	}
	if ticket.LastInactivityNoticeAt >= lastActive &&
		now.Sub(time.Unix(ticket.LastInactivityNoticeAt, 0)) < time.Duration(settings.InactivityReminderHours)*time.Hour {
		return ticketInactivityNone
	}
	return ticketInactivityRemind
}

// HandleTicketMessage resets the inactivity clock and lets the first staff reply claim the ticket.
func HandleTicketMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Message == nil || m.GuildID == "" || m.Author == nil || m.Author.Bot || m.WebhookID != "" {
		return
	}
	if !ticketTracksActivity(m.GuildID, m.ChannelID) {
		return
	}
	err := Tickets.UpdateOpen(m.Author.ID, m.GuildID, func(ip *types.InteractivePanels) error {
		if ticket := ip.Tickets.Open[m.ChannelID]; ticket != nil {
			ticket.LastActivityAt = time.Now().Unix()
			ticket.LastInactivityNoticeAt = 0
		}
		return nil
	})
	if err != nil {
		log.Printf("[Tickets] could not update activity for ticket %s in guild %s: %v", m.ChannelID, m.GuildID, err)
		return
	}
	autoClaimTicketOnStaffResponse(s, m)
}

func autoClaimTicketOnStaffResponse(s *discordgo.Session, m *discordgo.MessageCreate) {
	ip, err := Tickets.LoadOpen(m.GuildID)
	if err != nil {
		log.Printf("[Tickets] could not load ticket %s in guild %s for auto-claim: %v", m.ChannelID, m.GuildID, err)
		return
	}
	ticket := ip.Tickets.Open[m.ChannelID]
	if ticket == nil || ticket.Status != types.TicketStatusOpen {
		return
	}
	panel := ip.Panels[ticket.PanelID]
	if panel == nil || panel.Ticket == nil || !panel.Ticket.Settings.ClaimEnabled {
		return
	}
	category := FindTicketCategory(panel, ticket.CategoryID)

	member := m.Member
	if member == nil {
		member, err = s.GuildMember(m.GuildID, m.Author.ID)
		if err != nil {
			log.Printf("[Tickets] could not load member %s in guild %s for ticket auto-claim: %v", m.Author.ID, m.GuildID, err)
			return
		}
	}
	if !IsTicketStaff(member, panel, category) {
		return
	}
	if _, err := ClaimTicket(s, m.GuildID, m.ChannelID, m.Author.ID); err != nil && !errors.Is(err, ErrTicketAlreadyClaimed) {
		log.Printf("[Tickets] could not auto-claim ticket %s for staff member %s: %v", m.ChannelID, m.Author.ID, err)
	}
}

var ticketInactivitySweepOnce sync.Once

// StartTicketInactivitySweeper checks configured open tickets once a minute.
func StartTicketInactivitySweeper(s *discordgo.Session) {
	ticketInactivitySweepOnce.Do(func() {
		go func() {
			sweepTicketInactivity(s)
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				sweepTicketInactivity(s)
			}
		}()
	})
}

func sweepTicketInactivity(s *discordgo.Session) {
	if s == nil || s.State == nil {
		return
	}
	for _, guild := range s.State.Guilds {
		if guild == nil {
			continue
		}
		ip, err := Tickets.LoadOpen(guild.ID)
		if err != nil {
			log.Printf("[Tickets] could not load inactivity settings for guild %s: %v", guild.ID, err)
			continue
		}
		activeChannels := make(map[string]struct{}, len(ip.Tickets.Open))
		for channelID := range ip.Tickets.Open {
			activeChannels[channelID] = struct{}{}
		}
		setGuildTicketActivityChannels(guild.ID, activeChannels)
		now := time.Now()
		for _, candidate := range ip.Tickets.Open {
			if candidate == nil {
				continue
			}
			panel := ip.Panels[candidate.PanelID]
			if panel == nil || panel.Ticket == nil {
				continue
			}
			if candidate.LastActivityAt == 0 {
				var migrated *types.Ticket
				err := Tickets.UpdateOpen(sdk.SystemActor, guild.ID, func(current *types.InteractivePanels) error {
					ticket := current.Tickets.Open[candidate.ThreadID]
					if ticket == nil {
						return nil
					}
					if ticket.LastActivityAt == 0 {
						ticket.LastActivityAt = now.Unix()
					}
					migrated = copyTicket(ticket)
					return nil
				})
				if err != nil {
					log.Printf("[Tickets] could not initialize inactivity timestamp for ticket %s in guild %s: %v", candidate.ThreadID, guild.ID, err)
					continue
				}
				if migrated == nil {
					continue
				}
				candidate = migrated
			}
			settings := panel.Ticket.Settings
			if !settings.InactivityEnabled {
				continue
			}
			if !validInactivitySettings(settings) {
				log.Printf("[Tickets] invalid inactivity settings for panel %s in guild %s", panel.ID, guild.ID)
				continue
			}
			action := ticketInactivityActionFor(settings, candidate, now)
			if action == ticketInactivityNone {
				continue
			}

			notify := false
			err := Tickets.UpdateOpen(sdk.SystemActor, guild.ID, func(current *types.InteractivePanels) error {
				ticket := current.Tickets.Open[candidate.ThreadID]
				if ticket == nil {
					return nil
				}
				currentPanel := current.Panels[ticket.PanelID]
				if currentPanel == nil || currentPanel.Ticket == nil {
					return nil
				}
				currentSettings := currentPanel.Ticket.Settings
				if ticketInactivityActionFor(currentSettings, ticket, now) != ticketInactivityRemind {
					return nil
				}
				ticket.LastInactivityNoticeAt = now.Unix()
				candidate = copyTicket(ticket)
				panel = currentPanel
				settings = currentSettings
				notify = true
				return nil
			})
			if err != nil {
				log.Printf("[Tickets] could not schedule inactivity reminder for ticket %s in guild %s: %v", candidate.ThreadID, guild.ID, err)
				continue
			}
			if !notify {
				continue
			}
			elapsedHours := int(now.Sub(time.Unix(ticketLastActivity(candidate), 0)).Hours())
			vars := map[string]string{
				"user":           "<@" + candidate.OwnerID + ">",
				"username":       candidate.OwnerName,
				"inactive_hours": strconv.Itoa(elapsedHours),
				"reference":      candidate.Ref,
				"number":         strconv.Itoa(candidate.Number),
				"category":       candidate.CategoryLabel,
			}
			message, err := sendInactivityReminder(s, guild.ID, candidate.ThreadID, candidate.OwnerID, settings, vars)
			if err != nil {
				log.Printf("[Tickets] could not send inactivity reminder for ticket %s in guild %s: %v", candidate.ThreadID, guild.ID, err)
			} else if err := Tickets.UpdateOpen(sdk.SystemActor, guild.ID, func(current *types.InteractivePanels) error {
				if ticket := current.Tickets.Open[candidate.ThreadID]; ticket != nil {
					appendTicketLog(ticket, "inactivity_reminder", "", message, now.Unix())
				}
				return nil
			}); err != nil {
				log.Printf("[Tickets] could not save inactivity reminder log for ticket %s in guild %s: %v", candidate.ThreadID, guild.ID, err)
			}
		}
	}
}

func validInactivitySettings(settings types.TicketSettings) bool {
	return settings.InactivityReminderHours > 0 && settings.InactivityReminderHours <= maxInactivityHours
}

// sendInactivityReminder posts the reminder into the ticket: the saved Embed Builder message the panel points at
// (plain message, embeds or Components V2) when it still exists, otherwise the panel's plain text. It returns the
// text kept in the ticket log. Nothing is closed or deleted.
func sendInactivityReminder(s *discordgo.Session, guildID, threadID, ownerID string, settings types.TicketSettings, vars map[string]string) (string, error) {
	fill := VarFiller(vars)
	if id := strings.TrimSpace(settings.InactivityMessageID); id != "" {
		if doc := savedMessageDoc(guildID, id); doc != nil {
			payload := doc.Payload(fill)
			if !doc.AllowMentions && doc.AllowedMentions == nil {
				payload["allowed_mentions"] = map[string]any{"parse": []string{}, "users": []string{ownerID}}
			}
			_, err := SendPayload(s, threadID, payload)
			return "Inactivity reminder (" + doc.Name + ")", err
		}
	}
	text := fill(settings.InactivityMessage)
	_, err := s.ChannelMessageSendComplex(threadID, &discordgo.MessageSend{
		Content:         text,
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}, Users: []string{ownerID}},
	})
	return text, err
}

// savedMessageDoc is the Embed Builder message with the id, or nil when it is gone.
func savedMessageDoc(guildID, id string) *MessageDoc {
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if err != nil || cfg.Panels.IsEmpty() {
		return nil
	}
	var sections struct {
		Messages []MessageDoc `json:"messages"`
	}
	if cfg.Panels.Unmarshal(&sections) != nil {
		return nil
	}
	for i := range sections.Messages {
		if sections.Messages[i].ID == id {
			return &sections.Messages[i]
		}
	}
	return nil
}

func copyTicket(ticket *types.Ticket) *types.Ticket {
	copied := *ticket
	return &copied
}

// ============================================================================
// Logging
// ============================================================================

// PostTicketLogV2 sends a Components V2 entry (the closed-ticket transcript) to the log channel.
func PostTicketLogV2(s *discordgo.Session, panel *types.InteractivePanel, components []discordgo.MessageComponent, files []*discordgo.File) {
	if panel == nil || panel.Ticket == nil || panel.Ticket.Settings.LogChannelID == "" {
		return
	}
	_, _ = s.ChannelMessageSendComplex(panel.Ticket.Settings.LogChannelID, &discordgo.MessageSend{
		Flags:           discordgo.MessageFlagsIsComponentsV2,
		Components:      components,
		Files:           files,
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
	})
}

// PostTicketLog sends an entry to the panel's log channel, if one is configured.
func PostTicketLog(s *discordgo.Session, panel *types.InteractivePanel, embed *discordgo.MessageEmbed, files []*discordgo.File) {
	if panel == nil || panel.Ticket == nil || panel.Ticket.Settings.LogChannelID == "" {
		return
	}
	_, _ = s.ChannelMessageSendComplex(panel.Ticket.Settings.LogChannelID, &discordgo.MessageSend{
		Embeds:          []*discordgo.MessageEmbed{embed},
		Files:           files,
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
	})
}

// ============================================================================
// Transcript Generation
// ============================================================================

const transcriptStyle = `
*{box-sizing:border-box;margin:0;padding:0}
html{background:#1e1f22}
body{background:#1e1f22;color:#dbdee1;font-family:'gg sans','Noto Sans','Helvetica Neue',Helvetica,Arial,sans-serif;font-size:16px;line-height:1.375;padding:32px 16px}
a{color:#00a8fc;text-decoration:none}a:hover{text-decoration:underline}
.wrap{max-width:920px;margin:0 auto}
.header{background:#2b2d31;border-radius:12px;padding:24px;margin-bottom:16px;border-top:4px solid #5865f2}
.header h1{color:#f2f3f5;font-size:24px;font-weight:700;margin-bottom:4px}
.header .sub{color:#949ba4;font-size:14px;margin-bottom:16px}
.stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:12px}
.stat{background:#1e1f22;border-radius:8px;padding:10px 14px}
.stat .k{color:#949ba4;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.04em}
.stat .v{color:#f2f3f5;font-size:15px;font-weight:600;margin-top:2px;word-break:break-word}
.chat{background:#313338;border-radius:12px;padding:8px 0;overflow:hidden}
.group{display:flex;gap:16px;padding:8px 20px;margin-top:10px}
.group:first-child{margin-top:0}
.group:hover{background:#2e3035}
.avatar{width:40px;height:40px;border-radius:50%;flex-shrink:0;background:#5865f2;object-fit:cover}
.avatar.fallback{display:flex;align-items:center;justify-content:center;color:#fff;font-weight:700;font-size:18px}
.main{flex:1;min-width:0}
.meta{display:flex;align-items:center;gap:8px;margin-bottom:2px;flex-wrap:wrap}
.name{color:#f2f3f5;font-weight:600}
.tag{background:#5865f2;color:#fff;font-size:10px;font-weight:700;padding:1px 5px;border-radius:4px;text-transform:uppercase}
.time{color:#949ba4;font-size:12px}
.line{position:relative}
.line+.line{margin-top:2px}
.line .hover-time{display:none;position:absolute;left:-56px;top:2px;color:#949ba4;font-size:11px;width:46px;text-align:right}
.group:hover .line .hover-time{display:block}
.body{white-space:pre-wrap;word-wrap:break-word;overflow-wrap:anywhere}
.body code{background:#1e1f22;border-radius:4px;padding:1px 5px;font-family:Consolas,'Courier New',monospace;font-size:14px}
.body pre{background:#2b2d31;border:1px solid #1e1f22;border-radius:6px;padding:8px 10px;margin:4px 0;font-family:Consolas,'Courier New',monospace;font-size:14px;white-space:pre-wrap;overflow-x:auto}
.body pre code{background:none;padding:0}
.mention{background:rgba(88,101,242,.3);color:#c9cdfb;border-radius:4px;padding:0 3px;font-weight:500}
.body img.emoji{width:22px;height:22px;vertical-align:-5px}
.reply{color:#949ba4;font-size:13px;margin-bottom:4px}
.embed{max-width:520px;background:#2b2d31;border-left:4px solid #5865f2;border-radius:4px;padding:8px 16px 12px 12px;margin-top:6px}
.embed .e-author{color:#f2f3f5;font-size:14px;font-weight:600;margin-top:4px}
.embed .e-title{color:#f2f3f5;font-weight:700;margin-top:4px}
.embed .e-desc{font-size:14px;margin-top:4px;white-space:pre-wrap;overflow-wrap:anywhere}
.embed .e-fields{display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-top:8px}
.embed .e-field{grid-column:1/-1;font-size:14px}
.embed .e-field.inline{grid-column:auto}
.embed .e-fname{color:#f2f3f5;font-weight:700;font-size:14px}
.embed .e-fval{white-space:pre-wrap;overflow-wrap:anywhere}
.embed .e-foot{color:#949ba4;font-size:12px;margin-top:8px}
.embed img.e-img{max-width:100%;border-radius:4px;margin-top:8px}
.attachments{margin-top:6px;display:flex;flex-direction:column;gap:6px;align-items:flex-start}
.attachments img{max-width:420px;max-height:320px;border-radius:6px}
.file{background:#2b2d31;border:1px solid #1e1f22;border-radius:6px;padding:8px 12px;font-size:14px}
.empty{color:#949ba4;text-align:center;padding:40px}
.footer{color:#949ba4;font-size:12px;text-align:center;margin-top:16px}
@media(max-width:600px){.group{padding:8px 12px;gap:12px}.stats{grid-template-columns:1fr 1fr}}
`

var (
	reCodeBlock   = regexp.MustCompile("(?s)```(?:[a-zA-Z0-9_+-]*\\n)?(.*?)```")
	reInlineCode  = regexp.MustCompile("`([^`\\n]+)`")
	reBold        = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reUnderline   = regexp.MustCompile(`__(.+?)__`)
	reItalicStar  = regexp.MustCompile(`\*([^*\n]+)\*`)
	reItalicUnder = regexp.MustCompile(`(^|[^\w])_([^_\n]+)_([^\w]|$)`)
	reStrike      = regexp.MustCompile(`~~(.+?)~~`)
	reLink        = regexp.MustCompile(`https?://(?:[^\s<&]|&amp;)+`)
	reUserMention = regexp.MustCompile(`&lt;@!?(\d+)&gt;`)
	reRoleMention = regexp.MustCompile(`&lt;@&amp;(\d+)&gt;`)
	reChanMention = regexp.MustCompile(`&lt;#(\d+)&gt;`)
	reEmoji       = regexp.MustCompile(`&lt;(a?):([A-Za-z0-9_]+):(\d+)&gt;`)
	reTimestamp   = regexp.MustCompile(`&lt;t:(\d+)(?::[a-zA-Z])?&gt;`)
	reHeading     = regexp.MustCompile(`(?m)^#{1,3} (.+)$`)
	reQuote       = regexp.MustCompile(`(?m)^&gt; (.+)$`)
)

// transcriptMarkdown turns Discord markdown into safe HTML. Text is escaped
// first, so nothing a member typed can inject markup.
func transcriptMarkdown(text string, names map[string]string) string {
	var stash []string
	keep := func(markup string) string {
		stash = append(stash, markup)
		return "\x00" + strconv.Itoa(len(stash)-1) + "\x00"
	}

	text = html.EscapeString(text)
	text = reCodeBlock.ReplaceAllStringFunc(text, func(m string) string {
		return keep("<pre><code>" + strings.TrimRight(reCodeBlock.FindStringSubmatch(m)[1], "\n") + "</code></pre>")
	})
	text = reInlineCode.ReplaceAllStringFunc(text, func(m string) string {
		return keep("<code>" + reInlineCode.FindStringSubmatch(m)[1] + "</code>")
	})

	text = reUserMention.ReplaceAllStringFunc(text, func(m string) string {
		id := reUserMention.FindStringSubmatch(m)[1]
		name := names[id]
		if name == "" {
			name = "unknown-user"
		}
		return keep(`<span class="mention">@` + html.EscapeString(name) + `</span>`)
	})
	text = reRoleMention.ReplaceAllString(text, `<span class="mention">@role</span>`)
	text = reChanMention.ReplaceAllString(text, `<span class="mention">#channel</span>`)
	text = reEmoji.ReplaceAllStringFunc(text, func(m string) string {
		g := reEmoji.FindStringSubmatch(m)
		ext := "png"
		if g[1] == "a" {
			ext = "gif"
		}
		return keep(fmt.Sprintf(`<img class="emoji" alt=":%s:" src="https://cdn.discordapp.com/emojis/%s.%s?size=44">`, g[2], g[3], ext))
	})
	text = reTimestamp.ReplaceAllStringFunc(text, func(m string) string {
		unix, _ := strconv.ParseInt(reTimestamp.FindStringSubmatch(m)[1], 10, 64)
		return keep(`<span class="mention">` + time.Unix(unix, 0).UTC().Format("2006-01-02 15:04 UTC") + `</span>`)
	})
	text = reLink.ReplaceAllStringFunc(text, func(m string) string {
		return keep(`<a href="` + m + `" target="_blank" rel="noopener">` + m + `</a>`)
	})

	text = reHeading.ReplaceAllString(text, `<strong>$1</strong>`)
	text = reQuote.ReplaceAllString(text, `<span style="border-left:4px solid #4e5058;padding-left:8px;display:inline-block">$1</span>`)
	text = reBold.ReplaceAllString(text, `<strong>$1</strong>`)
	text = reUnderline.ReplaceAllString(text, `<u>$1</u>`)
	text = reItalicStar.ReplaceAllString(text, `<em>$1</em>`)
	text = reItalicUnder.ReplaceAllString(text, `$1<em>$2</em>$3`)
	text = reStrike.ReplaceAllString(text, `<s>$1</s>`)

	for idx := len(stash) - 1; idx >= 0; idx-- {
		text = strings.ReplaceAll(text, "\x00"+strconv.Itoa(idx)+"\x00", stash[idx])
	}
	return text
}

// BuildTicketTranscript renders the thread's messages as a standalone HTML page.
func BuildTicketTranscript(s *discordgo.Session, ticket *types.Ticket) ([]byte, []types.DiscordLogMessage, error) {
	var messages []*discordgo.Message
	var fetchErr error
	beforeID := ""
	for len(messages) < 5000 {
		batch, err := s.ChannelMessages(ticket.ThreadID, 100, beforeID, "", "")
		if err != nil {
			fetchErr = err
			break
		}
		if len(batch) == 0 {
			break
		}
		messages = append(messages, batch...)
		beforeID = batch[len(batch)-1].ID
		if len(batch) < 100 {
			break
		}
	}
	// Discord returns newest first; the page reads oldest first.
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	archiveMessages := make([]types.DiscordLogMessage, 0, len(messages))
	for _, message := range messages {
		archiveMessages = append(archiveMessages, logMessage(message))
	}

	names := map[string]string{}
	for _, message := range messages {
		if message.Author != nil {
			names[message.Author.ID] = transcriptDisplayName(message.Author, message.Member)
		}
		for _, mentioned := range message.Mentions {
			if _, known := names[mentioned.ID]; !known {
				names[mentioned.ID] = mentioned.Username
			}
		}
	}

	var page bytes.Buffer
	fmt.Fprintf(&page, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>%s</style></head><body><div class="wrap">`,
		html.EscapeString(ticket.ThreadName), transcriptStyle)

	stat := func(key, value string) string {
		return `<div class="stat"><div class="k">` + key + `</div><div class="v">` + html.EscapeString(value) + `</div></div>`
	}
	formatTime := func(unix int64) string {
		if unix == 0 {
			return "-"
		}
		return time.Unix(unix, 0).UTC().Format("Jan 2, 2006 15:04 UTC")
	}
	closer := "-"
	if ticket.ClosedBy != "" {
		closer = names[ticket.ClosedBy]
		if closer == "" {
			closer = ticket.ClosedBy
		}
	}
	fmt.Fprintf(&page, `<div class="header"><h1>%s</h1><div class="sub">Ticket transcript</div><div class="stats">%s%s%s%s%s%s</div></div>`,
		html.EscapeString(ticket.ThreadName),
		stat("Reference", "#"+ticket.Ref),
		stat("Category", ticket.CategoryLabel),
		stat("Owner", ticket.OwnerName),
		stat("Opened", formatTime(ticket.CreatedAt)),
		stat("Closed", formatTime(ticket.ClosedAt)),
		stat("Messages", strconv.Itoa(len(messages))+" - closed by "+closer),
	)

	page.WriteString(`<div class="chat">`)
	if len(messages) == 0 {
		page.WriteString(`<div class="empty">No messages were found in this ticket.</div>`)
	}

	var lastAuthor string
	var lastTime time.Time
	open := false
	for _, message := range messages {
		authorID := ""
		if message.Author != nil {
			authorID = message.Author.ID
		}
		grouped := open && authorID == lastAuthor && message.Timestamp.Sub(lastTime) < 7*time.Minute
		if !grouped {
			if open {
				page.WriteString(`</div></div>`)
			}
			writeTranscriptGroupHeader(&page, message, names)
			open = true
		}
		lastAuthor, lastTime = authorID, message.Timestamp
		writeTranscriptMessage(&page, message, names)
	}
	if open {
		page.WriteString(`</div></div>`)
	}
	page.WriteString(`</div>`)
	fmt.Fprintf(&page, `<div class="footer">Generated %s</div></div></body></html>`, time.Now().UTC().Format("Jan 2, 2006 15:04 UTC"))
	return page.Bytes(), archiveMessages, fetchErr
}

func appendTicketLog(ticket *types.Ticket, eventType, actorID, message string, createdAt int64) {
	ticket.Logs = append(ticket.Logs, types.TicketLogEntry{
		Type: eventType, ActorID: actorID, Message: message, CreatedAt: createdAt,
	})
}

func transcriptDisplayName(user *discordgo.User, member *discordgo.Member) string {
	if member != nil && member.Nick != "" {
		return member.Nick
	}
	if user.GlobalName != "" {
		return user.GlobalName
	}
	return user.Username
}

func writeTranscriptGroupHeader(page *bytes.Buffer, message *discordgo.Message, names map[string]string) {
	name := "Unknown"
	avatar := `<div class="avatar fallback">?</div>`
	tag := ""
	if message.Author != nil {
		name = names[message.Author.ID]
		if name == "" {
			name = message.Author.Username
		}
		if message.Author.Bot {
			tag = `<span class="tag">Bot</span>`
		}
		avatar = fmt.Sprintf(`<img class="avatar" src="%s" alt="">`, html.EscapeString(message.Author.AvatarURL("64")))
	}
	fmt.Fprintf(page, `<div class="group">%s<div class="main"><div class="meta"><span class="name">%s</span>%s<span class="time">%s</span></div>`,
		avatar, html.EscapeString(name), tag, message.Timestamp.UTC().Format("Jan 2, 2006 15:04"))
}

func writeTranscriptMessage(page *bytes.Buffer, message *discordgo.Message, names map[string]string) {
	page.WriteString(`<div class="line">`)
	fmt.Fprintf(page, `<span class="hover-time">%s</span>`, message.Timestamp.UTC().Format("15:04"))

	if text := strings.TrimSpace(message.Content); text != "" {
		fmt.Fprintf(page, `<div class="body">%s</div>`, transcriptMarkdown(text, names))
	}
	if v2 := strings.Join(componentText(message.Components), "\n"); strings.TrimSpace(v2) != "" {
		fmt.Fprintf(page, `<div class="body">%s</div>`, transcriptMarkdown(v2, names))
	}

	for _, embed := range message.Embeds {
		color := "#5865f2"
		if embed.Color != 0 {
			color = HexColor(embed.Color)
		}
		fmt.Fprintf(page, `<div class="embed" style="border-left-color:%s">`, color)
		if embed.Author != nil && embed.Author.Name != "" {
			fmt.Fprintf(page, `<div class="e-author">%s</div>`, html.EscapeString(embed.Author.Name))
		}
		if embed.Title != "" {
			fmt.Fprintf(page, `<div class="e-title">%s</div>`, transcriptMarkdown(embed.Title, names))
		}
		if embed.Description != "" {
			fmt.Fprintf(page, `<div class="e-desc">%s</div>`, transcriptMarkdown(embed.Description, names))
		}
		if len(embed.Fields) > 0 {
			page.WriteString(`<div class="e-fields">`)
			for _, field := range embed.Fields {
				class := "e-field"
				if field.Inline {
					class += " inline"
				}
				fmt.Fprintf(page, `<div class="%s"><div class="e-fname">%s</div><div class="e-fval">%s</div></div>`,
					class, html.EscapeString(field.Name), transcriptMarkdown(field.Value, names))
			}
			page.WriteString(`</div>`)
		}
		if embed.Image != nil && embed.Image.URL != "" {
			fmt.Fprintf(page, `<img class="e-img" src="%s" alt="">`, html.EscapeString(embed.Image.URL))
		}
		if embed.Footer != nil && embed.Footer.Text != "" {
			fmt.Fprintf(page, `<div class="e-foot">%s</div>`, html.EscapeString(embed.Footer.Text))
		}
		page.WriteString(`</div>`)
	}

	if len(message.Attachments) > 0 {
		page.WriteString(`<div class="attachments">`)
		for _, attachment := range message.Attachments {
			url := html.EscapeString(attachment.URL)
			if strings.HasPrefix(attachment.ContentType, "image/") {
				fmt.Fprintf(page, `<a href="%s" target="_blank" rel="noopener"><img src="%s" alt="%s"></a>`, url, url, html.EscapeString(attachment.Filename))
				continue
			}
			fmt.Fprintf(page, `<div class="file">📎 <a href="%s" target="_blank" rel="noopener">%s</a></div>`, url, html.EscapeString(attachment.Filename))
		}
		page.WriteString(`</div>`)
	}
	page.WriteString(`</div>`)
}

// componentText flattens the text of a V2 message so it shows up in the transcript.
func componentText(components []discordgo.MessageComponent) []string {
	var out []string
	for _, component := range components {
		switch c := component.(type) {
		case *discordgo.TextDisplay:
			out = append(out, c.Content)
		case discordgo.TextDisplay:
			out = append(out, c.Content)
		case *discordgo.Container:
			out = append(out, componentText(c.Components)...)
		case discordgo.Container:
			out = append(out, componentText(c.Components)...)
		case *discordgo.Section:
			out = append(out, componentText(c.Components)...)
		case discordgo.Section:
			out = append(out, componentText(c.Components)...)
		}
	}
	return out
}

// ============================================================================
// Components V2 Plumbing
// ============================================================================

var customEmojiPattern = regexp.MustCompile(`^<(a?):([A-Za-z0-9_]+):(\d{17,20})>$`)

// v2Text returns a markdown text block.
func v2Text(content string) discordgo.TextDisplay {
	return discordgo.TextDisplay{Content: truncate(content, 3900)}
}

// v2Divider returns a separator line with the given spacing.
func v2Divider(divider bool, large bool) discordgo.Separator {
	spacing := discordgo.SeparatorSpacingSizeSmall
	if large {
		spacing = discordgo.SeparatorSpacingSizeLarge
	}
	return discordgo.Separator{Divider: &divider, Spacing: &spacing}
}

// v2Container wraps components in a coloured container.
func v2Container(color int, components ...discordgo.MessageComponent) discordgo.Container {
	accent := color
	return discordgo.Container{AccentColor: &accent, Components: components}
}

// v2Row wraps up to five buttons (or one select menu) in an action row.
func v2Row(components ...discordgo.MessageComponent) discordgo.ActionsRow {
	return discordgo.ActionsRow{Components: components}
}

// v2Button builds a button with an optional emoji.
func v2Button(customID, label, emoji string, style discordgo.ButtonStyle) discordgo.Button {
	return discordgo.Button{
		CustomID: customID,
		Label:    label,
		Style:    style,
		Emoji:    parseComponentEmoji(emoji),
	}
}

// v2ButtonRows lays buttons out five to a row.
func v2ButtonRows(buttons []discordgo.MessageComponent) []discordgo.MessageComponent {
	var rows []discordgo.MessageComponent
	for start := 0; start < len(buttons); start += 5 {
		end := start + 5
		if end > len(buttons) {
			end = len(buttons)
		}
		rows = append(rows, v2Row(buttons[start:end]...))
	}
	return rows
}

// parseComponentEmoji reads a unicode emoji or a `<:name:id>` custom emoji.
func parseComponentEmoji(raw string) *discordgo.ComponentEmoji {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if match := customEmojiPattern.FindStringSubmatch(raw); match != nil {
		return &discordgo.ComponentEmoji{Name: match[2], ID: match[3], Animated: match[1] == "a"}
	}
	return &discordgo.ComponentEmoji{Name: raw}
}

// emojiPrefix returns the category emoji followed by a space, for inline text.
func emojiPrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return raw + " "
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

// UpdateV2 rewrites the message a component (or component-born modal) came from.
func UpdateV2(s *discordgo.Session, i *discordgo.InteractionCreate, components ...discordgo.MessageComponent) error {
	return InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsIsComponentsV2, Components: components},
	})
}

// EditInteractionV2 fills a slash command's deferred reply with a V2 message.
// Prefix invocations have no interaction token, so they get a normal message.
func EditInteractionV2(s *discordgo.Session, i *discordgo.InteractionCreate, components ...discordgo.MessageComponent) error {
	if IsPrefixInteraction(i) {
		_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Flags:      discordgo.MessageFlagsIsComponentsV2,
			Components: components,
			Reference: &discordgo.MessageReference{
				MessageID: PrefixSourceMessageID(i),
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

// RespondEmbed answers a component or modal interaction with an embed only the clicker sees.
func RespondEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) error {
	return InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:  discordgo.MessageFlagsEphemeral,
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}

// HexColor formats a colour for display and for pre-filling modals.
func HexColor(color int) string {
	return fmt.Sprintf("#%06X", color)
}

// ModalValues flattens a modal submission into custom_id -> value.
func ModalValues(i *discordgo.InteractionCreate) map[string]string {
	values := map[string]string{}
	for _, component := range i.ModalSubmitData().Components {
		row, ok := component.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, inner := range row.Components {
			if input, ok := inner.(*discordgo.TextInput); ok {
				values[input.CustomID] = strings.TrimSpace(input.Value)
			}
		}
	}
	return values
}

// ModalTextInput builds one single-component modal row.
func ModalTextInput(customID, label, value, placeholder string, paragraph, required bool, maxLength int) discordgo.ActionsRow {
	style := discordgo.TextInputShort
	if paragraph {
		style = discordgo.TextInputParagraph
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.TextInput{
			CustomID:    customID,
			Label:       label,
			Style:       style,
			Value:       value,
			Placeholder: placeholder,
			Required:    required,
			MaxLength:   maxLength,
		},
	}}
}

// RespondModal opens a modal in answer to a component click.
func RespondModal(s *discordgo.Session, i *discordgo.InteractionCreate, customID, title string, rows ...discordgo.MessageComponent) error {
	return InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{CustomID: customID, Title: title, Components: rows},
	})
}

// ============================================================================
// Access Checks
// ============================================================================

// InteractionIsAdmin reports whether the interacting member can manage the guild.
func InteractionIsAdmin(i *discordgo.InteractionCreate) bool {
	if i == nil || i.Member == nil {
		return false
	}
	return i.Member.Permissions&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) != 0
}

// IsTicketStaff reports whether the member may work tickets of the category:
// an administrator, or anyone holding a panel-wide or category staff role.
func IsTicketStaff(member *discordgo.Member, panel *types.InteractivePanel, category *types.TicketCategory) bool {
	if member == nil {
		return false
	}
	if member.Permissions&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) != 0 {
		return true
	}
	for _, roleID := range staffRoleIDs(panel, category) {
		if MemberHasRole(member, roleID) {
			return true
		}
	}
	return false
}

// pingRoleIDs are the roles a ticket in this category mentions: only the category's own list. The panel-wide staff
// roles can open every ticket but are not pinged unless the category lists them too.
func pingRoleIDs(category *types.TicketCategory) []string {
	seen := map[string]bool{}
	var roles []string
	if category != nil {
		for _, id := range category.StaffRoleIDs {
			if id != "" && !seen[id] {
				seen[id] = true
				roles = append(roles, id)
			}
		}
	}
	return roles
}

// staffRoleIDs merges the panel-wide and category staff roles without duplicates: everyone who may see the ticket.
func staffRoleIDs(panel *types.InteractivePanel, category *types.TicketCategory) []string {
	seen := map[string]bool{}
	var roles []string
	add := func(ids []string) {
		for _, id := range ids {
			if id != "" && !seen[id] {
				seen[id] = true
				roles = append(roles, id)
			}
		}
	}
	if panel != nil && panel.Ticket != nil {
		add(panel.Ticket.Settings.StaffRoleIDs)
	}
	if category != nil {
		add(category.StaffRoleIDs)
	}
	return roles
}

// ============================================================================
// Ticket Views
// ============================================================================

// TicketPlaceholders expands {user}, {username}, {category}, {reference}, {number}, {staff}, {server},
// {created} (when the ticket was opened, as a Discord relative time) and {status} (where it stands) in configurable ticket text.
func TicketPlaceholders(text string, ticket *types.Ticket, panel *types.InteractivePanel, category *types.TicketCategory, guildName string) string {
	staff := "the support team"
	if roles := pingRoleIDs(category); len(roles) > 0 {
		mentions := make([]string, len(roles))
		for idx, id := range roles {
			mentions[idx] = "<@&" + id + ">"
		}
		staff = strings.Join(mentions, " ")
	}
	return strings.NewReplacer(
		"{user}", "<@"+ticket.OwnerID+">",
		"{username}", ticket.OwnerName,
		"{category}", ticket.CategoryLabel,
		"{reference}", "#"+ticket.Ref,
		"{number}", fmt.Sprintf("%d", ticket.Number),
		"{staff}", staff,
		"{server}", guildName,
		"{created}", fmt.Sprintf("<t:%d:R>", ticket.CreatedAt),
		"{status}", TicketStatusLabel(ticket),
	).Replace(text)
}

// TicketPanelMessage renders the published panel members open tickets from.
//
// The panel is a list of blocks (look.Layout): the built-in ones show the panel's fields, the others are free text,
// lines, galleries, sections and link buttons. A panel without a layout has the standard one.
func TicketPanelMessage(panel *types.InteractivePanel) []discordgo.MessageComponent {
	look := panel.Appearance
	color := look.Color
	if color == 0 {
		color = defaultPanelColor
	}

	layout := ticketPanelLayout(panel)
	var inner []discordgo.MessageComponent
	for _, block := range layout {
		inner = append(inner, panelBlockComponents(panel, block)...)
	}
	if look.NoColor { // a container without an accent has no bar
		return []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	}
	return []discordgo.MessageComponent{v2Container(color, inner...)}
}

func ticketPanelLayout(panel *types.InteractivePanel) []types.PanelBlock {
	layout := panel.Appearance.Layout
	if len(layout) == 0 {
		return StandardPanelLayout(panel)
	}
	if len(panel.Ticket.Categories) > 0 && !layoutHas(layout, "buttons") && !ticketCategoryButtonsInline(panel) {
		layout = append(append([]types.PanelBlock{}, layout...), types.PanelBlock{Type: "buttons"})
	}
	return layout
}

func ticketPanelSend(panel *types.InteractivePanel) *discordgo.MessageSend {
	look := panel.Appearance
	switch look.Format {
	case "message":
		var parts []string
		if look.Title != "" {
			parts = append(parts, "## "+look.Title)
		}
		if look.Description != "" {
			parts = append(parts, look.Description)
		}
		if !look.HideCategoryList {
			for _, category := range panel.Ticket.Categories {
				text := fmt.Sprintf("**%s%s**", emojiPrefix(category.Emoji), category.Label)
				if category.Description != "" {
					text += "\n" + category.Description
				}
				parts = append(parts, text)
			}
		}
		if look.Footer != "" {
			parts = append(parts, look.Footer)
		}
		return &discordgo.MessageSend{
			Content:         truncate(strings.Join(parts, "\n\n"), 2000),
			Components:      ticketPanelButtons(panel),
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
	case "embed":
		embed := &discordgo.MessageEmbed{
			Title:       look.Title,
			Description: look.Description,
			Color:       look.Color,
		}
		if look.NoColor {
			embed.Color = EmbedBlendColor
		} else if embed.Color == 0 {
			embed.Color = defaultPanelColor
		}
		if look.Footer != "" {
			embed.Footer = &discordgo.MessageEmbedFooter{Text: look.Footer}
		}
		if look.BannerURL != "" {
			embed.Image = &discordgo.MessageEmbedImage{URL: look.BannerURL}
		}
		if !look.HideCategoryList {
			for _, category := range panel.Ticket.Categories {
				value := category.Description
				if value == "" {
					value = "Select the button below to open this ticket."
				}
				embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
					Name:   truncate(emojiPrefix(category.Emoji)+category.Label, 256),
					Value:  truncate(value, 1024),
					Inline: false,
				})
			}
		}
		if embed.Title == "" && embed.Description == "" && embed.Footer == nil && embed.Image == nil && len(embed.Fields) == 0 {
			embed.Description = "Choose a category below to open a ticket."
		}
		return &discordgo.MessageSend{
			Embeds:          []*discordgo.MessageEmbed{embed},
			Components:      ticketPanelButtons(panel),
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
	default:
		return &discordgo.MessageSend{
			Flags:           discordgo.MessageFlagsIsComponentsV2,
			Components:      TicketPanelMessage(panel),
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
	}
}

func ticketPanelPayload(panel *types.InteractivePanel) (map[string]any, error) {
	serialized, err := json.Marshal(ticketPanelSend(panel))
	if err != nil {
		return nil, fmt.Errorf("serialize ticket panel message: %w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(serialized, &payload); err != nil {
		return nil, fmt.Errorf("decode ticket panel message: %w", err)
	}
	payload["allowed_mentions"] = ticketPanelAllowedMentions(panel.Appearance.AllowedMentions)
	flags := 0
	if panel.Appearance.SuppressEmbeds {
		flags |= 1 << 2
	}
	if panel.Appearance.SuppressNotifications {
		flags |= 1 << 12
	}
	if panel.Appearance.Format == "component" || panel.Appearance.Format == "" {
		flags |= V2Flag
		applyTicketPanelComponentIDs(panel, payload)
	}
	if flags != 0 {
		payload["flags"] = flags
	}
	return payload, nil
}

func ticketPanelAllowedMentions(config *types.PanelAllowedMentions) map[string]any {
	if config == nil {
		return map[string]any{"parse": []string{}}
	}
	users := validMentionIDs(config.Users)
	roles := validMentionIDs(config.Roles)
	parse := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, kind := range config.Parse {
		if kind != "everyone" && kind != "users" && kind != "roles" || seen[kind] {
			continue
		}
		if kind == "users" && len(users) > 0 || kind == "roles" && len(roles) > 0 {
			continue
		}
		seen[kind] = true
		parse = append(parse, kind)
	}
	out := map[string]any{"parse": parse, "replied_user": config.RepliedUser}
	if len(users) > 0 {
		out["users"] = users
	}
	if len(roles) > 0 {
		out["roles"] = roles
	}
	return out
}

func applyTicketPanelComponentIDs(panel *types.InteractivePanel, payload map[string]any) {
	components, ok := payload["components"].([]any)
	if !ok || len(components) == 0 {
		return
	}
	container, ok := components[0].(map[string]any)
	if !ok {
		return
	}
	setDiscordComponentID(container, panel.Appearance.DiscordID)
	children, ok := container["components"].([]any)
	if !ok {
		return
	}
	layout := ticketPanelLayout(panel)
	position := 0
	for _, block := range layout {
		rendered := panelBlockComponents(panel, block)
		for range rendered {
			if position >= len(children) {
				return
			}
			child, ok := children[position].(map[string]any)
			if !ok {
				position++
				continue
			}
			if block.Type != "categories" && block.Type != "buttons" {
				setDiscordComponentID(child, block.DiscordID)
			}
			switch block.Type {
			case "gallery":
				if items, ok := child["items"].([]any); ok {
					for idx, item := range block.Items {
						if idx < len(items) {
							if itemPayload, ok := items[idx].(map[string]any); ok {
								setDiscordComponentID(itemPayload, item.DiscordID)
							}
						}
					}
				}
			case "section":
				if block.AccessoryType == "thumbnail" {
					if accessory, ok := child["accessory"].(map[string]any); ok {
						setDiscordComponentID(accessory, block.ThumbnailDiscordID)
					}
				}
			case "links":
				if rows, ok := child["components"].([]any); ok {
					for idx, link := range block.Links {
						if idx < len(rows) {
							if button, ok := rows[idx].(map[string]any); ok {
								setDiscordComponentID(button, link.DiscordID)
							}
						}
					}
				}
			}
			position++
		}
	}
}

func setDiscordComponentID(payload map[string]any, id uint32) {
	if id != 0 {
		payload["id"] = id
	}
}

func ticketPanelButtons(panel *types.InteractivePanel) []discordgo.MessageComponent {
	buttons := make([]discordgo.MessageComponent, 0, len(panel.Ticket.Categories))
	for _, category := range panel.Ticket.Categories {
		label := category.ButtonLabel
		if label == "" {
			label = category.Label
		}
		button := v2Button(
			fmt.Sprintf("%s:%s:%s", TicketIDOpen, panel.ID, category.ID),
			label,
			category.Emoji,
			discordgo.ButtonStyle(category.ButtonStyle),
		)
		buttons = append(buttons, button)
	}
	return v2ButtonRows(buttons)
}

func layoutHas(layout []types.PanelBlock, blockType string) bool {
	for _, b := range layout {
		if b.Type == blockType {
			return true
		}
	}
	return false
}

func ticketCategoryButtonsInline(panel *types.InteractivePanel) bool {
	if panel == nil || panel.Ticket == nil || !panel.Ticket.Settings.InlineCategoryButtons || panel.Appearance.HideCategoryList {
		return false
	}
	layout := panel.Appearance.Layout
	if len(layout) == 0 {
		layout = StandardPanelLayout(panel)
	}
	return layoutHas(layout, "categories")
}

// StandardPanelLayout is the layout of a panel that has no custom one: banner, title, description and a line, the
// category list, the footer, a line and the buttons. The line under the description is left out when there is no
// description, and the one above the buttons when there are no categories.
func StandardPanelLayout(panel *types.InteractivePanel) []types.PanelBlock {
	look := panel.Appearance
	categories := panel.Ticket.Categories
	line := types.PanelBlock{Type: "separator"}
	var out []types.PanelBlock
	if look.BannerURL != "" {
		out = append(out, types.PanelBlock{Type: "banner"})
	}
	if look.Title != "" {
		out = append(out, types.PanelBlock{Type: "title"})
	}
	if look.Description != "" {
		out = append(out, types.PanelBlock{Type: "description"}, line)
	}
	if !look.HideCategoryList && len(categories) > 0 {
		out = append(out, types.PanelBlock{Type: "categories"})
	}
	if look.Footer != "" {
		out = append(out, types.PanelBlock{Type: "footer"})
	}
	if len(categories) > 0 {
		out = append(out, line)
	}
	return append(out, types.PanelBlock{Type: "buttons"})
}

// panelBlockComponents renders one block; a block with nothing to show (an empty title, an unknown type) renders nothing.
func panelBlockComponents(panel *types.InteractivePanel, b types.PanelBlock) []discordgo.MessageComponent {
	look := panel.Appearance
	categories := panel.Ticket.Categories
	one := func(c discordgo.MessageComponent) []discordgo.MessageComponent {
		return []discordgo.MessageComponent{c}
	}

	switch b.Type {
	case "banner":
		if look.BannerURL != "" {
			return one(discordgo.MediaGallery{Items: []discordgo.MediaGalleryItem{{Media: discordgo.UnfurledMediaItem{URL: look.BannerURL}}}})
		}
	case "title":
		if look.Title != "" {
			return one(v2Text("## " + look.Title))
		}
	case "description":
		if look.Description != "" {
			return one(v2Text(look.Description))
		}
	case "categories":
		var out []discordgo.MessageComponent
		if !look.HideCategoryList {
			for _, category := range categories {
				text := fmt.Sprintf("**%s%s**", emojiPrefix(category.Emoji), category.Label)
				if category.Description != "" {
					text += "\n-# " + category.Description
				}
				if ticketCategoryButtonsInline(panel) {
					buttonLabel := category.ButtonLabel
					if buttonLabel == "" {
						buttonLabel = category.Label
					}
					button := v2Button(
						fmt.Sprintf("%s:%s:%s", TicketIDOpen, panel.ID, category.ID),
						buttonLabel,
						category.Emoji,
						discordgo.ButtonStyle(category.ButtonStyle),
					)
					out = append(out, discordgo.Section{
						Components: []discordgo.MessageComponent{v2Text(text)},
						Accessory:  button,
					})
				} else {
					out = append(out, v2Text(text))
				}
			}
		}
		return out
	case "footer":
		if look.Footer != "" {
			return one(v2Text(look.Footer))
		}
	case "buttons":
		if ticketCategoryButtonsInline(panel) {
			return nil
		}
		if len(categories) == 0 {
			return one(v2Text("-# This panel has no categories yet."))
		}
		buttons := make([]discordgo.MessageComponent, 0, len(categories))
		for _, category := range categories {
			buttonLabel := category.ButtonLabel
			if buttonLabel == "" {
				buttonLabel = category.Label
			}
			button := v2Button(fmt.Sprintf("%s:%s:%s", TicketIDOpen, panel.ID, category.ID), buttonLabel, category.Emoji, discordgo.ButtonStyle(category.ButtonStyle))
			buttons = append(buttons, button)
		}
		return v2ButtonRows(buttons)
	case "text":
		if strings.TrimSpace(b.Content) != "" {
			return one(v2Text(b.Content))
		}
	case "separator":
		divider := b.Divider == nil || *b.Divider
		return one(v2Divider(divider, b.Large))
	case "gallery":
		var items []discordgo.MediaGalleryItem
		for _, it := range b.Items {
			if it.URL == "" || len(items) == 10 {
				continue
			}
			item := discordgo.MediaGalleryItem{Media: discordgo.UnfurledMediaItem{URL: it.URL}}
			if it.Description != "" {
				description := it.Description
				item.Description = &description
			}
			items = append(items, item)
		}
		if len(items) > 0 {
			return one(discordgo.MediaGallery{Items: items})
		}
	case "section":
		if strings.TrimSpace(b.Text) == "" {
			return nil
		}
		if b.AccessoryType == "ticket_button" {
			category := FindTicketCategory(panel, b.AccessoryCategoryID)
			if category == nil {
				return one(v2Text(b.Text))
			}
			buttonLabel := category.ButtonLabel
			if buttonLabel == "" {
				buttonLabel = category.Label
			}
			button := v2Button(
				fmt.Sprintf("%s:%s:%s", TicketIDOpen, panel.ID, category.ID),
				buttonLabel,
				category.Emoji,
				discordgo.ButtonStyle(category.ButtonStyle),
			)
			return one(discordgo.Section{
				Components: []discordgo.MessageComponent{v2Text(b.Text)},
				Accessory:  button,
			})
		}
		if b.ThumbnailURL == "" {
			return one(v2Text(b.Text))
		}
		return one(discordgo.Section{
			Components: []discordgo.MessageComponent{v2Text(b.Text)},
			Accessory:  discordgo.Thumbnail{Media: discordgo.UnfurledMediaItem{URL: b.ThumbnailURL}},
		})
	case "links":
		var buttons []discordgo.MessageComponent
		for _, l := range b.Links {
			if l.URL != "" && l.Label != "" && len(buttons) < 5 {
				buttons = append(buttons, discordgo.Button{Style: discordgo.LinkButton, Label: l.Label, URL: l.URL})
			}
		}
		if len(buttons) > 0 {
			return one(v2Row(buttons...))
		}
	}
	return nil
}

// ticketDetailFields lays the ticket details out as inline columns:
// Reference / Category / Owner on the first row, Created At / Status below.
func ticketDetailFields(ticket *types.Ticket) []*discordgo.MessageEmbedField {
	return []*discordgo.MessageEmbedField{
		{Name: "Reference", Value: "#" + ticket.Ref, Inline: true},
		{Name: "Category", Value: ticket.CategoryLabel, Inline: true},
		{Name: "Owner", Value: "<@" + ticket.OwnerID + ">", Inline: true},
		{Name: "Created At", Value: fmt.Sprintf("<t:%d:R>", ticket.CreatedAt), Inline: true},
		{Name: "Status", Value: TicketStatusLabel(ticket), Inline: true},
	}
}

// TicketStatusLabel describes where a ticket currently stands.
func TicketStatusLabel(ticket *types.Ticket) string {
	label := DotYellow + " Awaiting Staff"
	switch ticket.Status {
	case types.TicketStatusClaimed:
		label = fmt.Sprintf("%s Claimed by <@%s>", DotGreen, ticket.ClaimedBy)
	case types.TicketStatusClosed:
		return DotRed + " Closed"
	}
	if ticket.Locked {
		label += " · Locked"
	}
	return label
}

func ticketWelcomeButtons(ticket *types.Ticket, panel *types.InteractivePanel) []discordgo.MessageComponent {
	closeButton := discordgo.Button{
		Label:    "Close Ticket",
		Style:    discordgo.DangerButton,
		Emoji:    &discordgo.ComponentEmoji{Name: "🔒"},
		CustomID: TicketIDClose,
	}
	buttons := []discordgo.MessageComponent{closeButton}

	if panel != nil && panel.Ticket != nil && panel.Ticket.Settings.ClaimEnabled {
		claim := discordgo.Button{
			Label:    "Claim Ticket",
			Style:    discordgo.SecondaryButton,
			Emoji:    &discordgo.ComponentEmoji{Name: "✋"},
			CustomID: TicketIDClaim,
		}
		if ticket.Status == types.TicketStatusClaimed {
			claim = discordgo.Button{
				Label:    "Unclaim",
				Style:    discordgo.SecondaryButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "↩️"},
				CustomID: TicketIDUnclaim,
			}
		}
		buttons = append(buttons, claim)
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: buttons}}
}

// TicketCloseConfirmMessage asks the clicker to confirm closing the ticket.
func TicketCloseConfirmMessage(deleteOnClose bool) *discordgo.MessageEmbed {
	outcome := "locked and archived"
	if deleteOnClose {
		outcome = "deleted"
	}
	return &discordgo.MessageEmbed{
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: ThumbGuild},
		Title:       "Close Ticket",
		Description: fmt.Sprintf("Are you sure you want to close this ticket? A transcript of the conversation will be saved and this thread will be %s.", outcome),
		Color:       ColorCaution,
	}
}

// TicketCloseConfirmButtons are the Yes / Cancel buttons under the close prompt.
func TicketCloseConfirmButtons() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Yes, Close",
				Style:    discordgo.DangerButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "🔒"},
				CustomID: TicketIDCloseYes,
			},
			discordgo.Button{
				Label:    "Cancel",
				Style:    discordgo.SecondaryButton,
				CustomID: TicketIDCloseNo,
			},
		}},
	}
}

// TicketTranscriptMessage is the V2 card for a closed ticket (the title carries its own markdown heading): the summary
// lines plus the transcript file attached inside the same container.
func TicketTranscriptMessage(color int, title, description string, lines []string, transcriptName string) []discordgo.MessageComponent {
	inner := []discordgo.MessageComponent{v2Text(title + "\n" + description)}
	if len(lines) > 0 {
		inner = append(inner, v2Text(strings.Join(lines, "\n")))
	}
	inner = append(inner,
		v2Text("You can download the transcript of this ticket below."),
		discordgo.FileComponent{File: discordgo.UnfurledMediaItem{URL: "attachment://" + transcriptName}},
	)
	return []discordgo.MessageComponent{v2Container(color, inner...)}
}

// TicketClosedLines is the summary shared by the closed-ticket log and the owner's DM.
func TicketClosedLines(ticket *types.Ticket, guildName string) []string {
	closedBy := "<@" + ticket.ClosedBy + ">"
	if ticket.ClosedBy == inactivityCloser {
		closedBy = "Inactivity policy"
	}
	lines := []string{
		"- **Thread:** `" + ticket.ThreadName + "`",
		"- **Server:** `" + guildName + "`",
		"- **Closed By:** " + closedBy,
	}
	return lines
}

// ============================================================================
// Panel Publishing
// ============================================================================

func TicketPublishPanelScreen(panels []*types.InteractivePanel) []discordgo.MessageComponent {
	inner := []discordgo.MessageComponent{
		v2Text("## Publish a Ticket Panel\nCreate and customize ticket panels in the Ranked Bedwars Dashboard. This Discord command only publishes an existing panel; to make changes, use the dashboard."),
		v2Divider(true, false),
	}
	if len(panels) == 0 {
		inner = append(inner, v2Text("No ticket panels are configured yet. Create one in the dashboard, then use `/ticket panel` to publish it."))
		return []discordgo.MessageComponent{v2Container(ColorDefault, inner...)}
	}

	options := make([]discordgo.SelectMenuOption, 0, len(panels))
	for _, panel := range panels {
		options = append(options, discordgo.SelectMenuOption{
			Label:       truncate(panel.Name, 100),
			Value:       panel.ID,
			Description: fmt.Sprintf("%d categories", len(panel.Ticket.Categories)),
		})
	}
	inner = append(inner,
		v2Text("**1. Choose the panel to publish**"),
		v2Row(discordgo.SelectMenu{
			CustomID:    "tkt:publish:panel",
			Placeholder: "Select a dashboard ticket panel",
			Options:     options,
		}),
	)
	return []discordgo.MessageComponent{v2Container(ColorDefault, inner...)}
}

// TicketPublishChannelScreen asks where to send the selected dashboard panel.
func TicketPublishChannelScreen(panel *types.InteractivePanel) []discordgo.MessageComponent {
	one := 1
	inner := []discordgo.MessageComponent{
		v2Text(fmt.Sprintf("## Publish %s\nCreate and customize ticket panels in the Ranked Bedwars Dashboard. This command only publishes panels; make future changes in the dashboard.", panel.Name)),
		v2Divider(true, false),
		v2Text("**1. Panel:** " + panel.Name + "\n**2. Choose the channel to send it to**"),
		v2Row(discordgo.SelectMenu{
			MenuType:    discordgo.ChannelSelectMenu,
			CustomID:    "tkt:publish:channel:" + panel.ID,
			Placeholder: "Select a channel",
			MinValues:   &one,
			MaxValues:   1,
			ChannelTypes: []discordgo.ChannelType{
				discordgo.ChannelTypeGuildText,
				discordgo.ChannelTypeGuildNews,
			},
		}),
	}
	return []discordgo.MessageComponent{v2Container(ColorDefault, inner...)}
}

func TicketPublishChannelErrorScreen(panel *types.InteractivePanel, notice string) []discordgo.MessageComponent {
	components := TicketPublishChannelScreen(panel)
	container, ok := components[0].(discordgo.Container)
	if !ok {
		return components
	}
	container.Components = append([]discordgo.MessageComponent{v2Text(notice)}, container.Components...)
	return []discordgo.MessageComponent{container}
}

// HandleTicketComponent routes every `tkt:` button and select menu.
func HandleTicketComponent(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, TicketIDPrefix) {
		return false
	}
	parts := strings.Split(customID, ":")
	if len(parts) < 2 {
		return true
	}

	switch parts[1] {
	case "open":
		if len(parts) == 4 {
			handleTicketOpen(s, i, parts[2], parts[3])
		}
	case "publish":
		handleTicketPublish(s, i, parts)
	case "claim":
		handleTicketClaim(s, i, true)
	case "unclaim":
		handleTicketClaim(s, i, false)
	case "close":
		if len(parts) == 2 {
			handleTicketCloseAsk(s, i)
		} else {
			handleTicketCloseAnswer(s, i, parts[2] == "yes")
		}
	}
	return true
}

func handleTicketPublish(s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {
	if !InteractionIsAdmin(i) {
		_ = RespondEmbed(s, i, TicketPermissionDeniedEmbed("Only administrators can publish ticket panels. Create and edit panels in the dashboard."))
		return
	}
	if len(parts) == 3 && parts[2] == "panel" {
		ip, err := Tickets.Load(i.GuildID)
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		panel := ip.Panels[firstValue(i)]
		if panel == nil || panel.Ticket == nil {
			_ = UpdateV2(s, i, TicketPublishPanelScreen(SortedTicketPanels(ip))...)
			return
		}
		_ = UpdateV2(s, i, TicketPublishChannelScreen(panel)...)
		return
	}
	if len(parts) != 4 || parts[2] != "channel" {
		return
	}
	panelID := parts[3]
	channelID := firstValue(i)
	if channelID == "" {
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return
	}

	ip, err := Tickets.Load(i.GuildID)
	if err != nil {
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return
	}
	panel := ip.Panels[panelID]
	if panel == nil || panel.Ticket == nil {
		_ = UpdateV2(s, i, TicketPublishPanelScreen(SortedTicketPanels(ip))...)
		return
	}
	if len(panel.Ticket.Categories) == 0 {
		_ = UpdateV2(s, i, TicketPublishChannelErrorScreen(panel, "This panel has no ticket categories. Add categories in the dashboard before publishing.")...)
		return
	}

	payload, err := ticketPanelPayload(panel)
	if err != nil {
		_ = UpdateV2(s, i, TicketPublishChannelErrorScreen(panel, "Could not build the ticket panel message. Please review its configuration and try again.")...)
		return
	}
	messageID, err := SendPayload(s, channelID, payload)
	if err != nil {
		_ = UpdateV2(s, i, TicketPublishChannelErrorScreen(panel, "Could not publish the panel there. Check that I can view and send messages in the selected channel.")...)
		return
	}
	oldChannelID, oldMessageID := panel.ChannelID, panel.MessageID
	err = Tickets.Update(InteractionUserID(i), i.GuildID, func(current *types.InteractivePanels) error {
		stored := current.Panels[panelID]
		if stored == nil || stored.Ticket == nil {
			return ErrTicketPanelNotFound
		}
		stored.ChannelID = channelID
		stored.MessageID = messageID
		stored.UpdatedAt = time.Now().Unix()
		return nil
	})
	if err != nil {
		if deleteErr := s.ChannelMessageDelete(channelID, messageID); deleteErr != nil {
			log.Printf("[Tickets] could not remove unpublished panel message %s in channel %s after config save failed: %v", messageID, channelID, deleteErr)
		}
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return
	}
	if oldChannelID != "" && oldMessageID != "" && (oldChannelID != channelID || oldMessageID != messageID) {
		if err := s.ChannelMessageDelete(oldChannelID, oldMessageID); err != nil {
			log.Printf("[Tickets] could not remove previous published panel message %s in channel %s: %v", oldMessageID, oldChannelID, err)
		}
	}
	_ = UpdateV2(s, i, []discordgo.MessageComponent{
		v2Container(ColorSuccess,
			v2Text(fmt.Sprintf("## Panel Published\n**%s** was sent to <#%s>.\n\nTo change the panel’s content, categories, appearance, or settings, make those changes in the Ranked Bedwars Dashboard. `/ticket panel` is only for publishing an existing panel.", panel.Name, channelID)),
		),
	}...)
}

// handleTicketOpen creates a ticket from a panel button.
func handleTicketOpen(s *discordgo.Session, i *discordgo.InteractionCreate, panelID, categoryID string) {
	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	owner := i.Member.User
	if owner.Bot {
		editComponentEmbed(s, i, TicketBotTargetEmbed())
		return
	}

	ticket, err := OpenTicket(s, i.GuildID, panelID, categoryID, owner, i.ChannelID)
	if err != nil {
		editComponentEmbed(s, i, ticketOpenErrorEmbed(err))
		return
	}
	editComponentEmbed(s, i, ticketCreatedEmbed(ticket.ThreadID))
}

// handleTicketClaim claims or releases the ticket the button is in.
func handleTicketClaim(s *discordgo.Session, i *discordgo.InteractionCreate, claim bool) {
	ctx, err := LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil {
		_ = RespondEmbed(s, i, TicketContextErrorEmbed(err))
		return
	}
	if !IsTicketStaff(i.Member, ctx.Panel, ctx.Category) {
		_ = RespondEmbed(s, i, TicketPermissionDeniedEmbed("Only support staff can claim tickets."))
		return
	}

	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})

	staffID := InteractionUserID(i)
	if claim {
		_, err = ClaimTicket(s, i.GuildID, i.ChannelID, staffID)
	} else {
		_, err = UnclaimTicket(s, i.GuildID, i.ChannelID, staffID, InteractionIsAdmin(i))
	}
	if err != nil {
		followupEmbed(s, i, TicketActionErrorEmbed(err))
		return
	}

	title, description := "Ticket Claimed", fmt.Sprintf("<@%s> claimed this ticket and will be assisting you.", staffID)
	presetType := "success"
	if !claim {
		title, description = "Ticket Unclaimed", fmt.Sprintf("<@%s> released this ticket. Another staff member will pick it up.", staffID)
		presetType = "caution"
	}
	_, _ = s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{Preset(presetType, types.PresetOptions{
			Title:       title,
			Description: description,
		})},
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
	})
}

// handleTicketCloseAsk shows the "are you sure" prompt only the clicker can see.
func handleTicketCloseAsk(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx, err := LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil {
		_ = RespondEmbed(s, i, TicketContextErrorEmbed(err))
		return
	}
	if !CanCloseTicket(i, ctx) {
		_ = RespondEmbed(s, i, TicketPermissionDeniedEmbed("Only support staff can close this ticket."))
		return
	}
	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:      discordgo.MessageFlagsEphemeral,
			Embeds:     []*discordgo.MessageEmbed{TicketCloseConfirmMessage(ctx.Panel.Ticket.Settings.DeleteOnClose)},
			Components: TicketCloseConfirmButtons(),
		},
	})
}

// handleTicketCloseAnswer finishes or cancels a close the clicker was asked about.
func handleTicketCloseAnswer(s *discordgo.Session, i *discordgo.InteractionCreate, confirmed bool) {
	if !confirmed {
		_ = RespondEmbed(s, i, Preset("info", types.PresetOptions{
			Title:       "Close Cancelled",
			Description: "The ticket stays open.",
			Thumbnail:   ThumbGuild,
		}))
		return
	}

	ctx, err := LoadTicketContext(i.GuildID, i.ChannelID)
	if err != nil {
		_ = RespondEmbed(s, i, TicketContextErrorEmbed(err))
		return
	}
	if !CanCloseTicket(i, ctx) {
		_ = RespondEmbed(s, i, TicketPermissionDeniedEmbed("Only support staff can close this ticket."))
		return
	}

	_ = RespondEmbed(s, i, Preset("info", types.PresetOptions{
		Title:       "Closing Ticket",
		Description: "Saving the transcript. This only takes a moment.",
		Thumbnail:   ThumbGuild,
	}))

	closerIsStaff := IsTicketStaff(i.Member, ctx.Panel, ctx.Category)
	if _, err := CloseTicket(s, i.GuildID, i.ChannelID, InteractionUserID(i), closerIsStaff); err != nil {
		_ = RespondEmbed(s, i, Preset("error", types.PresetOptions{
			Title:       "Unexpected Error",
			Description: "The ticket could not be closed. Please try again later or contact support.",
			Thumbnail:   ThumbFlag,
		}))
		return
	}
	_ = RespondEmbed(s, i, Preset("success", types.PresetOptions{
		Title:       "Ticket Closed",
		Description: "The transcript has been saved and this ticket is being closed.",
		Thumbnail:   ThumbGuild,
	}))
}

func selectedValues(i *discordgo.InteractionCreate) []string {
	return i.MessageComponentData().Values
}

// firstValue returns the first selected value, or "" when the menu was cleared.
func firstValue(i *discordgo.InteractionCreate) string {
	if values := selectedValues(i); len(values) > 0 {
		return values[0]
	}
	return ""
}

// ============================================================================
// Embed Builders
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
	return ctx.Ticket.OwnerID == InteractionUserID(i) && ctx.Panel.Ticket.Settings.OwnerCanClose
}

// IsStaffOfAnyPanel reports whether the member is staff for at least one ticket panel.
func IsStaffOfAnyPanel(i *discordgo.InteractionCreate, ip *types.InteractivePanels) bool {
	if InteractionIsAdmin(i) {
		return true
	}
	for _, panel := range SortedTicketPanels(ip) {
		if IsTicketStaff(i.Member, panel, nil) {
			return true
		}
		for _, category := range panel.Ticket.Categories {
			if IsTicketStaff(i.Member, panel, category) {
				return true
			}
		}
	}
	return false
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
	PostTicketLog(s, panel, TicketLogMessage(ColorDefault, "Ticket Renamed", "<@"+staffID+"> renamed a ticket.", []string{
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
	title, color, verb := "Ticket Locked", ColorCaution, "locked"
	if !locked {
		title, color, verb = "Ticket Unlocked", ColorDefault, "unlocked"
	}
	PostTicketLog(s, panel, TicketLogMessage(color, title, "<@"+staffID+"> "+verb+" a ticket.", []string{
		"- **Ticket:** <#" + ticket.ThreadID + ">",
		"- **Reference:** `#" + ticket.Ref + "`",
		"- **Owner:** <@" + ticket.OwnerID + ">",
	}), nil)
	return ticket, nil
}

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
