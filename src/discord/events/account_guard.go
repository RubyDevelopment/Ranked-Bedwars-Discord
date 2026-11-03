package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// The compromised-account guard: a custom event on "a message is sent" that only runs for a sender who looks like a hijacked or throwaway
// account (a new account, someone who has hardly written anything, a message with an image in a voice channel's chat...), and whose
// steps can delete that message, delete what the sender wrote in the last while in any channel, and time them out.
//
// The conditions are the event's `guard`; the steps are ordinary custom event actions (delete_message, delete_recent, timeout, message).
// The dashboard shows the same event on the Moderation > Compromised Accounts page and on the Events page.

// EventGuard is the "only when the sender looks like this" part of an event. Every condition that is set has to hold.
type EventGuard struct {
	ContentKinds      []string `json:"content_kinds"`       // text, image, attachment, link: any of these in the message (none: every message)
	ChannelTypes      []string `json:"channel_types"`       // text, voice, stage, announcement, thread, forum: any of these (none: every channel)
	ExcludeChannelIDs []string `json:"exclude_channel_ids"` // never in these channels
	MaxMessages       int      `json:"max_messages"`        // has sent fewer than this many messages in the server before this one (0: no limit)
	MaxAccountDays    int      `json:"max_account_days"`    // the Discord account is younger than this (0: no limit)
	MaxMemberHours    int      `json:"max_member_hours"`    // joined the server more recently than this (0: no limit)
	SkipRegistered    bool     `json:"skip_registered"`     // players registered with the bot are trusted
	SkipRoleIDs       []string `json:"skip_role_ids"`       // members with any of these roles are trusted
	SkipModerators    *bool    `json:"skip_moderators"`     // the owner, administrators and anyone who can manage messages are trusted (missing: on)
}

// guardInput is what the guard looks at: everything about the message and its sender, worked out beforehand so the decision itself is plain logic.
type guardInput struct {
	Kinds          map[string]bool // what the message holds: text, image, attachment, link
	ChannelID      string
	ChannelType    string
	Prior          int // messages the sender wrote here before this one
	AccountAge     time.Duration
	MemberAge      time.Duration
	MemberAgeKnown bool
	RoleIDs        []string
	Moderator      bool
	Registered     func() bool // asked last: it is a database call
}

// matches reports whether a message and its sender meet every condition of the guard.
func (g *EventGuard) matches(in guardInput) bool {
	if in.Moderator && (g.SkipModerators == nil || *g.SkipModerators) {
		return false
	}
	if slices.ContainsFunc(g.SkipRoleIDs, func(id string) bool { return slices.Contains(in.RoleIDs, id) }) {
		return false
	}
	if slices.Contains(g.ExcludeChannelIDs, in.ChannelID) {
		return false
	}
	if len(g.ChannelTypes) > 0 && !slices.Contains(g.ChannelTypes, in.ChannelType) {
		return false
	}
	if len(g.ContentKinds) > 0 && !slices.ContainsFunc(g.ContentKinds, func(kind string) bool { return in.Kinds[kind] }) {
		return false
	}
	if g.MaxMessages > 0 && in.Prior >= g.MaxMessages {
		return false
	}
	if g.MaxAccountDays > 0 && in.AccountAge >= time.Duration(g.MaxAccountDays)*24*time.Hour {
		return false
	}
	if g.MaxMemberHours > 0 && (!in.MemberAgeKnown || in.MemberAge >= time.Duration(g.MaxMemberHours)*time.Hour) {
		return false // how long they have been here is not known: nobody is acted on for a guess
	}
	if g.SkipRegistered && in.Registered != nil && in.Registered() {
		return false
	}
	return true
}

// ============================================================================
// What a message holds

var (
	imageName   = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|bmp|heic|avif)(\?|$)`)
	linkPattern = regexp.MustCompile(`(?i)(https?://|www\.|discord\.gg/|discord(app)?\.com/invite/)\S+`)
)

// messageKinds is what a message holds: text, a link or invite, any attachment, and an image (an attached picture or an embedded one).
func messageKinds(m *discordgo.Message) map[string]bool {
	kinds := map[string]bool{}
	if m == nil {
		return kinds
	}
	if strings.TrimSpace(m.Content) != "" {
		kinds["text"] = true
	}
	if linkPattern.MatchString(m.Content) {
		kinds["link"] = true
	}
	for _, a := range m.Attachments {
		kinds["attachment"] = true
		if strings.HasPrefix(strings.ToLower(a.ContentType), "image/") || imageName.MatchString(a.Filename) || a.Width > 0 {
			kinds["image"] = true
		}
	}
	for _, e := range m.Embeds {
		if e != nil && (e.Type == discordgo.EmbedTypeImage || e.Type == discordgo.EmbedTypeGifv || e.Image != nil) {
			kinds["image"] = true
		}
	}
	return kinds
}

