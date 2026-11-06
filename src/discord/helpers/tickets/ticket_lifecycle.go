package tickets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers/guildlink"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/storage"
	"ranked-bedwars/src/types"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"
)

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
	mode := ticketMode(panel, category)

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
		parentID := category.ThreadChannelID
		if parentID == "" {
			parentID = panel.Ticket.Settings.ThreadChannelID
		}
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
		// The ticket could not be saved: without a record every ticket command in its channel answers "Not A Ticket", so the
		// channel is removed instead of being left behind (the member is told it failed and can open it again).
		_, _ = s.ChannelDelete(channelID)
		return nil, err
	}
	ticketActivityIndex.Lock()
	ticketActivityIndex.channels[ticketActivityKey(guildID, ticket.ThreadID)] = struct{}{}
	ticketActivityIndex.Unlock()

	PostTicketLog(s, panel, TicketLogMessage(helpers.ColorSuccess, "Ticket Opened", "A new ticket was opened.", []string{
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
	users := helpers.ValidMentionIDs(config.Users)
	roles := helpers.ValidMentionIDs(config.Roles)
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
		users = helpers.ValidMentionIDs(append([]string{ticket.OwnerID}, users...))
	}
	if !seen["roles"] && category.PingStaff {
		roles = helpers.ValidMentionIDs(append(pingRoleIDs(category), roles...))
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

	title, color := "Ticket Claimed", helpers.ColorDefault
	description := fmt.Sprintf("<@%s> claimed this ticket.", staffID)
	if !claim {
		title, color = "Ticket Unclaimed", helpers.ColorCaution
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
	if !storage.Files.Enabled() {
		log.Printf("[Tickets] JSON archive not saved for ticket %s in guild %s: object storage is not configured", threadID, guildID)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		archiveURL, archiveErr := storage.Files.PutJSON(ctx, fmt.Sprintf("tickets/%s/%s.json", guildID, threadID), archive)
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
	closedLog := TicketTranscriptMessage(helpers.ColorError, "### 📄 Ticket Closed",
		"This ticket has been closed. The conversation transcript is attached for your records.", lines, transcriptName)
	PostTicketLogV2(s, panel, closedLog, []*discordgo.File{file()})

	if settings.DMTranscript {
		if dm, err := s.UserChannelCreate(ticket.OwnerID); err == nil {
			_, _ = s.ChannelMessageSendComplex(dm.ID, &discordgo.MessageSend{
				Flags: discordgo.MessageFlagsIsComponentsV2,
				Components: TicketTranscriptMessage(helpers.ColorError, "### 📄 Your Ticket Was Closed",
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

// isUnknownChannel reports whether Discord says the channel (or thread) no longer exists.
func isUnknownChannel(err error) bool {
	var rest *discordgo.RESTError
	return errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownChannel
}

// closeMissingTicket closes a ticket whose channel was deleted: it moves to the history as closed (by "system", with the reason),
// and stops being tracked. There is no conversation left to build a transcript from or to tell anybody in, so that is all it does.
func closeMissingTicket(guildID, threadID string) {
	err := Tickets.Update(sdk.SystemActor, guildID, func(ip *types.InteractivePanels) error {
		found := ip.Tickets.Open[threadID]
		if found == nil {
			return nil
		}
		found.Status = types.TicketStatusClosed
		found.ClosedAt = time.Now().Unix()
		found.ClosedBy = "system"
		found.CloseReason = "The ticket channel no longer exists."
		appendTicketLog(found, "closed", "system", found.CloseReason, found.ClosedAt)
		ArchiveTicket(ip, found)
		return nil
	})
	removeTicketActivityChannel(guildID, threadID)
	if err != nil {
		log.Printf("[Tickets] could not close ticket %s in guild %s whose channel is gone: %v", threadID, guildID, err)
		return
	}
	log.Printf("[Tickets] closed ticket %s in guild %s: its channel no longer exists", threadID, guildID)
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
	ticketInactivityClose
)

func ticketInactivityActionFor(settings types.TicketSettings, ticket *types.Ticket, now time.Time) ticketInactivityAction {
	if ticket == nil || !settings.InactivityEnabled || !validInactivitySettings(settings) {
		return ticketInactivityNone
	}
	lastActive := ticketLastActivity(ticket)
	idle := now.Sub(time.Unix(lastActive, 0))
	noticed := ticket.LastInactivityNoticeAt > 0 && ticket.LastInactivityNoticeAt >= lastActive // a message since the last reply
	if settings.InactivityCloseEnabled {
		// The message goes out once; the ticket closes InactivityCloseHours after it if nobody has replied since.
		if noticed {
			if now.Sub(time.Unix(ticket.LastInactivityNoticeAt, 0)) >= time.Duration(settings.InactivityCloseHours)*time.Hour {
				return ticketInactivityClose
			}
			return ticketInactivityNone
		}
	}
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

// StartTicketInactivitySweeper checks configured open tickets once a minute. The first check is a minute after the bot starts, not
// at once: the guild state is still loading then, and reminders go out when they fall due.
func StartTicketInactivitySweeper(s *discordgo.Session) {
	ticketInactivitySweepOnce.Do(func() {
		go func() {
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
		if guild == nil || guildlink.IsSecondary(guild.ID) {
			continue // a linked secondary server has no tickets of its own
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
			if candidate == nil || (candidate.Status != types.TicketStatusOpen && candidate.Status != types.TicketStatusClaimed) {
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

			if _, err := s.Channel(candidate.ThreadID); isUnknownChannel(err) {
				closeMissingTicket(guild.ID, candidate.ThreadID)
				continue
			}

			notify, closing := false, action == ticketInactivityClose
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
				if ticketInactivityActionFor(currentSettings, ticket, now) != action {
					closing = false // somebody replied (or the settings changed) since the sweep looked
					return nil
				}
				if action == ticketInactivityRemind {
					ticket.LastInactivityNoticeAt = now.Unix()
					notify = true
				}
				candidate = copyTicket(ticket)
				panel = currentPanel
				settings = currentSettings
				return nil
			})
			if err != nil {
				log.Printf("[Tickets] could not schedule inactivity reminder for ticket %s in guild %s: %v", candidate.ThreadID, guild.ID, err)
				continue
			}
			if closing {
				_, _ = s.ChannelMessageSend(candidate.ThreadID, "Nobody replied in time, so this ticket is being closed for inactivity.")
				if _, err := closeTicket(s, guild.ID, candidate.ThreadID, inactivityCloser, false); err != nil {
					log.Printf("[Tickets] could not close inactive ticket %s in guild %s: %v", candidate.ThreadID, guild.ID, err)
				}
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
			if settings.InactivityCloseEnabled {
				vars["close_hours"] = strconv.Itoa(settings.InactivityCloseHours)
				vars["hours_until_close"] = vars["close_hours"]
			}
			message, err := sendInactivityReminder(s, guild.ID, candidate.ThreadID, candidate.OwnerID, settings, vars)
			if isUnknownChannel(err) {
				closeMissingTicket(guild.ID, candidate.ThreadID)
			} else if err != nil {
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
	if settings.InactivityCloseEnabled && (settings.InactivityCloseHours <= 0 || settings.InactivityCloseHours > maxInactivityHours) {
		return false
	}
	return settings.InactivityReminderHours > 0 && settings.InactivityReminderHours <= maxInactivityHours
}

// sendInactivityReminder posts the reminder into the ticket: the message built in the panel editor, else the saved Embed
// Builder message the panel points at (plain message, embeds or Components V2) when it still exists, else the panel's plain text. It returns the
// text kept in the ticket log. Nothing is closed or deleted.
func sendInactivityReminder(s *discordgo.Session, guildID, threadID, ownerID string, settings types.TicketSettings, vars map[string]string) (string, error) {
	fill := helpers.VarFiller(vars)
	var doc *helpers.MessageDoc
	label := "Inactivity reminder"
	if settings.InactivityReply != nil {
		doc = helpers.ReplyDoc(settings.InactivityReply)
	} else if id := strings.TrimSpace(settings.InactivityMessageID); id != "" {
		if doc = savedMessageDoc(guildID, id); doc != nil {
			label = "Inactivity reminder (" + doc.Name + ")"
		}
	}
	if doc != nil {
		payload := doc.Payload(fill)
		if !doc.AllowMentions && doc.AllowedMentions == nil {
			payload["allowed_mentions"] = map[string]any{"parse": []string{}, "users": []string{ownerID}}
		}
		_, err := helpers.SendPayload(s, threadID, payload)
		return label, err
	}
	text := fill(settings.InactivityMessage)
	_, err := s.ChannelMessageSendComplex(threadID, &discordgo.MessageSend{
		Content:         text,
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}, Users: []string{ownerID}},
	})
	return text, err
}

// savedMessageDoc is the Embed Builder message with the id, or nil when it is gone.
func savedMessageDoc(guildID, id string) *helpers.MessageDoc {
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if err != nil || cfg.Panels.IsEmpty() {
		return nil
	}
	var sections struct {
		Messages []helpers.MessageDoc `json:"messages"`
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
