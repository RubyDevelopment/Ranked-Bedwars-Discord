package screenshare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk/base"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/utility"
	"github.com/bwmarrin/discordgo"

	guildmodel "github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models/guild"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/punish"
	"ranked-bedwars/src/discord/helpers/tickets"
	"ranked-bedwars/src/types"
)

// Screenshare tickets: a private channel per request where a screensharer works with the player, plus the review step for
// players with the SS Restricted role. The commands (screenshare, ssclose, sshistory) and the buttons share this file.

// ============================================================================
// Constants & Configuration
// ============================================================================

const (
	// Command is the command whose requirement slots (category, roles, channels) every screenshare feature reads.
	Command = "screenshare"

	KeyCategory     = "screenshare_category"
	KeyScreensharer = "screensharer_role"
	KeyRestricted   = "ss_restricted_role"
	KeyLogs         = "screenshare_logs"
	KeyReviews      = "ss_restricted_reviews"

	// TicketTimeout is how long a ticket waits for a screensharer before it expires.
	TicketTimeout = 10 * time.Minute
	// restrictionCap ends the queue ban on its own if the ticket is never closed.
	restrictionCap = 12 * time.Hour

	idPrefix = "ss:" // ss:claim:<id>, ss:close:<id>, ss:approve:<id>, ss:deny:<id>

	eventType = "screenshare"
)

// Requirements are the slots the dashboard asks an admin to assign for the screenshare commands.
var Requirements = []types.Requirement{
	{Type: types.ReqCategory, Key: KeyCategory, Label: "Screenshare Category"},
	{Type: types.ReqRole, Key: KeyScreensharer, Label: "Screensharer Role", Grants: true},
	{Type: types.ReqRole, Key: KeyRestricted, Label: "SS Restricted Role", Optional: true, Grants: true},
	{Type: types.ReqChannel, Key: KeyLogs, Label: "Screenshare Logs Channel", Optional: true},
	{Type: types.ReqChannel, Key: KeyReviews, Label: "SS Restricted Reviews Channel", Optional: true},
}

var (
	ErrNoCategory = errors.New("the screenshare category is not set")
	ErrNotOpen    = errors.New("the ticket is not open")
	ErrNotFound   = errors.New("no screenshare ticket here")
)

func slotRole(guildID, key string) string { return helpers.SettingRole(guildID, Command, key) }

func slotChannel(guildID string, reqType types.RequirementType, key string) string {
	id, _ := helpers.RequirementSlotID(guildID, Command, reqType, key)
	return id
}

// ============================================================================
// Store
// ============================================================================

// Requests is every screenshare ticket and review. The database API keeps guild events as an append-only log, so each change
// is written as a fresh snapshot and the newest snapshot of an ID wins; the guild's snapshots are read once and kept here.
var Requests = &store{guilds: map[string]*guildState{}}

type store struct {
	mu     sync.Mutex
	guilds map[string]*guildState
}

type guildState struct {
	reqs map[int]*types.ScreenshareRequest
	next int
}

