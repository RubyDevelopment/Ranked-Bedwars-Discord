package helpers

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
)

// Everything behind the ticket system that is not the slash command itself:
// the ticket buttons (open, claim, close), the `/ticket_manage panel` builder's
// buttons, selects and modals, and the shared embeds. The "Tickets"
// event (events/ticket_interaction.go) routes interactions here.

// ============================================================================
// Constants & Configuration
// ============================================================================

const (
	// interactivePanelsField is the guild config section panels are stored in.
	interactivePanelsField = "interactive_panels"

	// MaxTicketHistory caps how many closed tickets are kept for /ticket logs.
	MaxTicketHistory = 200

	// MaxPanelsPerGuild and MaxCategoriesPerPanel keep a panel within what a
	// single Discord message can render.
	MaxPanelsPerGuild     = 10
	MaxCategoriesPerPanel = 10

	idAlphabet  = "abcdefghijklmnopqrstuvwxyz0123456789"
	refAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

	defaultPanelColor = 0x55FFFF
)

// Default ticket copy, written in the same voice as the bot's own messages.
const (
	defaultWelcomeTitle = "Ticket Created: {category}"
	defaultWelcomeBody  = "Hello {user},\n\nThank you for reaching out! Our support team has been notified and will be with you shortly.\n\nPlease explain your issue in detail and our support team will assist you."
	defaultNameFormat   = "{username} - Ticket: {category}"
	defaultPanelFooter  = "**Getting Started:**\nUse the buttons below to open a ticket and select the right category for faster support."
)