// channelTypeName is the guard's name for a channel: a message in a voice channel's chat is "voice", in a thread of a forum "forum".
func channelTypeName(ch, parent *discordgo.Channel) string {
	switch ch.Type {
	case discordgo.ChannelTypeGuildVoice:
		return "voice"
	case discordgo.ChannelTypeGuildStageVoice:
		return "stage"
	case discordgo.ChannelTypeGuildNews:
		return "announcement"
	case discordgo.ChannelTypeGuildForum, discordgo.ChannelTypeGuildMedia:
		return "forum"
	case discordgo.ChannelTypeGuildPublicThread, discordgo.ChannelTypeGuildPrivateThread, discordgo.ChannelTypeGuildNewsThread:
		if parent != nil && (parent.Type == discordgo.ChannelTypeGuildForum || parent.Type == discordgo.ChannelTypeGuildMedia) {
			return "forum"
		}
		return "thread"
	}
	return "text"
}

func channelOf(s *discordgo.Session, id string) *discordgo.Channel {
	if id == "" {
		return nil
	}
	if ch, err := s.State.Channel(id); err == nil && ch != nil {
		return ch
	}
	if ch, err := s.Channel(id); err == nil {
		return ch
	}
	return nil
}

func channelTypeOf(s *discordgo.Session, channelID string) string {
	ch := channelOf(s, channelID)
	if ch == nil {
		return "text"
	}
	var parent *discordgo.Channel
	if ch.IsThread() {
		parent = channelOf(s, ch.ParentID)
	}
	return channelTypeName(ch, parent)
}

// isModerator: the owner, administrators and anyone who can manage messages or the server are never acted on by default.
func isModerator(s *discordgo.Session, guildID, channelID, userID string) bool {
	if g, err := s.State.Guild(guildID); err == nil && g != nil && g.OwnerID == userID {
		return true
	}
	perms, err := s.UserChannelPermissions(userID, channelID)
	if err != nil {
		return true // cannot tell: leave them alone
	}
	return perms&(discordgo.PermissionAdministrator|discordgo.PermissionManageMessages|discordgo.PermissionManageGuild) != 0
}

// isRegistered: a player registered with the bot. When the database cannot say, they count as registered: nobody is acted on for a guess.
func isRegistered(guildID, userID string) bool {
	_, err := database.Must().Player.Get(guildID, userID)
	return err == nil || !sdk.IsNotFound(err)
}

// guardAllows is whether the sender and message of an event pass the guard.
func guardAllows(s *discordgo.Session, guildID string, g *EventGuard, c types.EventCtx) bool {
	if c.User == nil || c.Message == nil {
		return false
	}
	in := guardInput{
		Kinds:       messageKinds(c.Message),
		ChannelID:   c.ChannelID,
		ChannelType: channelTypeOf(s, c.ChannelID),
		Prior:       c.MessagesBefore,
		AccountAge:  time.Since(helpers.AccountCreatedAt(c.User.ID)),
		Moderator:   isModerator(s, guildID, c.ChannelID, c.User.ID),
		Registered:  func() bool { return isRegistered(guildID, c.User.ID) },
	}
	if c.Member != nil {
		in.RoleIDs = c.Member.Roles
		if !c.Member.JoinedAt.IsZero() {
			in.MemberAge, in.MemberAgeKnown = time.Since(c.Member.JoinedAt), true
		}
	}
	return g.matches(in)
}

// ============================================================================
// How many messages a member has sent

// messageCounter counts the messages each member has sent in each server since the bot started keeping count. The numbers are kept in a
// file (data/ is not touched by a deploy), so they survive restarts. Discord does not tell a bot how many messages someone has sent, so
// a count can only start from when the bot began counting: pair "fewer than N messages" with how long the account or member has existed.
type messageCounter struct {
	mu    sync.Mutex
	n     map[string]int
	dirty bool
	path  string
	once  sync.Once
}

var messageCounts = &messageCounter{path: filepath.Join("data", "message_counts.json")}

func countKey(guildID, userID string) string { return guildID + "|" + userID }

func (c *messageCounter) start() {
	c.once.Do(func() {
		c.load()
		go func() {
			for range time.Tick(time.Minute) {
				if err := c.flush(); err != nil {
					logger.Warn(fmt.Sprintf("saving message counts: %v", err))
				}
			}
		}()
	})
}

func (c *messageCounter) load() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n = map[string]int{}
	if raw, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(raw, &c.n)
	}
}

// bump counts one more message from the member and returns how many they had sent before it.
func (c *messageCounter) bump(guildID, userID string) int {
	c.start()
	c.mu.Lock()
	defer c.mu.Unlock()
	key := countKey(guildID, userID)
	prior := c.n[key]
	c.n[key] = prior + 1
	c.dirty = true
	return prior
}