func (st *store) state(guildID string) (*guildState, error) {
	if g, ok := st.guilds[guildID]; ok {
		return g, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	events, err := database.Must().Client().Guild.ListEvents(ctx, base.GuildSnowflake(guildID))
	if err != nil {
		return nil, err
	}
	sort.Slice(events, func(a, b int) bool { return events[a].ID < events[b].ID })
	g := &guildState{reqs: map[int]*types.ScreenshareRequest{}, next: 1}
	for _, e := range events {
		if e.EventType != eventType {
			continue
		}
		var r types.ScreenshareRequest
		if json.Unmarshal(e.Data, &r) != nil || r.ID == 0 {
			continue
		}
		g.reqs[r.ID] = &r
		g.next = max(g.next, r.ID+1)
	}
	st.guilds[guildID] = g
	return g, nil
}

func (st *store) persist(guildID string, r types.ScreenshareRequest) error {
	data, err := utility.NewJSON(r)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = database.Must().Client().Guild.CreateEvent(ctx, base.GuildSnowflake(guildID), &guildmodel.GuildEventsModel{EventType: eventType, Data: data})
	return err
}

// Create stores r as a new request and returns it with its ID.
func (st *store) Create(guildID string, r types.ScreenshareRequest) (types.ScreenshareRequest, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	g, err := st.state(guildID)
	if err != nil {
		return r, err
	}
	r.ID, r.CreatedAt = g.next, time.Now().Unix()
	if err := st.persist(guildID, r); err != nil {
		return r, err
	}
	g.next++
	g.reqs[r.ID] = &r
	return r, nil
}

// Change applies fn to the request and stores the result. fn returns false to leave it alone (say, it is no longer in the
// state the click expects), which reports changed as false: that is what makes a claim or a decision go to one click only.
func (st *store) Change(guildID string, id int, fn func(r *types.ScreenshareRequest) bool) (types.ScreenshareRequest, bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	g, err := st.state(guildID)
	if err != nil {
		return types.ScreenshareRequest{}, false, err
	}
	cur, ok := g.reqs[id]
	if !ok {
		return types.ScreenshareRequest{}, false, ErrNotFound
	}
	next := *cur
	if !fn(&next) {
		return *cur, false, nil
	}
	if err := st.persist(guildID, next); err != nil {
		return *cur, false, err
	}
	g.reqs[id] = &next
	return next, true, nil
}

// Get is the request with this ID.
func (st *store) Get(guildID string, id int) (types.ScreenshareRequest, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	g, err := st.state(guildID)
	if err != nil || g.reqs[id] == nil {
		return types.ScreenshareRequest{}, false
	}
	return *g.reqs[id], true
}

// find lists the guild's requests that match, newest first.
func (st *store) find(guildID string, match func(r *types.ScreenshareRequest) bool) []types.ScreenshareRequest {
	st.mu.Lock()
	defer st.mu.Unlock()
	g, err := st.state(guildID)
	if err != nil {
		return nil
	}
	var out []types.ScreenshareRequest
	for _, r := range g.reqs {
		if match(r) {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID > out[b].ID })
	return out
}

// ByTarget is every request made on the player, newest first.
func (st *store) ByTarget(guildID, targetID string) []types.ScreenshareRequest {
	return st.find(guildID, func(r *types.ScreenshareRequest) bool { return r.TargetID == targetID })
}

// ByChannel is the ticket whose channel this is.
func (st *store) ByChannel(guildID, channelID string) (types.ScreenshareRequest, bool) {
	found := st.find(guildID, func(r *types.ScreenshareRequest) bool {
		return r.Kind == types.ScreenshareKindTicket && r.ChannelID == channelID
	})
	if len(found) == 0 {
		return types.ScreenshareRequest{}, false
	}
	return found[0], true
}

// PendingReview is the open investigation request on the player, if there is one.
func (st *store) PendingReview(guildID, targetID string) (types.ScreenshareRequest, bool) {
	found := st.find(guildID, func(r *types.ScreenshareRequest) bool {
		return r.Kind == types.ScreenshareKindReview && r.Status == types.ScreensharePending && r.TargetID == targetID
	})
	if len(found) == 0 {
		return types.ScreenshareRequest{}, false
	}
	return found[0], true
}

type dueTicket struct {
	guildID string
	req     types.ScreenshareRequest
}

// due lists the loaded tickets nobody claimed in time.
func (st *store) due(now int64) []dueTicket {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []dueTicket
	for guildID, g := range st.guilds {
		for _, r := range g.reqs {
			if r.Kind == types.ScreenshareKindTicket && r.Status == types.ScreenshareActive && r.ExpiresAt > 0 && now > r.ExpiresAt {
				out = append(out, dueTicket{guildID, *r})
			}
		}
	}
	return out
}

// ============================================================================
// Evidence
// ============================================================================

var videoExtension = regexp.MustCompile(`(?i)\.(mp4|mov|webm|mkv|avi|m4v|wmv|flv)(?:$|[?#])`)

// IsVideo reports whether an uploaded file is a video, by its type, name or address.
func IsVideo(a *discordgo.MessageAttachment) bool {
	return strings.HasPrefix(strings.ToLower(a.ContentType), "video/") || videoExtension.MatchString(a.Filename) || videoExtension.MatchString(a.URL)
}

// ParseLink is the clean http(s) address in raw, or "" when it is not a usable link.
func ParseLink(raw string) string {
	value := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(raw), "<"), ">")
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.Contains(u.Hostname(), ".") {
		return ""
	}
	return u.String()
}