// Ticket component custom IDs are namespaced `tkt:` and colon separated.
const (
	TicketIDPrefix = "tkt:"

	TicketIDOpen      = "tkt:open"      // tkt:open:<panel>:<category>
	TicketIDClaim     = "tkt:claim"     // in-ticket
	TicketIDUnclaim   = "tkt:unclaim"   // in-ticket
	TicketIDClose     = "tkt:close"     // in-ticket, asks for confirmation
	TicketIDCloseYes  = "tkt:close:yes" // confirmation
	TicketIDCloseNo   = "tkt:close:no"  // confirmation
	TicketIDBuilder   = "tkt:b"         // panel manager
	TicketIDPanelEdit = "tkt:e"         // tkt:e:<panel>:<action>
	TicketIDCategory  = "tkt:c"         // tkt:c:<panel>:<category>:<action>
	TicketIDSettings  = "tkt:s"         // tkt:s:<panel>:<action>
	TicketIDModal     = "tkt:m"         // tkt:m:<kind>:<panel>[:<category>]
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

// Load returns the guild's interactive panels. A missing or empty section
// resolves to an empty, ready-to-use value.
func (st *TicketStore) Load(guildID string) (*types.InteractivePanels, error) {
	return loadInteractivePanels(guildID)
}

// Update loads the guild's panels, lets fn change them, and saves the result.
// fn returning an error aborts the save.
func (st *TicketStore) Update(actorID, guildID string, fn func(ip *types.InteractivePanels) error) error {
	lock := st.guildLock(guildID)
	lock.Lock()
	defer lock.Unlock()

	ip, err := loadInteractivePanels(guildID)
	if err != nil {
		return err
	}
	if err := fn(ip); err != nil {
		return err
	}
	err = database.Must().GuildConfig.SaveField(actorID, guildID, interactivePanelsField, ip)
	return err
}

func loadInteractivePanels(guildID string) (*types.InteractivePanels, error) {
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

// NewTicketPanel builds a fresh ticket panel pre-filled with friendly defaults.
func NewTicketPanel(name, title, actorID string) *types.InteractivePanel {
	now := time.Now().Unix()
	if title == "" {
		title = name
	}
	return &types.InteractivePanel{
		ID:   randomID(idAlphabet, 6),
		Type: types.PanelTypeTicket,
		Name: name,
		Appearance: types.PanelAppearance{
			Title:       title,
			Description: "Need assistance? Open a ticket below and our team will help you out.",
			Footer:      defaultPanelFooter,
			Color:       defaultPanelColor,
		},
		Ticket: &types.TicketPanelConfig{
			Settings: types.TicketSettings{
				MaxOpenPerUser: 1,
				ClaimEnabled:   true,
				DMTranscript:   true,
				DeleteOnClose:  true,
				OwnerCanClose:  true,
			},
		},
		CreatedBy: actorID,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// NewTicketCategory builds a category with the default welcome message.
func NewTicketCategory(label string) *types.TicketCategory {
	return &types.TicketCategory{
		ID:          randomID(idAlphabet, 4),
		Label:       label,
		ButtonStyle: 2,
		NameFormat:  defaultNameFormat,
		PingStaff:   true,
		Welcome: types.TicketWelcome{
			Title: defaultWelcomeTitle,
			Body:  defaultWelcomeBody,
			Color: defaultPanelColor,
		},
	}
}

// ============================================================================
// Ticket Helpers
// ============================================================================

// OpenTicketsFor returns the open tickets owned by userID in the guild.
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

// ArchiveTicket moves a closed ticket out of Open and into the capped history.
func ArchiveTicket(ip *types.InteractivePanels, ticket *types.Ticket) {
	delete(ip.Tickets.Open, ticket.ThreadID)
	ip.Tickets.History = append(ip.Tickets.History, ticket)
	if overflow := len(ip.Tickets.History) - MaxTicketHistory; overflow > 0 {
		ip.Tickets.History = ip.Tickets.History[overflow:]
	}
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

	ticket := &types.Ticket{
		PanelID:       panel.ID,
		CategoryID:    category.ID,
		CategoryLabel: category.Label,
		OwnerID:       owner.ID,
		OwnerName:     owner.Username,
		Status:        types.TicketStatusOpen,
		CreatedAt:     time.Now().Unix(),
		Mode:          mode,
	}

	// Reserve the number and reference first so the thread name can use them.
	if err := Tickets.Update(owner.ID, guildID, func(ip *types.InteractivePanels) error {
		ticket.Number, ticket.Ref = NextTicketIdentity(ip)
		return nil
	}); err != nil {
		return nil, err
	}

	guildName := GuildDisplayName(s, guildID)
	ticket.ThreadName = truncate(TicketPlaceholders(category.NameFormat, ticket, panel, category, guildName), 100)
	if strings.TrimSpace(ticket.ThreadName) == "" {
		ticket.ThreadName = fmt.Sprintf("%s - Ticket: %s", owner.Username, category.Label)
	}

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

	embed := TicketWelcomeMessage(ticket, panel, category, guildName, false)
	ping := "<@" + ticket.OwnerID + ">"
	if category.PingStaff && ticket.Status != types.TicketStatusClosed {
		for _, roleID := range staffRoleIDs(panel, category) {
			ping += " <@&" + roleID + ">"
		}
	}
	buttons := ticketWelcomeButtons(ticket, panel, false)
	message, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:         ping,
		Embeds:          []*discordgo.MessageEmbed{embed},
		Components:      buttons,
		AllowedMentions: ticketMentions(ticket, panel, category),
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

	PostTicketLog(s, panel, TicketLogMessage(ColorSuccess, "Ticket Opened", "A new ticket was opened.", []string{
		"- **Thread:** <#" + ticket.ThreadID + ">",
		"- **Reference:** `#" + ticket.Ref + "`",
		"- **Category:** " + ticket.CategoryLabel,
		"- **Owner:** <@" + ticket.OwnerID + ">",
	}), nil)
	return ticket, nil
}

// ticketMentions limits pings to the ticket's owner and (when enabled) its staff roles.
func ticketMentions(ticket *types.Ticket, panel *types.InteractivePanel, category *types.TicketCategory) *discordgo.MessageAllowedMentions {
	mentions := &discordgo.MessageAllowedMentions{
		Parse: []discordgo.AllowedMentionType{},
		Users: []string{ticket.OwnerID},
	}
	if category.PingStaff {
		mentions.Roles = staffRoleIDs(panel, category)
	}
	return mentions
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
	embed := TicketWelcomeMessage(ticket, panel, category, GuildDisplayName(s, guildID), false)
	buttons := ticketWelcomeButtons(ticket, panel, false)
	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:              ticket.WelcomeMessageID,
		Channel:         ticket.ThreadID,
		Embeds:          &[]*discordgo.MessageEmbed{embed},
		Components:      &buttons,
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
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

	settings := types.TicketSettings{}
	if panel != nil && panel.Ticket != nil {
		settings = panel.Ticket.Settings
	}

	transcriptName := fmt.Sprintf("%s.html", strings.ToLower(ticket.OwnerName))
	transcript := BuildTicketTranscript(s, ticket)
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
func BuildTicketTranscript(s *discordgo.Session, ticket *types.Ticket) []byte {
	var messages []*discordgo.Message
	beforeID := ""
	for len(messages) < 5000 {
		batch, err := s.ChannelMessages(ticket.ThreadID, 100, beforeID, "", "")
		if err != nil || len(batch) == 0 {
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
	return page.Bytes()
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

// ParseHexColor reads `#55FFFF`, `55FFFF` or `0x55FFFF`.
func ParseHexColor(raw string) (int, bool) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "#"), "0x")
	if len(raw) != 6 {
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 16, 32)
	if err != nil {
		return 0, false
	}
	return int(value), true
}

// ParseButtonStyle reads a colour name into a button style.
func ParseButtonStyle(raw string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "grey", "gray", "secondary":
		return int(discordgo.SecondaryButton), true
	case "blue", "blurple", "primary":
		return int(discordgo.PrimaryButton), true
	case "green", "success":
		return int(discordgo.SuccessButton), true
	case "red", "danger":
		return int(discordgo.DangerButton), true
	}
	return 0, false
}

// ButtonStyleName is the inverse of ParseButtonStyle, for pre-filling modals.
func ButtonStyleName(style int) string {
	switch discordgo.ButtonStyle(style) {
	case discordgo.PrimaryButton:
		return "blue"
	case discordgo.SuccessButton:
		return "green"
	case discordgo.DangerButton:
		return "red"
	}
	return "grey"
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

// staffRoleIDs merges the panel-wide and category staff roles without duplicates.
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
	if roles := staffRoleIDs(panel, category); len(roles) > 0 {
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

// TicketPanelMessage renders the public panel members open tickets from. With
// preview set the buttons are disabled, for the builder's live preview.
//
// The panel is a list of blocks (look.Layout): the built-in ones show the panel's fields, the others are free text,
// lines, galleries, sections and link buttons. A panel without a layout has the standard one.
func TicketPanelMessage(panel *types.InteractivePanel, preview bool) []discordgo.MessageComponent {
	look := panel.Appearance
	color := look.Color
	if color == 0 {
		color = defaultPanelColor
	}

	layout := look.Layout
	if len(layout) == 0 {
		layout = StandardPanelLayout(panel)
	} else if len(panel.Ticket.Categories) > 0 && !layoutHas(layout, "buttons") {
		layout = append(append([]types.PanelBlock{}, layout...), types.PanelBlock{Type: "buttons"}) // members could not open tickets otherwise
	}

	var inner []discordgo.MessageComponent
	for _, block := range layout {
		inner = append(inner, panelBlockComponents(panel, block, preview)...)
	}
	if look.NoColor { // a container without an accent has no bar
		return []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	}
	return []discordgo.MessageComponent{v2Container(color, inner...)}
}

func ticketPanelSend(panel *types.InteractivePanel, preview bool) *discordgo.MessageSend {
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
			Components:      ticketPanelButtons(panel, preview),
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
			Components:      ticketPanelButtons(panel, preview),
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
	default:
		return &discordgo.MessageSend{
			Flags:           discordgo.MessageFlagsIsComponentsV2,
			Components:      TicketPanelMessage(panel, preview),
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
	}
}

func ticketPanelButtons(panel *types.InteractivePanel, preview bool) []discordgo.MessageComponent {
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
		button.Disabled = preview
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
func panelBlockComponents(panel *types.InteractivePanel, b types.PanelBlock, preview bool) []discordgo.MessageComponent {
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
				out = append(out, v2Text(text))
			}
		}
		return out
	case "footer":
		if look.Footer != "" {
			return one(v2Text(look.Footer))
		}
	case "buttons":
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
			button.Disabled = preview
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
		if b.ThumbnailURL == "" { // a section needs its image: without one it is plain text
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

// TicketWelcomeMessage renders the message posted inside a ticket: a ping line,
// the configurable welcome, the ticket details and the Close / Claim buttons.
// With preview set the buttons are disabled and no pings are included.
func TicketWelcomeMessage(ticket *types.Ticket, panel *types.InteractivePanel, category *types.TicketCategory, guildName string, preview bool) *discordgo.MessageEmbed {
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
	switch ticket.Status {
	case types.TicketStatusClaimed:
		return fmt.Sprintf("🟢 Claimed by <@%s>", ticket.ClaimedBy)
	case types.TicketStatusClosed:
		return "🔴 Closed"
	}
	return "🟡 Awaiting Staff"
}

func ticketWelcomeButtons(ticket *types.Ticket, panel *types.InteractivePanel, preview bool) []discordgo.MessageComponent {
	closeButton := discordgo.Button{
		Label:    "Close Ticket",
		Style:    discordgo.DangerButton,
		Emoji:    &discordgo.ComponentEmoji{Name: "🔒"},
		CustomID: TicketIDClose,
	}
	closeButton.Disabled = preview
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
		claim.Disabled = preview
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

// TicketLogMessage renders one entry of the ticket log channel (and the
// owner's DM). The transcript, when any, is attached to the message itself.
func TicketLogMessage(color int, title, description string, lines []string) *discordgo.MessageEmbed {
	body := description
	if len(lines) > 0 {
		body += "\n\n" + strings.Join(lines, "\n")
	}
	return &discordgo.MessageEmbed{Title: title, Description: body, Color: color}
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
	lines := []string{
		"- **Thread:** `" + ticket.ThreadName + "`",
		"- **Server:** `" + guildName + "`",
		"- **Closed By:** <@" + ticket.ClosedBy + ">",
	}
	return lines
}

// TicketLeaderboardMessage ranks the staff who have claimed the most tickets.
func TicketLeaderboardMessage(rows []TicketLeaderboardRow, viewerID string) *discordgo.MessageEmbed {
	if len(rows) == 0 {
		return &discordgo.MessageEmbed{
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
		Title: "Ticket Stats",
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
		fmt.Fprintf(&list, "**#%s** %s - <@%s> - closed <t:%d:R> by <@%s>%s\n", ticket.Ref, ticket.CategoryLabel, ticket.OwnerID, ticket.ClosedAt, ticket.ClosedBy, claimed)
	}
	description := list.String()
	if filter != "" {
		description = filter + "\n\n" + description
	}
	return &discordgo.MessageEmbed{Title: "Ticket Logs", Description: truncate(description, 4000), Color: ColorDefault}
}

// ============================================================================
// Builder Views
// ============================================================================

// TicketManagerScreen lists the guild's ticket panels.
func TicketManagerScreen(panels []*types.InteractivePanel) []discordgo.MessageComponent {
	intro := "Design the panels your members use to open tickets. Every panel has its own look, categories, welcome messages and settings."
	inner := []discordgo.MessageComponent{v2Text("## Ticket Panels\n" + intro), v2Divider(true, false)}

	if len(panels) == 0 {
		inner = append(inner, v2Text("No panels yet. Press **New Panel** to create your first one."))
	} else {
		var list strings.Builder
		options := make([]discordgo.SelectMenuOption, 0, len(panels))
		for _, panel := range panels {
			fmt.Fprintf(&list, "**%s** - %d categories - %s\n", panel.Name, len(panel.Ticket.Categories), panelSentLabel(panel))
			options = append(options, discordgo.SelectMenuOption{
				Label:       truncate(panel.Name, 100),
				Value:       panel.ID,
				Description: fmt.Sprintf("%d categories", len(panel.Ticket.Categories)),
			})
		}
		inner = append(inner, v2Text(list.String()), v2Row(discordgo.SelectMenu{
			CustomID:    TicketIDBuilder + ":select",
			Placeholder: "Select a panel to edit",
			Options:     options,
		}))
	}

	newButton := v2Button(TicketIDBuilder+":new", "New Panel", "➕", discordgo.SuccessButton)
	newButton.Disabled = len(panels) >= MaxPanelsPerGuild
	inner = append(inner, v2Row(newButton))
	return []discordgo.MessageComponent{v2Container(ColorDefault, inner...)}
}

func panelSentLabel(panel *types.InteractivePanel) string {
	if panel.ChannelID == "" || panel.MessageID == "" {
		return "not sent yet"
	}
	return "sent in <#" + panel.ChannelID + ">"
}

// TicketEditorScreen shows a live preview of the panel with its editing controls.
func TicketEditorScreen(panel *types.InteractivePanel, notice string) []discordgo.MessageComponent {
	id := func(action string) string { return fmt.Sprintf("%s:%s:%s", TicketIDPanelEdit, panel.ID, action) }
	categories := panel.Ticket.Categories

	addCategory := v2Button(id("addcat"), "Add Category", "➕", discordgo.SuccessButton)
	addCategory.Disabled = len(categories) >= MaxCategoriesPerPanel
	refresh := v2Button(id("refresh"), "Update Sent Panel", "🔄", discordgo.SecondaryButton)
	refresh.Disabled = panel.MessageID == ""

	editor := []discordgo.MessageComponent{
		v2Text(fmt.Sprintf("## Editing %s\n%s", panel.Name, panelSentLabel(panel))),
	}
	if notice != "" {
		editor = append(editor, v2Text(notice))
	}
	editor = append(editor, v2Divider(true, false),
		v2Row(
			v2Button(id("look"), "Edit Appearance", "🎨", discordgo.PrimaryButton),
			addCategory,
			v2Button(id("settings"), "Settings", "⚙️", discordgo.SecondaryButton),
		),
	)

	if len(categories) > 0 {
		options := make([]discordgo.SelectMenuOption, 0, len(categories))
		for _, category := range categories {
			description := category.Description
			if description == "" {
				description = "No description"
			}
			options = append(options, discordgo.SelectMenuOption{
				Label:       truncate(category.Label, 100),
				Value:       category.ID,
				Description: truncate(description, 100),
				Emoji:       parseComponentEmoji(category.Emoji),
			})
		}
		editor = append(editor, v2Row(discordgo.SelectMenu{
			CustomID:    id("cat"),
			Placeholder: "Edit a category",
			Options:     options,
		}))
	}

	one := 1
	editor = append(editor,
		v2Row(discordgo.SelectMenu{
			MenuType:     discordgo.ChannelSelectMenu,
			CustomID:     id("send"),
			Placeholder:  "Send this panel to a channel",
			MinValues:    &one,
			MaxValues:    1,
			ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews},
		}),
		v2Row(
			refresh,
			v2Button(id("delete"), "Delete Panel", "🗑️", discordgo.DangerButton),
			v2Button(id("back"), "Back", "", discordgo.SecondaryButton),
		),
	)

	preview := TicketPanelMessage(panel, true)
	return []discordgo.MessageComponent{
		v2Text("-# Live preview - this is how your panel will look"),
		preview[0],
		v2Container(ColorInfo, editor...),
	}
}

// TicketDeleteConfirmScreen asks before a panel is removed.
func TicketDeleteConfirmScreen(panel *types.InteractivePanel) []discordgo.MessageComponent {
	id := func(action string) string { return fmt.Sprintf("%s:%s:%s", TicketIDPanelEdit, panel.ID, action) }
	return []discordgo.MessageComponent{v2Container(ColorCaution,
		v2Text(fmt.Sprintf("### Delete %s?\nThis removes the panel and its configuration. Its sent message stops working, but open tickets stay open until they are closed.", panel.Name)),
		v2Divider(false, false),
		v2Row(
			v2Button(id("deleteyes"), "Yes, Delete", "🗑️", discordgo.DangerButton),
			v2Button(id("edit"), "Cancel", "", discordgo.SecondaryButton),
		),
	)}
}

// TicketCategoryScreen shows one category's settings and controls.
func TicketCategoryScreen(panel *types.InteractivePanel, category *types.TicketCategory, notice string) []discordgo.MessageComponent {
	id := func(action string) string {
		return fmt.Sprintf("%s:%s:%s:%s", TicketIDCategory, panel.ID, category.ID, action)
	}

	roles := "None (panel-wide staff roles apply)"
	if len(category.StaffRoleIDs) > 0 {
		mentions := make([]string, len(category.StaffRoleIDs))
		for idx, roleID := range category.StaffRoleIDs {
			mentions[idx] = "<@&" + roleID + ">"
		}
		roles = strings.Join(mentions, " ")
	}
	description := category.Description
	if description == "" {
		description = "None"
	}
	emoji := category.Emoji
	if emoji == "" {
		emoji = "None"
	}

	details := fmt.Sprintf(
		"## %s%s\n- **Description:** %s\n- **Emoji:** %s\n- **Button Colour:** %s\n- **Thread Name:** `%s`\n- **Staff Roles:** %s\n- **Ping Staff:** %s",
		emojiPrefix(category.Emoji), category.Label, description, emoji, ButtonStyleName(category.ButtonStyle),
		category.NameFormat, roles, onOff(category.PingStaff),
	)
	welcome := fmt.Sprintf(
		"**Welcome Message**\n- **Title:** %s\n- **Colour:** %s\n- **Body:**\n%s",
		valueOrNone(category.Welcome.Title), HexColor(category.Welcome.Color), quoteBlock(truncate(category.Welcome.Body, 600)),
	)

	inner := []discordgo.MessageComponent{v2Text(details)}
	if notice != "" {
		inner = append(inner, v2Text(notice))
	}
	zero := 0
	defaults := make([]discordgo.SelectMenuDefaultValue, len(category.StaffRoleIDs))
	for idx, roleID := range category.StaffRoleIDs {
		defaults[idx] = discordgo.SelectMenuDefaultValue{ID: roleID, Type: discordgo.SelectMenuDefaultValueRole}
	}
	inner = append(inner,
		v2Divider(true, false),
		v2Text(welcome),
		v2Text("-# Placeholders: {user} {username} {category} {reference} {number} {staff} {server}"),
		v2Divider(true, false),
		v2Row(
			v2Button(id("info"), "Edit Details", "✏️", discordgo.PrimaryButton),
			v2Button(id("welcome"), "Edit Welcome", "💬", discordgo.PrimaryButton),
			v2Button(id("preview"), "Preview Welcome", "👁️", discordgo.SecondaryButton),
			v2Button(id("ping"), "Toggle Staff Ping", "🔔", discordgo.SecondaryButton),
		),
		v2Row(discordgo.SelectMenu{
			MenuType:      discordgo.RoleSelectMenu,
			CustomID:      id("roles"),
			Placeholder:   "Staff roles for this category",
			MinValues:     &zero,
			MaxValues:     10,
			DefaultValues: defaults,
		}),
		v2Row(
			v2Button(id("delete"), "Delete Category", "🗑️", discordgo.DangerButton),
			v2Button(fmt.Sprintf("%s:%s:edit", TicketIDPanelEdit, panel.ID), "Back", "", discordgo.SecondaryButton),
		),
	)
	return []discordgo.MessageComponent{v2Container(ColorInfo, inner...)}
}

// TicketSettingsScreen shows the panel-wide ticket behaviour.
func TicketSettingsScreen(panel *types.InteractivePanel, notice string) []discordgo.MessageComponent {
	id := func(action string) string { return fmt.Sprintf("%s:%s:%s", TicketIDSettings, panel.ID, action) }
	settings := panel.Ticket.Settings

	threads := "The channel the panel is sent in"
	if settings.ThreadChannelID != "" {
		threads = "<#" + settings.ThreadChannelID + ">"
	}
	logs := "Not set - ticket logs are disabled"
	if settings.LogChannelID != "" {
		logs = "<#" + settings.LogChannelID + ">"
	}
	limit := "Unlimited"
	if settings.MaxOpenPerUser > 0 {
		limit = fmt.Sprintf("%d", settings.MaxOpenPerUser)
	}

	text := fmt.Sprintf(
		"## %s Settings\n- **Ticket Channel:** %s\n- **Log Channel:** %s\n- **Open Tickets Per Member:** %s\n- **Claiming:** %s\n- **DM Transcript To Owner:** %s\n- **Delete Thread On Close:** %s\n- **Owner Can Close:** %s",
		panel.Name, threads, logs, limit, onOff(settings.ClaimEnabled), onOff(settings.DMTranscript), onOff(settings.DeleteOnClose), onOff(settings.OwnerCanClose),
	)
	inner := []discordgo.MessageComponent{v2Text(text)}
	if notice != "" {
		inner = append(inner, v2Text(notice))
	}

	zero := 0
	textChannels := []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews}
	staffDefaults := make([]discordgo.SelectMenuDefaultValue, len(settings.StaffRoleIDs))
	for idx, roleID := range settings.StaffRoleIDs {
		staffDefaults[idx] = discordgo.SelectMenuDefaultValue{ID: roleID, Type: discordgo.SelectMenuDefaultValueRole}
	}

	inner = append(inner,
		v2Divider(true, false),
		v2Row(discordgo.SelectMenu{
			MenuType: discordgo.ChannelSelectMenu, CustomID: id("threads"), Placeholder: "Channel tickets are created in",
			MinValues: &zero, MaxValues: 1, ChannelTypes: textChannels, DefaultValues: channelDefault(settings.ThreadChannelID),
		}),
		v2Row(discordgo.SelectMenu{
			MenuType: discordgo.ChannelSelectMenu, CustomID: id("logs"), Placeholder: "Channel ticket logs are sent to",
			MinValues: &zero, MaxValues: 1, ChannelTypes: textChannels, DefaultValues: channelDefault(settings.LogChannelID),
		}),
		v2Row(discordgo.SelectMenu{
			MenuType: discordgo.RoleSelectMenu, CustomID: id("staff"), Placeholder: "Staff roles for every category",
			MinValues: &zero, MaxValues: 10, DefaultValues: staffDefaults,
		}),
		v2Row(
			v2Button(id("claim"), "Claiming", "✋", discordgo.SecondaryButton),
			v2Button(id("dm"), "DM Transcript", "📨", discordgo.SecondaryButton),
			v2Button(id("deleteclose"), "Delete On Close", "🗑️", discordgo.SecondaryButton),
			v2Button(id("owner"), "Owner Can Close", "🔒", discordgo.SecondaryButton),
			v2Button(id("limit"), "Ticket Limit", "🔢", discordgo.SecondaryButton),
		),
		v2Row(v2Button(fmt.Sprintf("%s:%s:edit", TicketIDPanelEdit, panel.ID), "Back", "", discordgo.SecondaryButton)),
	)
	return []discordgo.MessageComponent{v2Container(ColorInfo, inner...)}
}

func channelDefault(channelID string) []discordgo.SelectMenuDefaultValue {
	if channelID == "" {
		return nil
	}
	return []discordgo.SelectMenuDefaultValue{{ID: channelID, Type: discordgo.SelectMenuDefaultValueChannel}}
}

func onOff(enabled bool) string {
	if enabled {
		return "On"
	}
	return "Off"
}

func valueOrNone(text string) string {
	if strings.TrimSpace(text) == "" {
		return "None"
	}
	return text
}

func quoteBlock(text string) string {
	if strings.TrimSpace(text) == "" {
		return "> None"
	}
	lines := strings.Split(text, "\n")
	for idx, line := range lines {
		lines[idx] = "> " + line
	}
	return strings.Join(lines, "\n")
}

// ============================================================================
// Component Routing & Handlers
// ============================================================================

var (
	builderCustomEmoji = regexp.MustCompile(`^<a?:[A-Za-z0-9_]+:\d{17,20}>$`)
	builderASCIIText   = regexp.MustCompile(`[A-Za-z0-9]`)
)

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
	default:
		handleBuilderComponent(s, i, parts)
	}
	return true
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
	}))

	closerIsStaff := IsTicketStaff(i.Member, ctx.Panel, ctx.Category)
	if _, err := CloseTicket(s, i.GuildID, i.ChannelID, InteractionUserID(i), closerIsStaff); err != nil {
		_ = RespondEmbed(s, i, Preset("error", types.PresetOptions{
			Title:       "Unexpected Error",
			Description: "The ticket could not be closed. Please try again later or contact support.",
		}))
		return
	}
	_ = RespondEmbed(s, i, Preset("success", types.PresetOptions{
		Title:       "Ticket Closed",
		Description: "The transcript has been saved and this ticket is being closed.",
	}))
}

// handleBuilderComponent routes the panel builder's buttons and select menus.
func handleBuilderComponent(s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {
	if !InteractionIsAdmin(i) {
		_ = RespondEmbed(s, i, TicketPermissionDeniedEmbed("Only administrators can manage ticket panels."))
		return
	}

	switch parts[1] {
	case "b":
		if len(parts) == 3 {
			builderManager(s, i, parts[2])
		}
	case "e":
		if len(parts) == 4 {
			builderEditor(s, i, parts[2], parts[3])
		}
	case "c":
		if len(parts) == 5 {
			builderCategory(s, i, parts[2], parts[3], parts[4])
		}
	case "s":
		if len(parts) == 4 {
			builderSettings(s, i, parts[2], parts[3])
		}
	}
}

// selectedValues returns what the clicker picked in a select menu.
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

// mutatePanel applies fn to a stored panel and returns the saved result.
func mutatePanel(i *discordgo.InteractionCreate, panelID string, fn func(panel *types.InteractivePanel) error) (*types.InteractivePanel, error) {
	var updated *types.InteractivePanel
	err := Tickets.Update(InteractionUserID(i), i.GuildID, func(ip *types.InteractivePanels) error {
		panel := ip.Panels[panelID]
		if panel == nil || panel.Ticket == nil {
			return ErrTicketPanelNotFound
		}
		if err := fn(panel); err != nil {
			return err
		}
		panel.UpdatedAt = time.Now().Unix()
		updated = panel
		return nil
	})
	return updated, err
}

// showManager rewrites the message into the panel list.
func showManager(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ip, err := Tickets.Load(i.GuildID)
	if err != nil {
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return
	}
	_ = UpdateV2(s, i, TicketManagerScreen(SortedTicketPanels(ip))...)
}

// loadPanelOrManager loads a panel, sending the clicker back to the list if it is gone.
func loadPanelOrManager(s *discordgo.Session, i *discordgo.InteractionCreate, panelID string) *types.InteractivePanel {
	ip, err := Tickets.Load(i.GuildID)
	if err != nil {
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return nil
	}
	panel := ip.Panels[panelID]
	if panel == nil || panel.Ticket == nil {
		_ = UpdateV2(s, i, TicketManagerScreen(SortedTicketPanels(ip))...)
		return nil
	}
	return panel
}

func builderManager(s *discordgo.Session, i *discordgo.InteractionCreate, action string) {
	switch action {
	case "select":
		if panel := loadPanelOrManager(s, i, firstValue(i)); panel != nil {
			_ = UpdateV2(s, i, TicketEditorScreen(panel, "")...)
		}
	case "new":
		_ = RespondModal(s, i, TicketIDModal+":new", "New Ticket Panel",
			ModalTextInput("name", "Panel Name (only you see this)", "", "Support", false, true, 60),
			ModalTextInput("title", "Panel Title", "", "Support", false, false, 100),
		)
	}
}

func builderEditor(s *discordgo.Session, i *discordgo.InteractionCreate, panelID, action string) {
	panel := loadPanelOrManager(s, i, panelID)
	if panel == nil {
		return
	}
	edit := func(notice string) {
		_ = UpdateV2(s, i, TicketEditorScreen(panel, notice)...)
	}
	id := TicketIDModal + ":%s:" + panel.ID

	switch action {
	case "back":
		showManager(s, i)
	case "edit":
		edit("")
	case "look":
		look := panel.Appearance
		_ = RespondModal(s, i, fmt.Sprintf(id, "look"), "Panel Appearance",
			ModalTextInput("title", "Title", look.Title, "Support", false, false, 100),
			ModalTextInput("description", "Description", look.Description, "Shown under the title", true, false, 1500),
			ModalTextInput("footer", "Footer (markdown)", look.Footer, "Shown above the buttons", true, false, 800),
			ModalTextInput("color", "Accent Colour (hex)", HexColor(look.Color), "#55FFFF", false, false, 7),
			ModalTextInput("banner", "Banner Image URL", look.BannerURL, "https://...", false, false, 400),
		)
	case "addcat":
		_ = RespondModal(s, i, fmt.Sprintf(id, "cat")+":new", "Add Category", categoryModalRows(NewTicketCategory(""), true)...)
	case "cat":
		cat := FindTicketCategory(panel, firstValue(i))
		if cat == nil {
			edit("That category no longer exists.")
			return
		}
		_ = UpdateV2(s, i, TicketCategoryScreen(panel, cat, "")...)
	case "send":
		builderSendPanel(s, i, panel, firstValue(i))
	case "refresh":
		builderRefreshPanel(s, i, panel)
	case "settings":
		_ = UpdateV2(s, i, TicketSettingsScreen(panel, "")...)
	case "delete":
		_ = UpdateV2(s, i, TicketDeleteConfirmScreen(panel)...)
	case "deleteyes":
		builderDeletePanel(s, i, panel)
	}
}

// builderSendPanel posts the panel to a channel and remembers where it went.
func builderSendPanel(s *discordgo.Session, i *discordgo.InteractionCreate, panel *types.InteractivePanel, channelID string) {
	if len(panel.Ticket.Categories) == 0 {
		_ = UpdateV2(s, i, TicketEditorScreen(panel, "Add at least one category before sending the panel.")...)
		return
	}
	message, err := s.ChannelMessageSendComplex(channelID, ticketPanelSend(panel, false))
	if err != nil {
		_ = UpdateV2(s, i, TicketEditorScreen(panel, "Could not send the panel to <#"+channelID+">. Make sure I can view and send messages there.")...)
		return
	}

	// One panel, one message: the previous copy is removed so stale buttons don't linger.
	if panel.ChannelID != "" && panel.MessageID != "" {
		_ = s.ChannelMessageDelete(panel.ChannelID, panel.MessageID)
	}
	updated, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
		p.ChannelID, p.MessageID = channelID, message.ID
		return nil
	})
	if err != nil {
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return
	}
	_ = UpdateV2(s, i, TicketEditorScreen(updated, "Panel sent to <#"+channelID+">.")...)
}

// builderRefreshPanel rewrites the already-sent panel message with the latest design.
func builderRefreshPanel(s *discordgo.Session, i *discordgo.InteractionCreate, panel *types.InteractivePanel) {
	send := ticketPanelSend(panel, false)
	existing, fetchErr := s.ChannelMessage(panel.ChannelID, panel.MessageID)
	if fetchErr == nil && (existing.Flags&discordgo.MessageFlagsIsComponentsV2 != 0) != (send.Flags&discordgo.MessageFlagsIsComponentsV2 != 0) {
		replacement, err := s.ChannelMessageSendComplex(panel.ChannelID, send)
		if err != nil {
			_ = UpdateV2(s, i, TicketEditorScreen(panel, "Could not replace the panel message with the selected format.")...)
			return
		}
		updated, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			p.MessageID = replacement.ID
			return nil
		})
		if err != nil {
			_ = s.ChannelMessageDelete(panel.ChannelID, replacement.ID)
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		_ = s.ChannelMessageDelete(panel.ChannelID, panel.MessageID)
		_ = UpdateV2(s, i, TicketEditorScreen(updated, "The panel format changed and the panel was reposted in <#"+panel.ChannelID+">.")...)
		return
	}
	content, embeds, components := send.Content, send.Embeds, send.Components
	_, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:              panel.MessageID,
		Channel:         panel.ChannelID,
		Content:         &content,
		Embeds:          &embeds,
		Components:      &components,
		Flags:           send.Flags,
		AllowedMentions: send.AllowedMentions,
	})
	if err != nil {
		_, _ = mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			p.ChannelID, p.MessageID = "", ""
			return nil
		})
		_ = UpdateV2(s, i, TicketEditorScreen(panel, "The sent panel message no longer exists. Send it to a channel again.")...)
		return
	}
	_ = UpdateV2(s, i, TicketEditorScreen(panel, "The sent panel in <#"+panel.ChannelID+"> was updated.")...)
}