// flush writes the counts to the file when they changed.
func (c *messageCounter) flush() error {
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return nil
	}
	raw, err := json.Marshal(c.n)
	c.dirty = false
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// ============================================================================
// What a member wrote lately

type recentMsg struct {
	ChannelID string
	MessageID string
	At        time.Time
}

// recentLog remembers the last messages of each member, in every channel of the server, so the ones a scammer posted can be deleted
// together. It only knows what the bot saw since it started, up to recentKeep messages and recentFor of them.
type recentLog struct {
	mu sync.Mutex
	by map[string][]recentMsg
}

const (
	recentKeep = 100
	recentFor  = 24 * time.Hour
)

var recentMessages = &recentLog{by: map[string][]recentMsg{}}

func (r *recentLog) add(guildID, userID string, m recentMsg) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := countKey(guildID, userID)
	list := append(r.by[key], m)
	list = slices.DeleteFunc(list, func(x recentMsg) bool { return m.At.Sub(x.At) > recentFor })
	if len(list) > recentKeep {
		list = list[len(list)-recentKeep:]
	}
	r.by[key] = list
}

// latest is up to limit of the member's messages from the last `within`, newest first, from every channel or only channelID.
func (r *recentLog) latest(guildID, userID string, within time.Duration, limit int, channelID string, now time.Time) []recentMsg {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recentMsg
	list := r.by[countKey(guildID, userID)]
	for i := len(list) - 1; i >= 0 && len(out) < limit; i-- {
		if now.Sub(list[i].At) > within || (channelID != "" && list[i].ChannelID != channelID) {
			continue
		}
		out = append(out, list[i])
	}
	return out
}

func (r *recentLog) forget(guildID, userID string, gone []recentMsg) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := countKey(guildID, userID)
	r.by[key] = slices.DeleteFunc(r.by[key], func(x recentMsg) bool {
		return slices.ContainsFunc(gone, func(g recentMsg) bool { return g.MessageID == x.MessageID })
	})
}

// bulkMaxAge: Discord refuses to bulk delete messages older than two weeks.
const bulkMaxAge = 13 * 24 * time.Hour

// planDeletes sorts messages into what can be bulk deleted (two or more in one channel, each newer than two weeks, a hundred at a time)
// and what has to be deleted one by one.
func planDeletes(items []recentMsg, now time.Time) (bulk map[string][][]string, single []recentMsg) {
	young := map[string][]string{}
	for _, m := range items {
		if now.Sub(m.At) < bulkMaxAge {
			young[m.ChannelID] = append(young[m.ChannelID], m.MessageID)
		} else {
			single = append(single, m)
		}
	}
	bulk = map[string][][]string{}
	for channel, ids := range young {
		for len(ids) > 0 {
			n := min(len(ids), 100)
			chunk := ids[:n]
			ids = ids[n:]
			if len(chunk) < 2 {
				single = append(single, recentMsg{ChannelID: channel, MessageID: chunk[0], At: now})
				continue
			}
			bulk[channel] = append(bulk[channel], chunk)
		}
	}
	return bulk, single
}

// deleteRecent deletes a member's last messages: the ones the guard saw in the last `within`, newest first, at most limit, in every
// channel or only the one the event happened in. It returns how many were deleted.
func deleteRecent(s *discordgo.Session, guildID, userID, channelID string, a CustomEventAction) int {
	limit := min(max(a.Count, 1), 100)
	within := time.Duration(min(max(a.WithinMinutes, 1), 1440)) * time.Minute
	scope := ""
	if a.Scope == "channel" {
		scope = channelID
	}
	now := time.Now()
	items := recentMessages.latest(guildID, userID, within, limit, scope, now)
	bulk, single := planDeletes(items, now)
	deleted := 0
	for channel, chunks := range bulk {
		for _, ids := range chunks {
			if err := s.ChannelMessagesBulkDelete(channel, ids); err != nil {
				logger.Warn(fmt.Sprintf("bulk delete in %s: %v", channel, err))
				continue
			}
			deleted += len(ids)
		}
	}
	for _, m := range single {
		if err := s.ChannelMessageDelete(m.ChannelID, m.MessageID); err != nil {
			continue // already gone
		}
		deleted++
	}
	recentMessages.forget(guildID, userID, items)
	return deleted
}

// timeoutMember mutes the member for a number of minutes (Discord allows up to 28 days).
func timeoutMember(s *discordgo.Session, guildID, userID string, minutes int) error {
	until := time.Now().Add(time.Duration(min(max(minutes, 1), 28*24*60)) * time.Minute)
	return s.GuildMemberTimeout(guildID, userID, &until)
}