// RestrictedEvidence is the video a request on an SS Restricted player needs: an uploaded video, or a link from any site.
// problem says what is wrong when there is none.
func RestrictedEvidence(attachment *discordgo.MessageAttachment, link string) (evidenceURL, problem string) {
	switch {
	case attachment != nil:
		if !IsVideo(attachment) {
			return "", "The attached file is not a video. Upload a video file, or paste a Medal or YouTube link."
		}
		return attachment.URL, ""
	case strings.TrimSpace(link) != "":
		if u := ParseLink(link); u != "" {
			return u, ""
		}
		return "", "That evidence link is not a valid URL. Paste the full link, for example a Medal or YouTube URL."
	}
	return "", "This player is SS Restricted, so video evidence is required. Attach a video or provide a Medal or YouTube link."
}

var (
	timestampBreak = regexp.MustCompile(`(\d)\s*[-–—]\s*(\d)`)
	spaces         = regexp.MustCompile(`\s+`)
)

// NormalizeTimestamps cleans optional timestamps such as `0:42` or `0:42-1:15, 3:05`. problem is set when they hold no time.
func NormalizeTimestamps(raw string) (value, problem string) {
	value = strings.TrimSpace(spaces.ReplaceAllString(raw, " "))
	if value == "" {
		return "", ""
	}
	if !strings.ContainsAny(value, "0123456789") {
		return "", "Timestamps should look like `0:42` or `0:42-1:15` (separate several with commas)."
	}
	if r := []rune(value); len(r) > 200 {
		value = string(r[:200])
	}
	return timestampBreak.ReplaceAllString(value, "$1–$2"), ""
}

// ============================================================================
// Permissions
// ============================================================================

// CanHandle reports whether the member who clicked or ran the command is a screensharer (or an administrator).
func CanHandle(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if helpers.MemberIsAdministrator(s, i) {
		return true
	}
	role := slotRole(i.GuildID, KeyScreensharer)
	return role != "" && i.Member != nil && slices.Contains(i.Member.Roles, role)
}

// ============================================================================
// Tickets
// ============================================================================

// TicketParams is what a ticket is opened for.
type TicketParams struct {
	GuildID       string
	Target        *discordgo.User
	TargetName    string // their registered Minecraft username
	RequesterID   string
	Reason        string
	EvidenceURL   string
	EvidenceImage bool
	Timestamps    string
	ApprovedBy    string // set when the ticket comes from an approved SS Restricted review
}

// TicketReady reports whether a ticket can be opened right now (the screenshare category is set).
func TicketReady(guildID string) bool {
	return slotChannel(guildID, types.ReqCategory, KeyCategory) != ""
}

var channelNameUnsafe = regexp.MustCompile(`[^a-z0-9_-]+`)