// builderDeletePanel removes the panel and its sent message.
func builderDeletePanel(s *discordgo.Session, i *discordgo.InteractionCreate, panel *types.InteractivePanel) {
	if panel.ChannelID != "" && panel.MessageID != "" {
		_ = s.ChannelMessageDelete(panel.ChannelID, panel.MessageID)
	}
	if err := Tickets.Update(InteractionUserID(i), i.GuildID, func(ip *types.InteractivePanels) error {
		delete(ip.Panels, panel.ID)
		return nil
	}); err != nil {
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return
	}
	showManager(s, i)
}

func builderCategory(s *discordgo.Session, i *discordgo.InteractionCreate, panelID, categoryID, action string) {
	panel := loadPanelOrManager(s, i, panelID)
	if panel == nil {
		return
	}
	category := FindTicketCategory(panel, categoryID)
	if category == nil {
		_ = UpdateV2(s, i, TicketEditorScreen(panel, "That category no longer exists.")...)
		return
	}
	show := func(panel *types.InteractivePanel, notice string) {
		if updated := FindTicketCategory(panel, categoryID); updated != nil {
			_ = UpdateV2(s, i, TicketCategoryScreen(panel, updated, notice)...)
		}
	}
	modalID := func(kind string) string {
		return fmt.Sprintf("%s:%s:%s:%s", TicketIDModal, kind, panel.ID, category.ID)
	}

	switch action {
	case "info":
		_ = RespondModal(s, i, modalID("cat"), "Edit Category", categoryModalRows(category, false)...)
	case "welcome":
		welcome := category.Welcome
		_ = RespondModal(s, i, modalID("wel"), "Welcome Message",
			ModalTextInput("title", "Title", welcome.Title, "Ticket Created: {category}", false, false, 200),
			ModalTextInput("body", "Message", welcome.Body, "Hello {user}, ...", true, false, 1800),
			ModalTextInput("footer", "Footer", welcome.Footer, "Shown in small text at the bottom", false, false, 200),
			ModalTextInput("color", "Accent Colour (hex)", HexColor(welcome.Color), "#55FFFF", false, false, 7),
			ModalTextInput("thumbnail", "Thumbnail Image URL", welcome.ThumbnailURL, "https://...", false, false, 400),
		)
	case "preview":
		builderPreviewWelcome(s, i, panel, category)
	case "ping":
		updated, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			if target := FindTicketCategory(p, categoryID); target != nil {
				target.PingStaff = !target.PingStaff
			}
			return nil
		})
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		show(updated, "")
	case "roles":
		updated, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			if target := FindTicketCategory(p, categoryID); target != nil {
				target.StaffRoleIDs = append([]string{}, selectedValues(i)...)
			}
			return nil
		})
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		show(updated, "")
	case "delete":
		updated, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			kept := p.Ticket.Categories[:0]
			for _, existing := range p.Ticket.Categories {
				if existing.ID != categoryID {
					kept = append(kept, existing)
				}
			}
			p.Ticket.Categories = kept
			return nil
		})
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		_ = UpdateV2(s, i, TicketEditorScreen(updated, "Category deleted.")...)
	}
}

func builderSettings(s *discordgo.Session, i *discordgo.InteractionCreate, panelID, action string) {
	panel := loadPanelOrManager(s, i, panelID)
	if panel == nil {
		return
	}
	edit := func(notice string) {
		_ = UpdateV2(s, i, TicketSettingsScreen(panel, notice)...)
	}
	id := fmt.Sprintf("%s:%s:%s", TicketIDModal, action, panel.ID)

	switch action {
	case "edit":
		edit("")
	case "back":
		_ = UpdateV2(s, i, TicketEditorScreen(panel, "")...)
	case "threads":
		_, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			if p.Ticket != nil {
				p.Ticket.Settings.ThreadChannelID = firstValue(i)
			}
			return nil
		})
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		edit("")
	case "logs":
		_, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			if p.Ticket != nil {
				p.Ticket.Settings.LogChannelID = firstValue(i)
			}
			return nil
		})
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		edit("")
	case "staff":
		_, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			if p.Ticket != nil {
				p.Ticket.Settings.StaffRoleIDs = append([]string{}, selectedValues(i)...)
			}
			return nil
		})
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		edit("")
	case "claim":
		_, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			if p.Ticket != nil {
				p.Ticket.Settings.ClaimEnabled = !p.Ticket.Settings.ClaimEnabled
			}
			return nil
		})
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		edit("")
	case "dm":
		_, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			if p.Ticket != nil {
				p.Ticket.Settings.DMTranscript = !p.Ticket.Settings.DMTranscript
			}
			return nil
		})
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		edit("")
	case "deleteclose":
		_, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			if p.Ticket != nil {
				p.Ticket.Settings.DeleteOnClose = !p.Ticket.Settings.DeleteOnClose
			}
			return nil
		})
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		edit("")
	case "owner":
		_, err := mutatePanel(i, panel.ID, func(p *types.InteractivePanel) error {
			if p.Ticket != nil {
				p.Ticket.Settings.OwnerCanClose = !p.Ticket.Settings.OwnerCanClose
			}
			return nil
		})
		if err != nil {
			_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
			return
		}
		edit("")
	case "limit":
		_ = RespondModal(s, i, id, "Ticket Limit Per Member",
			ModalTextInput("limit", "Max tickets per member (0 for unlimited)", "", "0", false, true, 2),
		)
	}
}