// CreateTicket opens the private channel, posts the claimable request and queue bans the player while it is open.
func CreateTicket(s *discordgo.Session, p TicketParams) (types.ScreenshareRequest, error) {
	categoryID := slotChannel(p.GuildID, types.ReqCategory, KeyCategory)
	if categoryID == "" {
		return types.ScreenshareRequest{}, ErrNoCategory
	}
	roleID := slotRole(p.GuildID, KeyScreensharer)
	expires := time.Now().Add(TicketTimeout).Unix()

	access := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory)
	overwrites := []*discordgo.PermissionOverwrite{
		{ID: p.GuildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
		{ID: p.RequesterID, Type: discordgo.PermissionOverwriteTypeMember, Allow: access},
		{ID: p.Target.ID, Type: discordgo.PermissionOverwriteTypeMember, Allow: access},
	}
	// Only screensharers get explicit access; administrators see it through their permission. Other staff are left out.
	if roleID != "" {
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{ID: roleID, Type: discordgo.PermissionOverwriteTypeRole, Allow: access})
	}
	name := "ss-" + channelNameUnsafe.ReplaceAllString(strings.ToLower(p.Target.Username), "")
	channel, err := s.GuildChannelCreateComplex(p.GuildID, discordgo.GuildChannelCreateData{
		Name: name, Type: discordgo.ChannelTypeGuildText, ParentID: categoryID, PermissionOverwrites: overwrites,
	})
	if err != nil {
		return types.ScreenshareRequest{}, err
	}

	req := types.ScreenshareRequest{
		Kind: types.ScreenshareKindTicket, Status: types.ScreenshareActive, TargetID: p.Target.ID, TargetName: p.TargetName, RequesterID: p.RequesterID,
		Reason: p.Reason, EvidenceURL: p.EvidenceURL, EvidenceImage: p.EvidenceImage, Timestamps: p.Timestamps,
		ChannelID: channel.ID, ApprovedBy: p.ApprovedBy, ExpiresAt: expires,
	}
	content := "<@" + p.Target.ID + ">"
	mentions := &discordgo.MessageAllowedMentions{Users: []string{p.Target.ID}}
	if roleID != "" {
		content = "<@&" + roleID + "> " + content
		mentions.Roles = []string{roleID}
	}
	embed := requestEmbed(req)
	helpers.WithGuildFooterIconForGuild(s, p.GuildID, embed)
	message, err := s.ChannelMessageSendComplex(channel.ID, &discordgo.MessageSend{Content: content, Embeds: []*discordgo.MessageEmbed{embed}, AllowedMentions: mentions})
	if err != nil {
		_, _ = s.ChannelDelete(channel.ID)
		return types.ScreenshareRequest{}, err
	}
	req.MessageID = message.ID
	if req, err = Requests.Create(p.GuildID, req); err != nil {
		_, _ = s.ChannelDelete(channel.ID)
		return types.ScreenshareRequest{}, err
	}
	editMessage(s, req, nil, buttons(button("Claim", discordgo.SuccessButton, "claim", req.ID)))

	// The player cannot queue while the ticket is open; the ban is lifted when it closes or expires.
	staff := p.ApprovedBy
	if staff == "" {
		staff = p.RequesterID
	}
	applied, hadPrior := punish.ApplyScreenshareRestriction(s, p.GuildID, staff, p.Target, req.ID, p.Reason, time.Now().Add(restrictionCap))
	if changed, ok, err := Requests.Change(p.GuildID, req.ID, func(r *types.ScreenshareRequest) bool {
		r.RestrictionApplied, r.HadPriorRestriction = applied, hadPrior
		return true
	}); ok && err == nil {
		req = changed
	}

	go Log(s, p.GuildID, logEmbed("Screenshare Requested", fmt.Sprintf("<@%s> requested a screenshare on <@%s> in <#%s>.\n\n> **Reason:** %s", p.RequesterID, p.Target.ID, channel.ID, req.Reason), types.ScreenshareActive), channel.ID, req.ID)
	return req, nil
}

// Claim hands the ticket to the screensharer who clicked. changed is false when someone else claimed it first.
func Claim(s *discordgo.Session, guildID string, id int, staffID string) (req types.ScreenshareRequest, changed bool, err error) {
	req, changed, err = Requests.Change(guildID, id, func(r *types.ScreenshareRequest) bool {
		if r.Kind != types.ScreenshareKindTicket || r.Status != types.ScreenshareActive {
			return false
		}
		r.Status, r.ClaimedBy = types.ScreenshareClaimed, staffID
		return true
	})
	if !changed {
		return req, false, err
	}
	// The player who asked for it steps out once a screensharer has it.
	if req.RequesterID != req.TargetID && req.RequesterID != staffID {
		_ = s.ChannelPermissionDelete(req.ChannelID, req.RequesterID)
	}
	editMessage(s, req, []*discordgo.MessageEmbed{claimedEmbed(req)}, buttons(button("Close", discordgo.DangerButton, "close", req.ID)))
	go Log(s, guildID, logEmbed("Screenshare Claimed", fmt.Sprintf("<@%s> claimed the screenshare on <@%s> in <#%s>.", staffID, req.TargetID, req.ChannelID), types.ScreenshareClaimed), req.ChannelID, req.ID)
	return req, true, nil
}

// Close ends a claimed ticket: the queue ban is lifted, a transcript is logged and the channel goes away a moment later.
func Close(s *discordgo.Session, guildID string, id int, closerID string) (types.ScreenshareRequest, error) {
	req, changed, err := Requests.Change(guildID, id, func(r *types.ScreenshareRequest) bool {
		if r.Kind != types.ScreenshareKindTicket || r.Status != types.ScreenshareClaimed {
			return false
		}
		r.Status, r.ClosedAt, r.ClosedBy = types.ScreenshareClosed, time.Now().Unix(), closerID
		return true
	})
	if err != nil {
		return req, err
	}
	if !changed {
		return req, ErrNotOpen
	}
	release(s, guildID, req, closerID, fmt.Sprintf("Screenshare ticket #%d closed", id))
	editMessage(s, req, []*discordgo.MessageEmbed{closedEmbed(req)}, []discordgo.MessageComponent{})
	Log(s, guildID, logEmbed("Screenshare Closed", fmt.Sprintf("<@%s> closed the screenshare ticket for <@%s>.", closerID, req.TargetID), types.ScreenshareClosed), req.ChannelID, req.ID)
	deleteChannelLater(s, req.ChannelID, 5*time.Second)
	return req, nil
}

// expire ends a ticket nobody claimed in time.
func expire(s *discordgo.Session, guildID string, id int) {
	req, changed, err := Requests.Change(guildID, id, func(r *types.ScreenshareRequest) bool {
		if r.Kind != types.ScreenshareKindTicket || r.Status != types.ScreenshareActive {
			return false
		}
		r.Status = types.ScreenshareExpired
		return true
	})
	if err != nil || !changed {
		return
	}
	release(s, guildID, req, s.State.User.ID, fmt.Sprintf("Screenshare ticket #%d expired", id))
	editMessage(s, req, []*discordgo.MessageEmbed{expiredEmbed(req)}, []discordgo.MessageComponent{})
	Log(s, guildID, logEmbed("Screenshare Expired", fmt.Sprintf("The screenshare request on <@%s> by <@%s> expired without a screensharer.", req.TargetID, req.RequesterID), types.ScreenshareExpired), req.ChannelID, req.ID)
	deleteChannelLater(s, req.ChannelID, 15*time.Second)
}

// release lifts the queue ban the ticket added (one the player already had is left alone).
func release(s *discordgo.Session, guildID string, req types.ScreenshareRequest, actor, note string) {
	if req.RestrictionApplied && !req.HadPriorRestriction {
		punish.ReleaseScreenshareRestriction(s, guildID, actor, req.TargetID, req.ID, note)
	}
}

func deleteChannelLater(s *discordgo.Session, channelID string, after time.Duration) {
	time.AfterFunc(after, func() { _, _ = s.ChannelDelete(channelID) })
}

// ============================================================================
// SS Restricted reviews
// ============================================================================

// Review posts an investigation request on an SS Restricted player for a screensharer to approve or deny. channelID is where
// it goes when no review channel is set.
func Review(s *discordgo.Session, guildID, channelID string, p TicketParams) (types.ScreenshareRequest, string, error) {
	if channelID2 := slotChannel(guildID, types.ReqChannel, KeyReviews); channelID2 != "" {
		channelID = channelID2
	}
	req := types.ScreenshareRequest{
		Kind: types.ScreenshareKindReview, Status: types.ScreensharePending, TargetID: p.Target.ID, TargetName: p.TargetName, RequesterID: p.RequesterID,
		Reason: p.Reason, EvidenceURL: p.EvidenceURL, Timestamps: p.Timestamps, ChannelID: channelID,
	}
	roleID := slotRole(guildID, KeyScreensharer)
	content, mentions := "", &discordgo.MessageAllowedMentions{}
	if roleID != "" {
		content, mentions.Roles = "<@&"+roleID+">", []string{roleID}
	}
	embed := reviewEmbed(req)
	helpers.WithGuildFooterIconForGuild(s, guildID, embed)
	message, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: content, Embeds: []*discordgo.MessageEmbed{embed}, AllowedMentions: mentions})
	if err != nil {
		return req, channelID, err
	}
	req.MessageID = message.ID
	if req, err = Requests.Create(guildID, req); err != nil {
		_ = s.ChannelMessageDelete(channelID, message.ID)
		return req, channelID, err
	}
	editMessage(s, req, nil, buttons(button("Approve Investigation", discordgo.SuccessButton, "approve", req.ID), button("Deny Request", discordgo.DangerButton, "deny", req.ID)))
	return req, channelID, nil
}

// ============================================================================
// Buttons
// ============================================================================

func init() { helpers.RegisterComponentHandler(handleComponent) }

func button(label string, style discordgo.ButtonStyle, action string, id int) discordgo.Button {
	return discordgo.Button{Label: label, Style: style, CustomID: idPrefix + action + ":" + strconv.Itoa(id)}
}

func buttons(b ...discordgo.Button) []discordgo.MessageComponent {
	row := discordgo.ActionsRow{}
	for _, one := range b {
		row.Components = append(row.Components, one)
	}
	return []discordgo.MessageComponent{row}
}