func categoryModalRows(category *types.TicketCategory, isNew bool) []discordgo.MessageComponent {
	label, description, emoji, nameFormat := category.Label, category.Description, category.Emoji, category.NameFormat
	style := ButtonStyleName(category.ButtonStyle)
	if isNew {
		label, description, emoji = "", "", ""
	}
	return []discordgo.MessageComponent{
		ModalTextInput("label", "Button Label", label, "General Support", false, true, 80),
		ModalTextInput("emoji", "Emoji", emoji, "📌", false, false, 60),
		ModalTextInput("description", "Description (shown on the panel)", description, "For general questions and help.", false, false, 100),
		ModalTextInput("style", "Button Colour (blue, grey, green, red)", style, "grey", false, false, 10),
		ModalTextInput("name_format", "Thread Name", nameFormat, "{username} - Ticket: {category}", false, false, 100),
	}
}

func builderPreviewWelcome(s *discordgo.Session, i *discordgo.InteractionCreate, panel *types.InteractivePanel, category *types.TicketCategory) {
	ticket := &types.Ticket{
		OwnerID:       i.Member.User.ID,
		OwnerName:     i.Member.User.Username,
		Status:        types.TicketStatusOpen,
		Ref:           "ABC123",
		Number:        1,
		CategoryLabel: category.Label,
	}
	embed := TicketWelcomeMessage(ticket, panel, category, GuildDisplayName(s, i.GuildID), true)
	_ = RespondEmbed(s, i, embed)
}