// editMessage rewrites the request's message: embeds nil keeps them, components nil keeps the buttons.
func editMessage(s *discordgo.Session, req types.ScreenshareRequest, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) {
	edit := &discordgo.MessageEdit{ID: req.MessageID, Channel: req.ChannelID}
	if embeds != nil {
		edit.Embeds = &embeds
	}
	if components != nil {
		edit.Components = &components
	}
	_, _ = s.ChannelMessageEditComplex(edit)
}

func handleComponent(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, idPrefix) {
		return false
	}
	parts := strings.Split(customID, ":")
	id, err := strconv.Atoi(parts[len(parts)-1])
	if len(parts) != 3 || err != nil {
		return false
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
	go func() {
		switch parts[1] {
		case "claim":
			onClaim(s, i, id)
		case "close":
			onClose(s, i, id)
		case "approve":
			onDecide(s, i, id, true)
		case "deny":
			onDecide(s, i, id, false)
		}
	}()
	return true
}

func refuse(s *discordgo.Session, i *discordgo.InteractionCreate, title, text string) {
	helpers.FollowupEmbed(s, i, helpers.Preset("error", types.PresetOptions{Title: title, Description: text, Thumbnail: helpers.ThumbFlag}))
}

func refuseInactive(s *discordgo.Session, i *discordgo.InteractionCreate) {
	refuse(s, i, "Request Inactive", "This screenshare request is no longer open.")
}

func refuseNotScreensharer(s *discordgo.Session, i *discordgo.InteractionCreate) {
	refuse(s, i, "Not Eligible", "Only members with the screensharer role can do this.")
}

func onClaim(s *discordgo.Session, i *discordgo.InteractionCreate, id int) {
	req, ok := Requests.Get(i.GuildID, id)
	if !ok || req.Kind != types.ScreenshareKindTicket || req.Status != types.ScreenshareActive {
		refuseInactive(s, i)
		return
	}
	if !CanHandle(s, i) {
		refuseNotScreensharer(s, i)
		return
	}
	if time.Now().Unix() > req.ExpiresAt {
		expire(s, i.GuildID, id)
		refuseInactive(s, i)
		return
	}
	if _, changed, _ := Claim(s, i.GuildID, id, helpers.InteractionUserID(i)); !changed {
		refuseInactive(s, i)
	}
}

func onClose(s *discordgo.Session, i *discordgo.InteractionCreate, id int) {
	if !CanHandle(s, i) {
		refuseNotScreensharer(s, i)
		return
	}
	if _, err := Close(s, i.GuildID, id, helpers.InteractionUserID(i)); err != nil {
		refuseInactive(s, i)
	}
}

func onDecide(s *discordgo.Session, i *discordgo.InteractionCreate, id int, approve bool) {
	guildID, user := i.GuildID, helpers.InteractionUserID(i)
	req, ok := Requests.Get(guildID, id)
	if !ok || req.Kind != types.ScreenshareKindReview || req.Status != types.ScreensharePending {
		refuse(s, i, "Request Resolved", "Another screensharer already decided this request.")
		return
	}
	if !CanHandle(s, i) {
		refuse(s, i, "Not A Reviewer", "Only screensharers can approve or deny investigation requests.")
		return
	}
	if req.RequesterID == user && !helpers.MemberIsAdministrator(s, i) {
		refuse(s, i, "Own Request", "You cannot decide your own request. Another screensharer has to review it.")
		return
	}
	// Fail before the request is used up, so a setup problem does not burn it.
	if approve && !TicketReady(guildID) {
		refuse(s, i, "Screenshare Category Missing", "An administrator has to set the screenshare category before requests can be approved.")
		return
	}
	status := types.ScreenshareDenied
	if approve {
		status = types.ScreenshareApproved
	}
	req, changed, err := Requests.Change(guildID, id, func(r *types.ScreenshareRequest) bool {
		if r.Status != types.ScreensharePending {
			return false
		}
		r.Status, r.ReviewedBy = status, user
		return true
	})
	if err != nil || !changed {
		refuse(s, i, "Request Resolved", "Another screensharer already decided this request.")
		return
	}

	say := func(text string) {
		_, _ = s.ChannelMessageSendComplex(req.ChannelID, &discordgo.MessageSend{Content: text, AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{req.RequesterID}}})
	}
	if !approve {
		editMessage(s, req, []*discordgo.MessageEmbed{reviewEmbed(req)}, []discordgo.MessageComponent{})
		say(fmt.Sprintf("<@%s> your investigation request for <@%s> was denied by <@%s>.", req.RequesterID, req.TargetID, user))
		go Log(s, guildID, logEmbed("Investigation Denied", fmt.Sprintf("<@%s> denied the investigation request on <@%s> from <@%s>.\n\n> **Reason:** %s", user, req.TargetID, req.RequesterID, req.Reason), types.ScreenshareDenied), "", 0)
		return
	}

	target, err := s.User(req.TargetID)
	var ticket types.ScreenshareRequest
	if err == nil {
		ticket, err = CreateTicket(s, TicketParams{GuildID: guildID, Target: target, TargetName: req.TargetName, RequesterID: req.RequesterID, Reason: req.Reason, EvidenceURL: req.EvidenceURL, Timestamps: req.Timestamps, ApprovedBy: user})
	}
	if err != nil {
		editMessage(s, req, []*discordgo.MessageEmbed{reviewEmbed(req)}, []discordgo.MessageComponent{})
		say(fmt.Sprintf("Request approved by <@%s>, but the screenshare ticket could not be created. <@%s> will need to submit the request again.", user, req.RequesterID))
		return
	}
	req, _, _ = Requests.Change(guildID, id, func(r *types.ScreenshareRequest) bool { r.TicketChannelID = ticket.ChannelID; return true })
	editMessage(s, req, []*discordgo.MessageEmbed{reviewEmbed(req)}, []discordgo.MessageComponent{})
	say(fmt.Sprintf("<@%s> your investigation request was approved: <#%s>", req.RequesterID, ticket.ChannelID))
	go Log(s, guildID, logEmbed("Investigation Approved", fmt.Sprintf("<@%s> approved the investigation request on <@%s> from <@%s>.\n\n> **Reason:** %s", user, req.TargetID, req.RequesterID, req.Reason), types.ScreenshareApproved), "", 0)
}