// ============================================================================
// Response Helpers
// ============================================================================

// editComponentEmbed fills the deferred ephemeral reply of a button click.
func editComponentEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}})
}

// followupEmbed sends an ephemeral embed after the click was already acknowledged.
func followupEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Embeds: []*discordgo.MessageEmbed{embed},
		Flags:  discordgo.MessageFlagsEphemeral,
	})
}

// ============================================================================
// Embed Builders
// ============================================================================

func ticketCreatedEmbed(threadID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Ticket Created",
		Description: fmt.Sprintf("Your ticket was created: <#%s>. Continue the conversation there.", threadID),
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
		})
	case errors.Is(err, ErrTicketPanelNotFound), errors.Is(err, ErrTicketCategoryNotFound):
		return Preset("error", types.PresetOptions{
			Title:       "Panel Outdated",
			Description: "This panel is no longer available. Ask staff to send a fresh one.",
		})
	}
	return Preset("error", types.PresetOptions{
		Title:       "Ticket Not Created",
		Description: "Your ticket could not be created. The bot may be missing permission to create private threads in this channel. Please contact staff.",
	})
}

func TicketBotTargetEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be part of tickets. Select a member instead.",
	})
}

func TicketPermissionDeniedEmbed(reason string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Permission Denied",
		Description: reason,
	})
}

func ticketNotInChannelEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not A Ticket",
		Description: "This command must be run inside an open ticket thread.",
	})
}

func ticketPanelMissingEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Panel Not Found",
		Description: "The panel for this ticket no longer exists. Ask an administrator to check `/ticket_manage panel`.",
	})
}

func TicketClaimedEmbed(staffID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Ticket Claimed",
		Description: fmt.Sprintf("Successfully claimed this ticket.\n\n> **Claimed By:** <@%s>", staffID),
	})
}

func TicketMemberAddedEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Member Added",
		Description: fmt.Sprintf("Successfully added <@%s> to this ticket.", userID),
	})
}

func TicketMemberRemovedEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Member Removed",
		Description: fmt.Sprintf("Successfully removed <@%s> from this ticket.", userID),
	})
}

func TicketRemoveOwnerEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "The ticket owner cannot be removed from their own ticket. Close the ticket instead.",
	})
}

func UnexpectedErrorTicketEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred. Please try again later or contact support.",
	})
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

// TicketActionErrorEmbed explains why a claim or close could not be applied.
func TicketActionErrorEmbed(err error) *discordgo.MessageEmbed {
	switch {
	case errors.Is(err, ErrTicketNotFound):
		return ticketNotInChannelEmbed()
	case errors.Is(err, ErrTicketAlreadyClaimed):
		return Preset("error", types.PresetOptions{
			Title:       "Already Claimed",
			Description: "This ticket has already been claimed. Only the staff member who claimed it can release it.",
		})
	case errors.Is(err, ErrTicketNotClaimed):
		return Preset("error", types.PresetOptions{
			Title:       "Not Claimed",
			Description: "This ticket has not been claimed yet. Claim it first to take ownership.",
		})
	case errors.Is(err, ErrTicketClaimDisabled):
		return Preset("error", types.PresetOptions{
			Title:       "Claiming Disabled",
			Description: "Claiming is turned off for this panel. An administrator can enable it under the panel settings.",
		})
	}
	return UnexpectedErrorTicketEmbed()
}

// ============================================================================
// Modal Handling
// ============================================================================

// HandleTicketModal processes modal submissions from the panel builder.
func HandleTicketModal(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, TicketIDModal+":") {
		return false
	}
	if !InteractionIsAdmin(i) {
		_ = RespondEmbed(s, i, TicketPermissionDeniedEmbed("Only administrators can manage ticket panels."))
		return true
	}

	parts := strings.Split(customID, ":")
	if len(parts) < 3 {
		return true
	}
	values := ModalValues(i)

	switch parts[2] {
	case "new":
		modalNewPanel(s, i, values)
	case "look":
		if len(parts) == 4 {
			modalLook(s, i, parts[3], values)
		}
	case "cat":
		if len(parts) == 5 {
			modalCategory(s, i, parts[3], parts[4], values)
		}
	case "wel":
		if len(parts) == 5 {
			modalWelcome(s, i, parts[3], parts[4], values)
		}
	case "lim":
		if len(parts) == 4 {
			modalLimit(s, i, parts[3], values)
		}
	}
	return true
}

// modalError tells the admin what to fix without losing their place.
func modalError(s *discordgo.Session, i *discordgo.InteractionCreate, title, description string) {
	_ = RespondEmbed(s, i, Preset("error", types.PresetOptions{Title: title, Description: description}))
}