// ============================================================================
// Expiry sweep
// ============================================================================

// StartSweeper expires tickets nobody claimed in time, including ones left open while the bot was down.
func StartSweeper(s *discordgo.Session) {
	go func() {
		for _, g := range append([]*discordgo.Guild(nil), s.State.Guilds...) {
			Requests.mu.Lock()
			_, _ = Requests.state(g.ID) // read each guild's tickets once; a guild with none costs one call
			Requests.mu.Unlock()
		}
		for range time.Tick(30 * time.Second) {
			for _, d := range Requests.due(time.Now().Unix()) {
				expire(s, d.guildID, d.req.ID)
			}
		}
	}()
}

// ============================================================================
// Log
// ============================================================================

// Log posts embed to the screenshare logs channel. With a ticket channel it attaches that channel's transcript, so call it
// before the channel is deleted.
func Log(s *discordgo.Session, guildID string, embed *discordgo.MessageEmbed, ticketChannelID string, ticketID int) {
	channelID := slotChannel(guildID, types.ReqChannel, KeyLogs)
	if channelID == "" {
		return
	}
	helpers.WithGuildFooterIconForGuild(s, guildID, embed)
	send := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}}
	if ticketChannelID != "" {
		ticket := &types.Ticket{ThreadID: ticketChannelID, ThreadName: "Screenshare #" + strconv.Itoa(ticketID), Ref: "SS-" + strconv.Itoa(ticketID), CategoryLabel: "Screenshare", CreatedAt: time.Now().Unix()}
		if page, _, _ := tickets.BuildTicketTranscript(s, ticket); len(page) > 0 {
			send.Files = []*discordgo.File{{Name: fmt.Sprintf("screenshare-%d.html", ticketID), ContentType: "text/html", Reader: strings.NewReader(string(page))}}
		}
	}
	_, _ = s.ChannelMessageSendComplex(channelID, send)
}

// ============================================================================
// Embeds
// ============================================================================

func logEmbed(title, description, status string) *discordgo.MessageEmbed {
	preset := map[string]string{types.ScreenshareActive: "caution", types.ScreenshareClaimed: "success", types.ScreenshareApproved: "success"}[status]
	if preset == "" {
		preset = "error"
	}
	return helpers.Preset(preset, types.PresetOptions{Title: title, Description: description})
}

func evidenceText(r types.ScreenshareRequest) string {
	if r.EvidenceURL == "" {
		return ""
	}
	return fmt.Sprintf("[Open the evidence](%s)", r.EvidenceURL)
}

func field(name, value string) *discordgo.MessageEmbedField {
	return &discordgo.MessageEmbedField{Name: name, Value: value, Inline: true}
}