func modalNewPanel(s *discordgo.Session, i *discordgo.InteractionCreate, values map[string]string) {
	name := values["name"]
	if name == "" {
		modalError(s, i, "Name Required", "Give the panel a name so you can find it later.")
		return
	}

	panel := NewTicketPanel(name, values["title"], InteractionUserID(i))
	err := Tickets.Update(InteractionUserID(i), i.GuildID, func(ip *types.InteractivePanels) error {
		if len(SortedTicketPanels(ip)) >= MaxPanelsPerGuild {
			return errPanelLimit
		}
		ip.Panels[panel.ID] = panel
		return nil
	})
	switch {
	case err == errPanelLimit:
		modalError(s, i, "Panel Limit Reached", fmt.Sprintf("A server can have up to **%d** ticket panels. Delete one before creating another.", MaxPanelsPerGuild))
		return
	case err != nil:
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return
	}
	_ = UpdateV2(s, i, TicketEditorScreen(panel, "Panel created. Add a category, then send it to a channel.")...)
}

func modalLook(s *discordgo.Session, i *discordgo.InteractionCreate, panelID string, values map[string]string) {
	color := 0
	if raw := values["color"]; raw != "" {
		parsed, ok := ParseHexColor(raw)
		if !ok {
			modalError(s, i, "Invalid Colour", "Use a hex colour such as `#55FFFF`.")
			return
		}
		color = parsed
	}
	if banner := values["banner"]; banner != "" && !isHTTPURL(banner) {
		modalError(s, i, "Invalid Image", "The banner must be a direct `https://` image link.")
		return
	}

	updated, err := mutatePanel(i, panelID, func(p *types.InteractivePanel) error {
		p.Appearance.Title = values["title"]
		p.Appearance.Description = values["description"]
		p.Appearance.Footer = values["footer"]
		p.Appearance.Color = color
		p.Appearance.BannerURL = values["banner"]
		return nil
	})
	finishModal(s, i, updated, err, func(panel *types.InteractivePanel) []discordgo.MessageComponent {
		return TicketEditorScreen(panel, "Appearance saved. Use **Update Sent Panel** to apply it to the live message.")
	})
}

func modalCategory(s *discordgo.Session, i *discordgo.InteractionCreate, panelID, categoryID string, values map[string]string) {
	label := values["label"]
	if label == "" {
		modalError(s, i, "Label Required", "A category needs a label so members know what the button is for.")
		return
	}
	emoji := values["emoji"]
	if !validButtonEmoji(emoji) {
		modalError(s, i, "Invalid Emoji", "Use a single emoji, or a server emoji like `<:name:123456789012345678>`.")
		return
	}
	style, ok := ParseButtonStyle(values["style"])
	if !ok {
		modalError(s, i, "Invalid Button Colour", "Use `blue`, `grey`, `green`, or `red`.")
		return
	}
	nameFormat := values["name_format"]
	if nameFormat == "" {
		nameFormat = "{username} - Ticket: {category}"
	}

	var created *types.TicketCategory
	updated, err := mutatePanel(i, panelID, func(p *types.InteractivePanel) error {
		if categoryID == "new" {
			if len(p.Ticket.Categories) >= MaxCategoriesPerPanel {
				return errCategoryLimit
			}
			created = NewTicketCategory(label)
			p.Ticket.Categories = append(p.Ticket.Categories, created)
		} else {
			created = FindTicketCategory(p, categoryID)
			if created == nil {
				return ErrTicketCategoryNotFound
			}
		}
		created.Label = label
		created.Emoji = emoji
		created.Description = values["description"]
		created.ButtonStyle = style
		created.NameFormat = nameFormat
		return nil
	})
	if err == errCategoryLimit {
		modalError(s, i, "Category Limit Reached", fmt.Sprintf("A panel can have up to **%d** categories.", MaxCategoriesPerPanel))
		return
	}
	finishModal(s, i, updated, err, func(panel *types.InteractivePanel) []discordgo.MessageComponent {
		if categoryID == "new" {
			return TicketCategoryScreen(panel, created, "Category added. Set its staff roles and welcome message below.")
		}
		return TicketCategoryScreen(panel, created, "")
	})
}

func modalWelcome(s *discordgo.Session, i *discordgo.InteractionCreate, panelID, categoryID string, values map[string]string) {
	color := 0
	if raw := values["color"]; raw != "" {
		parsed, ok := ParseHexColor(raw)
		if !ok {
			modalError(s, i, "Invalid Colour", "Use a hex colour such as `#55FFFF`.")
			return
		}
		color = parsed
	}
	if thumbnail := values["thumbnail"]; thumbnail != "" && !isHTTPURL(thumbnail) {
		modalError(s, i, "Invalid Image", "The thumbnail must be a direct `https://` image link.")
		return
	}

	var edited *types.TicketCategory
	updated, err := mutatePanel(i, panelID, func(p *types.InteractivePanel) error {
		edited = FindTicketCategory(p, categoryID)
		if edited == nil {
			return ErrTicketCategoryNotFound
		}
		edited.Welcome.Title = values["title"]
		edited.Welcome.Body = values["body"]
		edited.Welcome.Footer = values["footer"]
		edited.Welcome.Color = color
		edited.Welcome.ThumbnailURL = values["thumbnail"]
		return nil
	})
	finishModal(s, i, updated, err, func(panel *types.InteractivePanel) []discordgo.MessageComponent {
		return TicketCategoryScreen(panel, edited, "Welcome message saved. Use **Preview Welcome** to see it.")
	})
}

func modalLimit(s *discordgo.Session, i *discordgo.InteractionCreate, panelID string, values map[string]string) {
	limit, err := strconv.Atoi(values["limit"])
	if err != nil || limit < 0 || limit > 25 {
		modalError(s, i, "Invalid Limit", "Enter a whole number from `0` (unlimited) to `25`.")
		return
	}
	updated, err := mutatePanel(i, panelID, func(p *types.InteractivePanel) error {
		p.Ticket.Settings.MaxOpenPerUser = limit
		return nil
	})
	finishModal(s, i, updated, err, func(panel *types.InteractivePanel) []discordgo.MessageComponent {
		return TicketSettingsScreen(panel, "")
	})
}

func finishModal(s *discordgo.Session, i *discordgo.InteractionCreate, panel *types.InteractivePanel, err error, screen func(*types.InteractivePanel) []discordgo.MessageComponent) {
	if err != nil {
		_ = RespondEmbed(s, i, UnexpectedErrorTicketEmbed())
		return
	}
	_ = UpdateV2(s, i, screen(panel)...)
}

// validButtonEmoji accepts no emoji, a server emoji, or a short unicode emoji.
func validButtonEmoji(emoji string) bool {
	if emoji == "" || builderCustomEmoji.MatchString(emoji) {
		return true
	}
	return len([]rune(emoji)) <= 8 && !builderASCIIText.MatchString(emoji) && !strings.ContainsAny(emoji, "<>: ")
}

func isHTTPURL(raw string) bool {
	return strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://")
}

type builderError string

func (e builderError) Error() string { return string(e) }

const (
	errPanelLimit    = builderError("panel limit reached")
	errCategoryLimit = builderError("category limit reached")
)

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
		category = NewTicketCategory(ticket.CategoryLabel)
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
	case discordgo.InteractionModalSubmit:
		HandleTicketModal(s, i, i.ModalSubmitData().CustomID)
	}
}