// playerText is a player the way the strike request writes them: the mention with their Minecraft username beside it.
func playerText(id, name string) string {
	if name == "" {
		return "<@" + id + ">"
	}
	return "<@" + id + "> (`" + name + "`)"
}

func avatar(r types.ScreenshareRequest) string { return helpers.MinecraftBustAvatarURL(r.TargetName) }

func requestEmbed(r types.ScreenshareRequest) *discordgo.MessageEmbed {
	fields := []*discordgo.MessageEmbedField{field("Suspect", playerText(r.TargetID, r.TargetName)), field("Reporter", "<@"+r.RequesterID+">")}
	if r.ApprovedBy != "" {
		fields = append(fields, field("Approved By", "<@"+r.ApprovedBy+">"))
	}
	fields = append(fields, &discordgo.MessageEmbedField{Name: "Reason", Value: r.Reason})
	if r.Timestamps != "" {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Timestamps", Value: r.Timestamps})
	}
	if !r.EvidenceImage && r.EvidenceURL != "" {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Evidence", Value: evidenceText(r)})
	}
	opts := types.PresetOptions{
		Title:       "Screenshare Request",
		Description: fmt.Sprintf("A screenshare was requested. A screensharer will claim this ticket shortly, and the request expires <t:%d:R>.", r.ExpiresAt),
		Thumbnail:   avatar(r),
		Fields:      fields,
	}
	if r.EvidenceImage {
		opts.Image = r.EvidenceURL
	}
	return helpers.Preset("default", opts)
}

func claimedEmbed(r types.ScreenshareRequest) *discordgo.MessageEmbed {
	fields := []*discordgo.MessageEmbedField{field("Suspect", playerText(r.TargetID, r.TargetName)), field("Screensharer", "<@"+r.ClaimedBy+">"), &discordgo.MessageEmbedField{Name: "Reason", Value: r.Reason}}
	if r.EvidenceURL != "" {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Evidence", Value: evidenceText(r)})
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Screenshare Claimed",
		Description: fmt.Sprintf("<@%s> is handling this screenshare. Close the ticket with the button below or `/ssclose` when it is finished.", r.ClaimedBy),
		Thumbnail:   avatar(r),
		Fields:      fields,
	})
}

func closedEmbed(r types.ScreenshareRequest) *discordgo.MessageEmbed {
	return helpers.Preset("default", types.PresetOptions{
		Title:       "Screenshare Closed",
		Description: fmt.Sprintf("<@%s> closed the screenshare ticket for %s. This channel will be deleted in a few seconds.", r.ClosedBy, playerText(r.TargetID, r.TargetName)),
		Thumbnail:   avatar(r),
	})
}

func expiredEmbed(r types.ScreenshareRequest) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Screenshare Expired",
		Description: fmt.Sprintf("No screensharer claimed the request on %s in time. This channel will be deleted shortly.\n\n> **Reporter:** <@%s>\n> **Reason:** %s", playerText(r.TargetID, r.TargetName), r.RequesterID, r.Reason),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func reviewEmbed(r types.ScreenshareRequest) *discordgo.MessageEmbed {
	preset, state := "caution", "Waiting for a screensharer to approve or deny it."
	switch r.Status {
	case types.ScreenshareApproved:
		preset, state = "success", fmt.Sprintf("Approved by <@%s>.", r.ReviewedBy)
		if r.TicketChannelID != "" {
			state += fmt.Sprintf(" The ticket is <#%s>.", r.TicketChannelID)
		}
	case types.ScreenshareDenied:
		preset, state = "error", fmt.Sprintf("Denied by <@%s>.", r.ReviewedBy)
	}
	fields := []*discordgo.MessageEmbedField{field("Suspect", playerText(r.TargetID, r.TargetName)), field("Reporter", "<@"+r.RequesterID+">"), &discordgo.MessageEmbedField{Name: "Reason", Value: r.Reason}}
	if r.Timestamps != "" {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Timestamps", Value: r.Timestamps})
	}
	fields = append(fields, &discordgo.MessageEmbedField{Name: "Video Evidence", Value: evidenceText(r)})
	return helpers.Preset(preset, types.PresetOptions{
		Title:       "SS Restricted Investigation Request",
		Description: "This player is SS Restricted, so a screensharer has to review the video before a screenshare opens. " + state,
		Thumbnail:   avatar(r),
		Fields:      fields,
	})
}
