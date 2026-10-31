package events

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
	baseutility "ranked-bedwars/src/utility"
)

// message, DM the member, give or take a role. They live in the guild config's panels.custom_events, and their messages
// are the same documents the Embed Builder makes, so they are rendered by MessageDoc.Payload like every other message.
// The trigger names are the dashboard's (lib/custom-events.ts); the gateway handlers are further down in this file.

type CustomEventAction struct {
	Type        string              `json:"type"` // message, dm, add_role, remove_role
	ChannelID   string              `json:"channel_id"`
	SameChannel bool                `json:"same_channel"` // message: post in the channel where the event happened
	RoleID      string              `json:"role_id"`
	Message     *helpers.MessageDoc `json:"message"`
}

type CustomEvent struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Trigger         string              `json:"trigger"`
	Enabled         *bool               `json:"enabled"` // missing means on
	IncludeBots     bool                `json:"include_bots"`
	FilterChannelID string              `json:"filter_channel_id"` // only when it happened in this channel
	FilterRoleID    string              `json:"filter_role_id"`    // only for members with this role
	Actions         []CustomEventAction `json:"actions"`
}

const customEventsCacheTTL = 30 * time.Second

var (
	customEventsMu    sync.Mutex
	customEventsCache = map[string]struct {
		list []CustomEvent
		exp  time.Time
	}{}
)

// LoadCustomEvents reads the guild's events (cached briefly). A failed read counts as "none".
func LoadCustomEvents(guildID string) []CustomEvent {
	customEventsMu.Lock()
	defer customEventsMu.Unlock()
	if e, ok := customEventsCache[guildID]; ok && time.Now().Before(e.exp) {
		return e.list
	}
	raw, err := database.Must().GuildConfig.Field(guildID, "panels")
	if err != nil && !sdk.IsNotFound(err) {
		return nil
	}
	list := parseCustomEvents(raw)
	customEventsCache[guildID] = struct {
		list []CustomEvent
		exp  time.Time
	}{list, time.Now().Add(customEventsCacheTTL)}
	return list
}

func parseCustomEvents(panels json.RawMessage) []CustomEvent {
	var holder struct {
		Events []CustomEvent `json:"custom_events"`
	}
	_ = json.Unmarshal(panels, &holder)
	return holder.Events
}

// eventsFor are the enabled events that start on trigger and whose filters this happening passes.
func eventsFor(all []CustomEvent, trigger string, c types.EventCtx) []CustomEvent {
	var out []CustomEvent
	for _, e := range all {
		switch {
		case e.Trigger != trigger, e.Enabled != nil && !*e.Enabled:
		case c.User != nil && c.User.Bot && !e.IncludeBots:
		case e.FilterChannelID != "" && e.FilterChannelID != c.ChannelID:
		case e.FilterRoleID != "" && (c.Member == nil || !hasRole(c.Member, e.FilterRoleID)):
		default:
			out = append(out, e)
		}
	}
	return out
}

func name(u *discordgo.User, m *discordgo.Member) string {
	switch {
	case m != nil && m.Nick != "":
		return m.Nick
	case u != nil && u.GlobalName != "":
		return u.GlobalName
	case u != nil && u.Username != "":
		return u.Username
	}
	return "Unknown"
}

// eventVars are the {{variables}} an event's messages can use: the member, the guild, the time, the channel, then the event's own.
func eventVars(s *discordgo.Session, guildID string, ev CustomEvent, c types.EventCtx) map[string]string {
	v := map[string]string(helpers.GuildVars(s, guildID, nil))
	now := time.Now().UTC()
	v["time.date"], v["time.time"] = now.Format("2006-01-02"), now.Format("15:04")
	v["time.unix"], v["time.relative"] = fmt.Sprint(now.Unix()), fmt.Sprintf("<t:%d:R>", now.Unix())
	v["event.name"], v["event.reason"], v["event.count"] = ev.Name, "", ""
	v["member.count"] = v["guild.member_count"]

	if u := c.User; u != nil {
		v["member.name"], v["member.mention"], v["member.id"], v["member.tag"] = name(u, c.Member), u.Mention(), u.ID, u.Username
		v["member.avatar"] = helpers.AvatarOf(u)
		v["member.created"] = fmt.Sprintf("<t:%d:R>", helpers.AccountCreatedAt(u.ID).Unix())
		v["user.name"], v["user.mention"], v["user.avatar"] = v["member.name"], u.Mention(), v["member.avatar"]
		if c.Member != nil && !c.Member.JoinedAt.IsZero() {
			v["member.joined"] = fmt.Sprintf("<t:%d:R>", c.Member.JoinedAt.Unix())
		}
	} else {
		v["member.name"], v["member.mention"] = "Unknown", "Unknown"
	}
	if c.ChannelID != "" {
		v["channel.id"], v["channel.mention"] = c.ChannelID, "<#"+c.ChannelID+">"
		if ch, err := s.State.Channel(c.ChannelID); err == nil && ch != nil {
			v["channel.name"] = ch.Name
		}
	}
	for k, x := range c.Extra {
		v[k] = x
	}
	return v
}

// RunCustomEvents runs every event of the guild that starts on trigger. A failing step is logged and does not stop the
// ones after it. What the bot itself does never starts an event, so a step cannot set off its own event again.
func RunCustomEvents(s *discordgo.Session, guildID, trigger string, c types.EventCtx) {
	if c.User != nil && s.State != nil && s.State.User != nil && c.User.ID == s.State.User.ID {
		return
	}
	for _, ev := range eventsFor(LoadCustomEvents(guildID), trigger, c) {
		fill := helpers.VarFiller(eventVars(s, guildID, ev, c))
		for i, a := range ev.Actions {
			if err := runEventAction(s, guildID, c, a, fill); err != nil {
				logger.Warn(fmt.Sprintf("event %q step %d in %s: %v", ev.Name, i+1, guildID, err))
			}
		}
	}
}

func runEventAction(s *discordgo.Session, guildID string, c types.EventCtx, a CustomEventAction, fill func(string) string) error {
	switch a.Type {
	case "message":
		channel := a.ChannelID
		if a.SameChannel {
			channel = c.ChannelID
		}
		if a.Message == nil || channel == "" {
			return nil
		}
		_, err := helpers.SendPayload(s, channel, a.Message.Payload(fill))
		return err
	case "dm":
		if a.Message == nil || c.User == nil {
			return nil
		}
		ch, err := s.UserChannelCreate(c.User.ID)
		if err != nil {
			return err
		}
		_, err = helpers.SendPayload(s, ch.ID, a.Message.Payload(fill))
		return err
	case "add_role":
		if a.RoleID == "" || c.User == nil {
			return nil
		}
		markBotRoleChange(guildID, c.User.ID, a.RoleID, true)
		return s.GuildMemberRoleAdd(guildID, c.User.ID, a.RoleID)
	case "remove_role":
		if a.RoleID == "" || c.User == nil {
			return nil
		}
		markBotRoleChange(guildID, c.User.ID, a.RoleID, false)
		return s.GuildMemberRoleRemove(guildID, c.User.ID, a.RoleID)
	}
	return nil
}

// An event's own role changes must not start events (a "role given" event that gives a role would otherwise run
// forever, and so would two events that undo each other). The change is noted here before the bot makes it and the
// member-update handler asks BotChangedRole when it sees the change come back.
const botRoleChangeTTL = 30 * time.Second

var botRoleChanges sync.Map // "guild|user|role|added" -> time it stops counting

func botRoleKey(guildID, userID, roleID string, added bool) string {
	return fmt.Sprintf("%s|%s|%s|%t", guildID, userID, roleID, added)
}

func markBotRoleChange(guildID, userID, roleID string, added bool) {
	now := time.Now()
	botRoleChanges.Range(func(k, v any) bool {
		if now.After(v.(time.Time)) {
			botRoleChanges.Delete(k)
		}
		return true
	})
	botRoleChanges.Store(botRoleKey(guildID, userID, roleID, added), now.Add(botRoleChangeTTL))
}

// BotChangedRole reports whether the bot's own event just made this role change, and forgets it.
func BotChangedRole(guildID, userID, roleID string, added bool) bool {
	until, ok := botRoleChanges.LoadAndDelete(botRoleKey(guildID, userID, roleID, added))
	return ok && time.Now().Before(until.(time.Time))
}

// ── Gateway handlers ──
// The gateway side of the dashboard's Events page: one handler per kind of thing Discord reports, each turning it into a
// trigger name (lib/custom-events.ts) and the variables of that event, then handing it to RunCustomEvents. They
// are plumbing, not events a server switches on here (each custom event has its own switch).

type extra = map[string]string

func on(name string, execute any) {
	helpers.RegisterEvent(&types.Event{Name: "Custom Events: " + name, System: true, Type: "custom_events", Execute: execute})
}

func run(s *discordgo.Session, guildID, trigger string, c types.EventCtx) {
	if guildID != "" {
		RunCustomEvents(s, guildID, trigger, c)
	}
}

// who finds the person: the member the server holds, else just their id.
func who(s *discordgo.Session, guildID, userID string) (*discordgo.User, *discordgo.Member) {
	if m, err := s.State.Member(guildID, userID); err == nil && m != nil && m.User != nil {
		return m.User, m
	}
	return &discordgo.User{ID: userID}, nil
}

func withUser(m *discordgo.Member) *discordgo.User {
	if m == nil {
		return nil
	}
	return m.User
}

func roleVars(s *discordgo.Session, guildID, roleID string, role *discordgo.Role) extra {
	if role == nil {
		role, _ = s.State.Role(guildID, roleID)
	}
	x := extra{"role.id": roleID, "role.mention": "<@&" + roleID + ">", "role.name": "(deleted role)", "role.color": ""}
	if role != nil {
		x["role.name"], x["role.color"] = role.Name, fmt.Sprintf("#%06x", role.Color)
	}
	return x
}

func messageVars(guildID string, m *discordgo.Message, author *discordgo.User) extra {
	x := extra{"message.content": "(not known)", "message.id": "", "message.link": "", "message.author": "Unknown"}
	if m == nil {
		return x
	}
	x["message.content"], x["message.id"] = m.Content, m.ID
	x["message.link"] = fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, m.ChannelID, m.ID)
	if author != nil {
		x["message.author"] = author.Username
	}
	return x
}

func emojiText(e discordgo.Emoji) string {
	switch {
	case e.ID == "":
		return e.Name
	case e.Animated:
		return "<a:" + e.Name + ":" + e.ID + ">"
	}
	return "<:" + e.Name + ":" + e.ID + ">"
}

func untilText(t *time.Time) string {
	if t == nil {
		return ""
	}
	return fmt.Sprintf("<t:%d:R>", t.Unix())
}

func init() {
	// ── Members ──
	on("Member Join", func(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
		run(s, m.GuildID, "member_join", types.EventCtx{User: m.User, Member: m.Member})
	})
	on("Member Leave", func(s *discordgo.Session, m *discordgo.GuildMemberRemove) {
		run(s, m.GuildID, "member_leave", types.EventCtx{User: m.User, Member: m.Member})
	})
	on("Member Ban", func(s *discordgo.Session, m *discordgo.GuildBanAdd) {
		reason := ""
		if ban, err := s.GuildBan(m.GuildID, m.User.ID); err == nil && ban != nil {
			reason = ban.Reason
		}
		run(s, m.GuildID, "member_ban", types.EventCtx{User: m.User, Extra: extra{"event.reason": reason}})
	})
	on("Member Unban", func(s *discordgo.Session, m *discordgo.GuildBanRemove) {
		run(s, m.GuildID, "member_unban", types.EventCtx{User: m.User})
	})
	on("Member Update", memberUpdate)
	// Nickname, role, boost and timeout changes are found by comparing with the member as the bot saw them before, so
	// every member is loaded into the cache when a server becomes available.
	on("Member Cache", func(s *discordgo.Session, g *discordgo.GuildCreate) {
		_ = s.RequestGuildMembers(g.ID, "", 0, "", false)
	})

	// ── Messages ──
	on("Message Create", func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.GuildID == "" || m.Author == nil {
			return
		}
		member := m.Member
		if member != nil {
			member.User = m.Author
		}
		run(s, m.GuildID, "message_create", types.EventCtx{User: m.Author, Member: member, ChannelID: m.ChannelID, Extra: messageVars(m.GuildID, m.Message, m.Author)})
	})
	on("Message Edit", func(s *discordgo.Session, m *discordgo.MessageUpdate) {
		if m.GuildID == "" || m.Author == nil || m.EditedTimestamp == nil {
			return
		}
		old := "(not known)"
		if m.BeforeUpdate != nil {
			if m.BeforeUpdate.Content == m.Content {
				return // an embed unfurling, not an edit
			}
			old = m.BeforeUpdate.Content
		}
		x := messageVars(m.GuildID, m.Message, m.Author)
		x["change.old"], x["change.new"] = old, m.Content
		run(s, m.GuildID, "message_edit", types.EventCtx{User: m.Author, Member: m.Member, ChannelID: m.ChannelID, Extra: x})
	})
	on("Message Delete", func(s *discordgo.Session, m *discordgo.MessageDelete) {
		if m.GuildID == "" {
			return
		}
		msg := m.Message
		if m.BeforeDelete != nil {
			msg = m.BeforeDelete
		}
		var author *discordgo.User
		if msg != nil {
			author = msg.Author
		}
		run(s, m.GuildID, "message_delete", types.EventCtx{User: author, ChannelID: m.ChannelID, Extra: messageVars(m.GuildID, m.BeforeDelete, author)})
	})
	on("Message Bulk Delete", func(s *discordgo.Session, m *discordgo.MessageDeleteBulk) {
		run(s, m.GuildID, "message_bulk_delete", types.EventCtx{ChannelID: m.ChannelID, Extra: extra{"event.count": fmt.Sprint(len(m.Messages))}})
	})

	// ── Reactions ──
	on("Reaction Add", func(s *discordgo.Session, m *discordgo.MessageReactionAdd) {
		user, member := who(s, m.GuildID, m.UserID)
		if m.Member != nil {
			member, user = m.Member, withUser(m.Member)
			if user == nil {
				user = &discordgo.User{ID: m.UserID}
			}
		}
		x := messageVars(m.GuildID, &discordgo.Message{ID: m.MessageID, ChannelID: m.ChannelID}, nil)
		x["message.content"], x["reaction.emoji"] = "", emojiText(m.Emoji)
		run(s, m.GuildID, "reaction_add", types.EventCtx{User: user, Member: member, ChannelID: m.ChannelID, Extra: x})
	})
	on("Reaction Remove", func(s *discordgo.Session, m *discordgo.MessageReactionRemove) {
		user, _ := who(s, m.GuildID, m.UserID)
		x := messageVars(m.GuildID, &discordgo.Message{ID: m.MessageID, ChannelID: m.ChannelID}, nil)
		x["message.content"], x["reaction.emoji"] = "", emojiText(m.Emoji)
		run(s, m.GuildID, "reaction_remove", types.EventCtx{User: user, ChannelID: m.ChannelID, Extra: x})
	})

	// ── Channels ──
	on("Channel Create", func(s *discordgo.Session, m *discordgo.ChannelCreate) {
		if m.Channel == nil || m.IsThread() {
			return
		}
		run(s, m.GuildID, "channel_create", types.EventCtx{ChannelID: m.ID, Extra: extra{"channel.name": m.Name}})
	})
	on("Channel Delete", func(s *discordgo.Session, m *discordgo.ChannelDelete) {
		if m.Channel == nil || m.IsThread() {
			return
		}
		run(s, m.GuildID, "channel_delete", types.EventCtx{Extra: extra{"channel.name": m.Name, "channel.id": m.ID, "channel.mention": "#" + m.Name}})
	})
	on("Channel Update", func(s *discordgo.Session, m *discordgo.ChannelUpdate) {
		if m.Channel == nil || m.IsThread() {
			return
		}
		run(s, m.GuildID, "channel_update", types.EventCtx{ChannelID: m.ID, Extra: extra{"channel.name": m.Name}})
	})
	on("Thread Create", func(s *discordgo.Session, m *discordgo.ThreadCreate) {
		if m.Channel == nil || !m.NewlyCreated {
			return
		}
		run(s, m.GuildID, "thread_create", types.EventCtx{ChannelID: m.ID, Extra: extra{"channel.name": m.Name}})
	})
	on("Thread Delete", func(s *discordgo.Session, m *discordgo.ThreadDelete) {
		if m.Channel == nil {
			return
		}
		run(s, m.GuildID, "thread_delete", types.EventCtx{Extra: extra{"channel.name": m.Name, "channel.id": m.ID, "channel.mention": m.Name}})
	})

	// ── Roles ──
	on("Role Create", func(s *discordgo.Session, m *discordgo.GuildRoleCreate) {
		run(s, m.GuildID, "role_create", types.EventCtx{Extra: roleVars(s, m.GuildID, m.Role.ID, m.Role)})
	})
	on("Role Delete", func(s *discordgo.Session, m *discordgo.GuildRoleDelete) {
		run(s, m.GuildID, "role_delete", types.EventCtx{Extra: roleVars(s, m.GuildID, m.RoleID, nil)})
	})
	on("Role Update", func(s *discordgo.Session, m *discordgo.GuildRoleUpdate) {
		run(s, m.GuildID, "role_update", types.EventCtx{Extra: roleVars(s, m.GuildID, m.Role.ID, m.Role)})
	})

	// ── Voice ──
	on("Voice", func(s *discordgo.Session, m *discordgo.VoiceStateUpdate) {
		if m.VoiceState == nil || m.GuildID == "" {
			return
		}
		before := ""
		if m.BeforeUpdate != nil {
			before = m.BeforeUpdate.ChannelID
		}
		now := m.ChannelID
		if before == now {
			return // muted, deafened or streaming: not a move
		}
		user, member := who(s, m.GuildID, m.UserID)
		if m.Member != nil && m.Member.User != nil {
			user, member = m.Member.User, m.Member
		}
		x := extra{"voice.channel": "", "voice.channel_name": "", "voice.from": "", "voice.from_name": ""}
		put := func(key, id string) {
			if id == "" {
				return
			}
			x["voice."+key] = "<#" + id + ">"
			if ch, err := s.State.Channel(id); err == nil && ch != nil {
				x["voice."+key+"_name"] = ch.Name
			}
		}
		put("channel", now)
		put("from", before)
		switch {
		case before == "" && now != "":
			run(s, m.GuildID, "voice_join", types.EventCtx{User: user, Member: member, ChannelID: now, Extra: x})
		case before != "" && now == "":
			run(s, m.GuildID, "voice_leave", types.EventCtx{User: user, Member: member, ChannelID: before, Extra: x})
		case before != "" && now != "":
			run(s, m.GuildID, "voice_move", types.EventCtx{User: user, Member: member, ChannelID: now, Extra: x})
		}
	})

	// ── Invites ──
	on("Invite Create", func(s *discordgo.Session, m *discordgo.InviteCreate) {
		inviter := "Unknown"
		if m.Inviter != nil {
			inviter = m.Inviter.Username
		}
		run(s, m.GuildID, "invite_create", types.EventCtx{User: m.Inviter, ChannelID: m.ChannelID, Extra: extra{"invite.code": m.Code, "invite.url": "https://discord.gg/" + m.Code, "invite.inviter": inviter}})
	})
	on("Invite Delete", func(s *discordgo.Session, m *discordgo.InviteDelete) {
		run(s, m.GuildID, "invite_delete", types.EventCtx{ChannelID: m.ChannelID, Extra: extra{"invite.code": m.Code, "invite.url": "https://discord.gg/" + m.Code}})
	})

	// ── Scheduled events ──
	scheduled := func(e *discordgo.GuildScheduledEvent) extra {
		return extra{
			"scheduled.name": e.Name, "scheduled.description": e.Description,
			"scheduled.link":  fmt.Sprintf("https://discord.com/events/%s/%s", e.GuildID, e.ID),
			"scheduled.start": fmt.Sprintf("<t:%d:R>", e.ScheduledStartTime.Unix()),
		}
	}
	// The last status seen of each scheduled event, so an update that only edits the event is not taken for a start.
	var seenMu sync.Mutex
	seen := map[string]discordgo.GuildScheduledEventStatus{}
	remember := func(e *discordgo.GuildScheduledEvent) {
		seenMu.Lock()
		seen[e.ID] = e.Status
		seenMu.Unlock()
	}
	on("Scheduled Event Create", func(s *discordgo.Session, m *discordgo.GuildScheduledEventCreate) {
		remember(m.GuildScheduledEvent)
		run(s, m.GuildID, "scheduled_event_create", types.EventCtx{Extra: scheduled(m.GuildScheduledEvent)})
	})
	on("Scheduled Event Delete", func(s *discordgo.Session, m *discordgo.GuildScheduledEventDelete) {
		seenMu.Lock()
		delete(seen, m.ID)
		seenMu.Unlock()
		run(s, m.GuildID, "scheduled_event_delete", types.EventCtx{Extra: scheduled(m.GuildScheduledEvent)})
	})
	// On startup the events that already exist are noted too, so editing one that is running does not "start" it again.
	on("Scheduled Event Seed", func(s *discordgo.Session, g *discordgo.GuildCreate) {
		if list, err := s.GuildScheduledEvents(g.ID, false); err == nil {
			for _, e := range list {
				remember(e)
			}
		}
	})
	on("Scheduled Event Update", func(s *discordgo.Session, m *discordgo.GuildScheduledEventUpdate) {
		// An update also arrives for plain edits: only a change of status is a start or an end.
		seenMu.Lock()
		last, known := seen[m.ID]
		seen[m.ID] = m.Status
		seenMu.Unlock()
		if known && last == m.Status {
			return
		}
		switch m.Status {
		case discordgo.GuildScheduledEventStatusActive:
			run(s, m.GuildID, "scheduled_event_start", types.EventCtx{Extra: scheduled(m.GuildScheduledEvent)})
		case discordgo.GuildScheduledEventStatusCompleted:
			run(s, m.GuildID, "scheduled_event_end", types.EventCtx{Extra: scheduled(m.GuildScheduledEvent)})
		}
	})

	// ── Server ──
	on("Guild Update", func(s *discordgo.Session, m *discordgo.GuildUpdate) {
		if m.Guild != nil {
			run(s, m.ID, "guild_update", types.EventCtx{})
		}
	})
	on("AutoMod", func(s *discordgo.Session, m *discordgo.AutoModerationActionExecution) {
		user, member := who(s, m.GuildID, m.UserID)
		rule := "AutoMod"
		if r, err := s.AutoModerationRule(m.GuildID, m.RuleID); err == nil && r != nil {
			rule = r.Name
		}
		run(s, m.GuildID, "automod_action", types.EventCtx{User: user, Member: member, ChannelID: m.ChannelID, Extra: extra{"automod.rule": rule, "automod.content": m.Content, "automod.keyword": m.MatchedKeyword}})
	})
}

// memberUpdate turns what changed about a member into the member events: boosting, nickname, roles and timeouts. It needs
// the member as the bot saw it before (BeforeUpdate), so a change to a member the bot never saw is not reported.
func memberUpdate(s *discordgo.Session, m *discordgo.GuildMemberUpdate) {
	before := m.BeforeUpdate
	if before == nil || m.Member == nil {
		return
	}
	c := func(x extra) types.EventCtx { return types.EventCtx{User: m.User, Member: m.Member, Extra: x} }

	switch {
	case before.PremiumSince == nil && m.PremiumSince != nil:
		run(s, m.GuildID, "member_boost", c(nil))
	case before.PremiumSince != nil && m.PremiumSince == nil:
		run(s, m.GuildID, "member_unboost", c(nil))
	}
	if before.Nick != m.Nick {
		shown := func(n string) string {
			if n == "" {
				return "(none)"
			}
			return n
		}
		run(s, m.GuildID, "member_nickname", c(extra{"change.old": shown(before.Nick), "change.new": shown(m.Nick)}))
	}
	for _, id := range m.Roles {
		if !slices.Contains(before.Roles, id) && !BotChangedRole(m.GuildID, m.User.ID, id, true) {
			run(s, m.GuildID, "member_role_added", c(roleVars(s, m.GuildID, id, nil)))
		}
	}
	for _, id := range before.Roles {
		if !slices.Contains(m.Roles, id) && !BotChangedRole(m.GuildID, m.User.ID, id, false) {
			run(s, m.GuildID, "member_role_removed", c(roleVars(s, m.GuildID, id, nil)))
		}
	}
	now := time.Now()
	wasOut := before.CommunicationDisabledUntil != nil && before.CommunicationDisabledUntil.After(now)
	isOut := m.CommunicationDisabledUntil != nil && m.CommunicationDisabledUntil.After(now)
	switch {
	case !wasOut && isOut:
		run(s, m.GuildID, "member_timeout", c(extra{"timeout.until": untilText(m.CommunicationDisabledUntil)}))
	case wasOut && !isOut:
		run(s, m.GuildID, "member_timeout_end", c(nil))
	}
}

// ============================================================================
// Running a command
// ============================================================================

// Steps run in order; a failing or stopping step answers with its message and ends the command. Then the first
// reply whose condition holds (or the default reply) is sent with {{variables}} filled in.

type runner struct {
	s       *discordgo.Session
	i       *discordgo.InteractionCreate
	e       *entry
	name    string
	invoker *discordgo.User
	vars    map[string]string
	users   map[string]*discordgo.User // user and player options by option name

	// ephemeral is what the reply was deferred as: the called subcommand's default reply decides it before any step runs.
	ephemeral bool
}

func runCustomCommand(s *discordgo.Session, i *discordgo.InteractionCreate, name string) error {
	e := entries(i.GuildID)[name]
	if e == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("This command no longer exists."))
	}
	data := i.ApplicationCommandData()
	opts, sub := data.Options, ""
	if len(e.b.SubcommandGroups) > 0 {
		if len(opts) == 0 || len(opts[0].Options) == 0 {
			return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("Pick a subcommand to run."))
		}
		sub, opts = opts[0].Name+" "+opts[0].Options[0].Name, opts[0].Options[0].Options
	} else if len(e.b.Subcommands) > 0 {
		if len(opts) == 0 {
			return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("Pick a subcommand to run."))
		}
		sub, opts = opts[0].Name, opts[0].Options
	}
	b := e.b.bodyFor(sub)
	if b == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("That subcommand no longer exists."))
	}

	invoker := i.User
	if i.Member != nil && i.Member.User != nil {
		invoker = i.Member.User
	}
	r := &runner{s: s, i: i, e: e, name: name, invoker: invoker, users: map[string]*discordgo.User{}, ephemeral: b.Reply.Ephemeral}
	r.vars = r.baseVars(sub)
	r.readOptions(b, opts, data)

	for _, st := range b.Steps {
		if st.When != nil && !r.holds(*st.When) {
			continue
		}
		if msg, stopped := r.exec(st); stopped {
			return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed(r.fill(msg)))
		}
	}

	chosen := b.Reply
	for _, rule := range b.ReplyRules {
		if r.holds(rule.When) {
			chosen = rule.Reply
			break
		}
	}
	return r.send(chosen)
}

// ============================================================================
// Variables
// ============================================================================

func (r *runner) fill(text string) string { return helpers.FillVars(text, r.vars) }

func (r *runner) baseVars(sub string) map[string]string {
	v := map[string]string(helpers.GuildVars(r.s, r.i.GuildID, r.invoker))
	now := time.Now().UTC()
	v["time.date"], v["time.time"] = now.Format("2006-01-02"), now.Format("15:04")
	v["time.unix"], v["time.relative"] = itoa(int(now.Unix())), fmt.Sprintf("<t:%d:R>", now.Unix())
	v["channel.id"], v["channel.mention"] = r.i.ChannelID, "<#"+r.i.ChannelID+">"
	if ch, err := r.s.State.Channel(r.i.ChannelID); err == nil && ch != nil {
		v["channel.name"] = ch.Name
	}
	v["command.name"], v["command.subcommand"] = r.name, sub
	v["command.prefix"] = helpers.LoadGuildContext(r.i.GuildID).Settings.Prefix
	if r.invoker != nil {
		v["user.id"] = r.invoker.ID
	}
	return v
}

func (r *runner) readOptions(b *body, given []*discordgo.ApplicationCommandInteractionDataOption, data discordgo.ApplicationCommandInteractionData) {
	byName := map[string]*discordgo.ApplicationCommandInteractionDataOption{}
	for _, o := range given {
		byName[o.Name] = o
	}
	for _, def := range b.Options {
		o := byName[def.Name]
		key := "opt." + def.Name
		if o == nil {
			r.vars[key] = ""
			continue
		}
		switch o.Type {
		case discordgo.ApplicationCommandOptionUser, discordgo.ApplicationCommandOptionMentionable:
			u := o.UserValue(r.s)
			r.users[def.Name] = u
			r.vars[key], r.vars[key+".id"], r.vars[key+".mention"] = u.Username, u.ID, "<@"+u.ID+">"
			if def.Type == "player" {
				if view, err := database.Must().Player.Get(r.i.GuildID, u.ID); err == nil && view.Config != nil {
					r.vars[key] = view.Config.Username
				}
			}
		case discordgo.ApplicationCommandOptionRole:
			id := fmt.Sprint(o.Value)
			r.vars[key], r.vars[key+".id"], r.vars[key+".mention"] = "<@&"+id+">", id, "<@&"+id+">"
		case discordgo.ApplicationCommandOptionChannel:
			id := fmt.Sprint(o.Value)
			r.vars[key], r.vars[key+".id"], r.vars[key+".mention"] = "<#"+id+">", id, "<#"+id+">"
		case discordgo.ApplicationCommandOptionAttachment:
			id := fmt.Sprint(o.Value)
			if data.Resolved != nil && data.Resolved.Attachments[id] != nil {
				r.vars[key] = data.Resolved.Attachments[id].URL
			}
		case discordgo.ApplicationCommandOptionNumber:
			r.vars[key] = ftoa(o.FloatValue())
		case discordgo.ApplicationCommandOptionInteger:
			r.vars[key] = itoa(int(o.IntValue()))
		default:
			r.vars[key] = fmt.Sprint(o.Value)
		}
	}
}

// who resolves a step's target: the invoker, or a user/player option.
func (r *runner) who(target string) *discordgo.User {
	if target == "" || target == "@self" {
		return r.invoker
	}
	return r.users[strings.TrimPrefix(target, "opt:")]
}

// ============================================================================
// Conditions
// ============================================================================

func (r *runner) holds(c condition) bool {
	left, right := strings.TrimSpace(r.fill(c.Left)), strings.TrimSpace(r.fill(c.Right))
	ln, lerr := strconv.ParseFloat(left, 64)
	rn, rerr := strconv.ParseFloat(right, 64)
	numeric := lerr == nil && rerr == nil
	switch c.Op {
	case "eq":
		if numeric {
			return ln == rn
		}
		return strings.EqualFold(left, right)
	case "ne":
		if numeric {
			return ln != rn
		}
		return !strings.EqualFold(left, right)
	case "gt":
		return numeric && ln > rn
	case "gte":
		return numeric && ln >= rn
	case "lt":
		return numeric && ln < rn
	case "lte":
		return numeric && ln <= rn
	case "contains":
		return strings.Contains(strings.ToLower(left), strings.ToLower(right))
	case "not_empty":
		return left != ""
	case "empty":
		return left == ""
	}
	return false
}

// ============================================================================
// Steps
// ============================================================================

// exec runs one step. It reports true with the message to answer when the step stops the command.
func (r *runner) exec(st step) (string, bool) {
	stop := func(def string) (string, bool) {
		if st.Message != "" {
			return st.Message, true
		}
		return def, true
	}
	guildID := r.i.GuildID

	switch st.Type {
	case "require_player", "load_stats":
		u := r.who(st.Target)
		if u == nil {
			return stop("I could not tell who that is.")
		}
		view, err := database.Must().Player.Get(guildID, u.ID)
		if err != nil {
			if sdk.IsNotFound(err) {
				return stop(u.Mention() + " is not registered.")
			}
			return stop("Something went wrong. Please try again later.")
		}
		r.vars["player.name"], r.vars["player.id"], r.vars["player.mention"] = view.Config.Username, u.ID, u.Mention()
		r.vars["player.avatar"], r.vars["player.head"], r.vars["player.body"] = helpers.AvatarOf(u), helpers.HeadURL(view.Config.Username), helpers.BodyURL(view.Config.Username)
		r.vars["player.joined"] = view.Config.CreatedAt.Format("2006-01-02")
		if st.Type == "load_stats" {
			seasonKey, seasonID, err := helpers.ActiveSeasonKey(guildID)
			if err != nil || seasonKey == "" {
				return stop("There is no active season.")
			}
			playerID := sdk.UintToString(view.Player.ID)
			stats, err := database.Must().Stats.GetOrCreate(guildID, playerID, seasonID)
			if err != nil {
				return stop("Something went wrong. Please try again later.")
			}
			position := 0
			if rank, err := database.Must().Stats.Rank(guildID, playerID, seasonID); err == nil {
				position = rank.Position
			}
			for k, v := range helpers.StatsVars(u, view.Config, stats, position, seasonKey, seasonID) {
				r.vars[k] = v
			}
		}

	case "load_leaderboard":
		seasonKey, seasonID, err := helpers.ActiveSeasonKey(guildID)
		if err != nil || seasonKey == "" {
			return stop("There is no active season.")
		}
		limit := min(max(st.Limit, 1), 10)
		rows, err := database.Must().Stats.Leaderboard(guildID, seasonID, sdk.Page{Limit: limit})
		if err != nil {
			return stop("Something went wrong. Please try again later.")
		}
		vars := helpers.Vars{}
		helpers.LeaderboardRowVars(r.s, guildID, rows.Data, vars)
		for k, v := range vars {
			r.vars[k] = v
		}
		r.vars["lb.title"], r.vars["season.name"], r.vars["season.id"] = seasonKey+" Leaderboard", seasonKey, itoa(int(seasonID))

	case "load_game":
		number := atoi(strings.TrimSpace(r.fill(st.Left)))
		game, ok := helpers.Games.ByNumber(guildID, number)
		if !ok {
			return stop(fmt.Sprintf("Game #%d was not found.", number))
		}
		r.vars["game.number"], r.vars["game.status"], r.vars["game.map"] = itoa(game.Number), string(game.State), game.Map
		r.vars["game.mode"], r.vars["game.bot"] = game.Mode.Name, game.Bot
		r.vars["game.started_at"], r.vars["game.ended_at"] = "", ""
		if !game.StartedAt.IsZero() {
			r.vars["game.started_at"] = fmt.Sprintf("<t:%d:R>", game.StartedAt.Unix())
		}
		if (game.State == helpers.GameCleanup || game.State == helpers.GameVoided) && !game.StateAt.IsZero() {
			r.vars["game.ended_at"] = fmt.Sprintf("<t:%d:R>", game.StateAt.Unix())
		}

	case "load_recent_games":
		u := r.who(st.Target)
		if u == nil {
			return stop("I could not tell who that is.")
		}
		view, err := database.Must().Player.Get(guildID, u.ID)
		if err != nil {
			return stop(u.Mention() + " is not registered.")
		}
		_, seasonID, _ := helpers.ActiveSeasonKey(guildID)
		all, err := database.Must().Player.AllGames(guildID, sdk.UintToString(view.Player.ID))
		if err != nil {
			return stop("Something went wrong. Please try again later.")
		}
		for n := 1; n <= 5; n++ { // rows the player does not have are blank, not left as {{recent.N.*}}
			k := fmt.Sprintf("recent.%d.", n)
			r.vars[k+"number"], r.vars[k+"result"], r.vars[k+"map"] = "", "", ""
		}
		for n, h := range helpers.FilterGameHistoryEntries(all, seasonID, 0, false) {
			if n >= min(max(st.Limit, 1), 5) {
				break
			}
			result := "Loss"
			if h.IsWinner {
				result = "Win"
			}
			k := fmt.Sprintf("recent.%d.", n+1)
			r.vars[k+"number"], r.vars[k+"result"], r.vars[k+"map"] = itoa(h.Game.GameNumber), result, h.Game.MapName
		}

	case "load_commands":
		r.vars["commands.list"], r.vars["commands.count"] = r.commandList(strings.TrimSpace(st.Value))

	case "check":
		if !r.holds(condition{Left: st.Left, Op: st.Op, Right: st.Right}) {
			return stop("That is not allowed right now.")
		}
	case "stop":
		return stop("Stopped.")

	case "set_var":
		r.vars["var."+st.Name] = r.fill(st.Value)
	case "math":
		a, b := atof(strings.TrimSpace(r.fill(st.Left))), atof(strings.TrimSpace(r.fill(st.Right)))
		var out float64
		switch st.MathOp {
		case "+":
			out = a + b
		case "-":
			out = a - b
		case "*":
			out = a * b
		case "/", "%":
			if b == 0 {
				return stop("You cannot divide by zero.")
			}
			out = a / b
			if st.MathOp == "%" {
				out = math.Mod(a, b)
			}
		}
		r.vars["var."+st.Name] = ftoa(math.Round(out*1e6) / 1e6)
	case "random_pick":
		var lines []string
		for _, l := range strings.Split(st.Value, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) > 0 {
			r.vars["var."+st.Name] = r.fill(lines[rand.Intn(len(lines))])
		}
	case "random_number":
		lo, hi := atoi(strings.TrimSpace(r.fill(st.Left))), atoi(strings.TrimSpace(r.fill(st.Right)))
		if hi < lo {
			lo, hi = hi, lo
		}
		r.vars["var."+st.Name] = itoa(lo + rand.Intn(hi-lo+1))

	case "require_role":
		id := r.e.cfg.RequiredRoles[st.Name].ID
		if id == "" || !hasRole(r.i.Member, id) {
			if id == "" {
				return stop("That role requirement is not configured.")
			}
			return stop("You need the <@&" + id + "> role for this.")
		}
	case "check_role":
		u := r.who(st.Target)
		id := r.e.cfg.RequiredRoles[st.Name].ID
		if u == nil || id == "" {
			return stop("I could not check that user's role. Check the user option and configured role.")
		}
		member := r.i.Member
		if member == nil || member.User == nil || member.User.ID != u.ID {
			var err error
			member, err = helpers.FetchGuildMember(r.s, guildID, u.ID)
			if err != nil || member == nil {
				return stop("I could not find that member in this server.")
			}
		}
		if !hasRole(member, id) {
			return stop("That user does not have the <@&" + id + "> role.")
		}
	case "add_role", "remove_role":
		u := r.who(st.Target)
		id := r.e.cfg.RequiredRoles[st.Name].ID
		if u == nil || id == "" {
			return stop("I could not change that role.")
		}
		var err error
		if st.Type == "add_role" {
			err = r.s.GuildMemberRoleAdd(r.i.GuildID, u.ID, id)
		} else {
			err = r.s.GuildMemberRoleRemove(r.i.GuildID, u.ID, id)
		}
		if err != nil {
			return stop("I could not change that role. Check my permissions and role order.")
		}
	case "send_message":
		if id := r.e.cfg.RequiredChannels[st.Name].ID; id != "" {
			if _, err := r.s.ChannelMessageSendComplex(id, &discordgo.MessageSend{Content: strings.TrimSpace(r.fill(st.Value)), AllowedMentions: &discordgo.MessageAllowedMentions{}}); err != nil {
				logger.Warn(fmt.Sprintf("sending custom command message in %s: %v", id, err))
				return stop("I could not send the message. Check the bot's channel permissions.")
			}
		}
	case "send_payload":
		id := r.e.cfg.RequiredChannels[st.Name].ID
		if id == "" {
			return stop("That channel is not configured.")
		}
		var payload map[string]any
		if st.Reply.MessageID != "" {
			payload = r.savedMessage(st.Reply.MessageID)
		} else {
			payload = r.replyPayload(st.Reply)
		}
		if payload == nil {
			return stop("I could not load the selected rich message.")
		}
		if _, err := helpers.SendPayload(r.s, id, payload); err != nil {
			logger.Warn(fmt.Sprintf("sending custom command rich message in %s: %v", id, err))
			return stop("I could not send the rich message. Check the bot's channel permissions.")
		}
	case "dm_user":
		u := r.who(st.Target)
		if u == nil {
			return stop("I could not find the user to message.")
		}
		dm, err := r.s.UserChannelCreate(u.ID)
		if err == nil {
			_, err = r.s.ChannelMessageSendComplex(dm.ID, &discordgo.MessageSend{Content: strings.TrimSpace(r.fill(st.Value)), AllowedMentions: &discordgo.MessageAllowedMentions{}})
		}
		if err != nil {
			logger.Warn(fmt.Sprintf("sending custom command DM to %s: %v", u.ID, err))
			return stop("I could not send that DM. The user may have DMs disabled.")
		}
	}
	return "", false
}

func hasRole(m *discordgo.Member, id string) bool {
	if m == nil {
		return false
	}
	for _, r := range m.Roles {
		if r == id {
			return true
		}
	}
	return false
}

// commandList is every enabled command the invoker may use, one line each, and how many. category filters by
// the command's category (or its start, so "general" takes "general/game" too).
func (r *runner) commandList(category string) (string, string) {
	gc := helpers.LoadGuildContext(r.i.GuildID)
	admin := helpers.MemberIsAdministrator(r.s, r.i)
	all := map[string]*types.Command{}
	for _, c := range helpers.CommandRegistry {
		if c != nil && c.Data != nil && !c.Disabled {
			all[c.Data.Name] = c
		}
	}
	for name, c := range customCommandsFor(r.i.GuildID) {
		all[name] = c
	}
	var lines []string
	for name, c := range all {
		if category != "" && !strings.HasPrefix(c.Category, category) {
			continue
		}
		if perm, ok := gc.Settings.Commands[name]; ok && perm.Disabled != nil && *perm.Disabled {
			continue
		}
		if !helpers.CheckSlashAccess(gc.Settings, r.i.GuildID, name, "", r.i.ChannelID, r.i.Member, admin).Allowed {
			continue
		}
		lines = append(lines, fmt.Sprintf("`/%s` %s", name, c.Description))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), itoa(len(lines))
}

// ============================================================================
// Replies
// ============================================================================

func (r *runner) buttonRow(rep reply) []discordgo.MessageComponent {
	var row []discordgo.MessageComponent
	for _, b := range rep.Buttons {
		if url := r.fill(b.URL); url != "" {
			button := discordgo.Button{Style: discordgo.LinkButton, Label: r.fill(b.Label), URL: url, Disabled: b.Disabled}
			if b.Emoji != "" {
				button.Emoji = &discordgo.ComponentEmoji{Name: b.Emoji}
			}
			row = append(row, button)
		}
	}
	if len(row) == 0 {
		return nil
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: row}}
}

func (r *runner) replyPayload(rep reply) map[string]any {
	doc := &helpers.MessageDoc{
		AllowMentions:         rep.AllowMentions,
		SuppressEmbeds:        rep.SuppressEmbeds,
		SuppressNotifications: rep.SuppressNotifications,
	}
	if rep.AllowedMentions != nil {
		doc.AllowedMentions = rep.AllowedMentions
	}
	switch rep.Type {
	case "component":
		doc.Mode = "v2"
		doc.V2 = rep.Components
	case "embed":
		doc.Mode = "embeds"
		doc.Content = rep.Content
		doc.Embeds = rep.Embeds
		if len(doc.Embeds) == 0 {
			legacy := helpers.Embed{Title: rep.Title, Description: rep.Text, Color: rep.Color, Thumbnail: rep.Thumbnail}
			legacy.Footer.Text = rep.Footer
			doc.Embeds = []helpers.Embed{legacy}
		}
		doc.Buttons = rep.Buttons
	default:
		doc.Mode = "embeds"
		doc.Content = rep.Content
		if doc.Content == "" {
			doc.Content = rep.Text
		}
		doc.Buttons = rep.Buttons
	}
	return doc.Payload(r.fill)
}

// embedOf is the embed an "embed" reply describes, with its variables filled in.
func (r *runner) embedOf(rep reply) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{Title: r.fill(rep.Title), Description: r.fill(rep.Text)}
	if len(rep.Color) == 7 && rep.Color[0] == '#' {
		if n, err := strconv.ParseInt(rep.Color[1:], 16, 32); err == nil {
			e.Color = int(n)
		}
	}
	if rep.Footer != "" {
		e.Footer = &discordgo.MessageEmbedFooter{Text: r.fill(rep.Footer)}
	}
	if rep.Thumbnail != "" {
		e.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: r.fill(rep.Thumbnail)}
	}
	return e
}

// sendAs answers with a reply whose privacy differs from the one the response was deferred with (a reply rule that is
// private while the default is public, or the other way round): Discord cannot change that afterwards, so the deferred
// message is removed and the reply goes out as a follow-up with the privacy it asked for.
func (r *runner) sendAs(rep reply) error {
	s, i := r.s, r.i
	var flags discordgo.MessageFlags
	if rep.Ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}

	if rep.Type == "message" {
		if payload := r.savedMessage(rep.MessageID); payload != nil {
			bits, _ := payload["flags"].(int)
			payload["flags"] = bits | int(flags)
			_ = s.InteractionResponseDelete(i.Interaction)
			_, err := s.RequestWithBucketID("POST", discordgo.EndpointWebhookToken(i.AppID, i.Token), payload, discordgo.EndpointWebhookToken(i.AppID, ""))
			return err
		}
	}
	if rep.Type == "message" || rep.Type == "embed" || rep.Type == "component" {
		payload := r.replyPayload(rep)
		if flags != 0 {
			bits, _ := payload["flags"].(int)
			payload["flags"] = bits | int(flags)
		}
		_ = s.InteractionResponseDelete(i.Interaction)
		_, err := s.RequestWithBucketID("POST", discordgo.EndpointWebhookToken(i.AppID, i.Token), payload, discordgo.EndpointWebhookToken(i.AppID, ""))
		return err
	}

	params := &discordgo.WebhookParams{Flags: flags, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	switch {
	case rep.Type == "message":
		params.Embeds = []*discordgo.MessageEmbed{helpers.SimpleErrorEmbed("The saved message for this reply was deleted.")}
	case rep.Type == "embed":
		params.Embeds = []*discordgo.MessageEmbed{r.embedOf(rep)}
	default:
		if img := r.bannerIf(rep); img != nil {
			params.Content = r.fill(rep.Text)
			params.Files = []*discordgo.File{{Name: r.name + ".png", ContentType: "image/png", Reader: bytes.NewReader(img)}}
		} else if params.Content = r.fill(rep.Text); strings.TrimSpace(params.Content) == "" {
			params.Content = "Done."
		}
	}
	if row := r.buttonRow(rep); row != nil && params.Files == nil {
		params.Components = row
	}
	_ = s.InteractionResponseDelete(i.Interaction)
	_, err := s.FollowupMessageCreate(i.Interaction, true, params)
	return err
}

// bannerIf is the banner of a "banner" reply, or nil for any other kind of reply.
func (r *runner) bannerIf(rep reply) []byte {
	if rep.Type != "banner" {
		return nil
	}
	return r.banner()
}

func (r *runner) send(rep reply) error {
	s, i := r.s, r.i
	if rep.Ephemeral != r.ephemeral && !helpers.IsPrefixInteraction(i) {
		return r.sendAs(rep)
	}
	switch rep.Type {
	case "message":
		if payload := r.savedMessage(rep.MessageID); payload != nil {
			if helpers.IsPrefixInteraction(i) {
				_, err := helpers.SendPayload(s, i.ChannelID, payload)
				return err
			}
			return helpers.EditOriginalPayload(s, i.Interaction, payload)
		}
		if rep.MessageID != "" {
			return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("The saved message for this reply was deleted."))
		}
		payload := r.replyPayload(rep)
		if helpers.IsPrefixInteraction(i) {
			_, err := helpers.SendPayload(s, i.ChannelID, payload)
			return err
		}
		return helpers.EditOriginalPayload(s, i.Interaction, payload)

	case "embed", "component":
		payload := r.replyPayload(rep)
		if helpers.IsPrefixInteraction(i) {
			_, err := helpers.SendPayload(s, i.ChannelID, payload)
			return err
		}
		return helpers.EditOriginalPayload(s, i.Interaction, payload)

	case "banner":
		if img := r.banner(); img != nil {
			return helpers.EditInteractionFile(s, i, r.fill(rep.Text), r.name+".png", img)
		}
	}
	text := r.fill(rep.Text)
	if strings.TrimSpace(text) == "" {
		text = "Done."
	}
	text = strings.TrimSpace(text)
	edit := &discordgo.WebhookEdit{Content: &text, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	if row := r.buttonRow(rep); row != nil {
		edit.Components = &row
	}
	return helpers.EditInteractionResponse(s, i, edit)
}

// banner draws the guild's banner style for this command, or nil when there is none or drawing fails.
func (r *runner) banner() []byte {
	doc := helpers.LoadBanners(r.i.GuildID)
	if doc == nil {
		return nil
	}
	viewer := helpers.BannerViewer(r.invoker.ID, r.i.Member)
	b := doc.Choose("custom:"+r.name, viewer, "")
	if b == nil {
		return nil
	}
	img, err := helpers.RenderBanner(b, helpers.Vars(r.vars))
	if err != nil {
		logger.Warn(fmt.Sprintf("banner of /%s in %s: %v", r.name, r.i.GuildID, err))
		return nil
	}
	return img
}

// savedMessage is the Embed Builder message with the id, as a payload with the command's variables filled in.
func (r *runner) savedMessage(id string) map[string]any {
	cfg, err := database.Must().GuildConfig.Get(r.i.GuildID)
	if err != nil {
		return nil
	}
	var sections struct {
		Messages []helpers.MessageDoc `json:"messages"`
	}
	if cfg.Panels.IsEmpty() || cfg.Panels.Unmarshal(&sections) != nil {
		return nil
	}
	for idx := range sections.Messages {
		if sections.Messages[idx].ID == id {
			return sections.Messages[idx].Payload(helpers.VarFiller(r.vars))
		}
	}
	return nil
}

type option struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Choices     []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"choices"`
	Min *float64 `json:"min"`
	Max *float64 `json:"max"`
}

type condition struct {
	Left  string `json:"left"`
	Op    string `json:"op"`
	Right string `json:"right"`
}

type step struct {
	ID      string     `json:"id"`
	Type    string     `json:"type"`
	Target  string     `json:"target"`
	Limit   int        `json:"limit"`
	Message string     `json:"message"`
	Left    string     `json:"left"`
	Op      string     `json:"op"`
	Right   string     `json:"right"`
	MathOp  string     `json:"mathOp"`
	Name    string     `json:"name"`
	Value   string     `json:"value"`
	When    *condition `json:"when"`
	Reply   reply      `json:"reply"`
}

type replyButton = types.LinkButton

type reply struct {
	Type                  string                   `json:"type"`
	Text                  string                   `json:"text"`
	Content               string                   `json:"content"`
	MessageID             string                   `json:"messageId"`
	Ephemeral             bool                     `json:"ephemeral"`
	AllowMentions         bool                     `json:"allow_mentions"`
	AllowedMentions       *helpers.AllowedMentions `json:"allowed_mentions"`
	SuppressEmbeds        bool                     `json:"suppress_embeds"`
	SuppressNotifications bool                     `json:"suppress_notifications"`
	Title                 string                   `json:"title"`
	Color                 string                   `json:"color"`
	Footer                string                   `json:"footer"`
	Thumbnail             string                   `json:"thumbnail"`
	Buttons               []replyButton            `json:"buttons"`
	Embeds                []types.Embed            `json:"embeds"`
	Components            []types.V2Item           `json:"components"`
}

type replyRule struct {
	When  condition `json:"when"`
	Reply reply     `json:"reply"`
}

// body is everything a command, or one subcommand of it, does when run.
type body struct {
	Options    []option    `json:"options"`
	Steps      []step      `json:"steps"`
	Reply      reply       `json:"reply"`
	ReplyRules []replyRule `json:"replyRules"`
}

type subcommand struct {
	body
	Name        string `json:"name"`
	Description string `json:"description"`
}

type builder struct {
	body
	Requirements []struct {
		Type     string `json:"type"`
		Key      string `json:"key"`
		Optional bool   `json:"optional"`
	} `json:"requirements"`
	Subcommands      []subcommand      `json:"subcommands"`
	SubcommandGroups []subcommandGroup `json:"subcommandGroups"`
}

type subcommandGroup struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Subcommands []subcommand `json:"subcommands"`
}

// bodyFor is the body a call runs: the named subcommand's, or the command's own.
func (b *builder) bodyFor(sub string) *body {
	if strings.Contains(sub, " ") {
		parts := strings.SplitN(sub, " ", 2)
		for groupIndex := range b.SubcommandGroups {
			group := &b.SubcommandGroups[groupIndex]
			if group.Name != parts[0] {
				continue
			}
			for commandIndex := range group.Subcommands {
				if group.Subcommands[commandIndex].Name == parts[1] {
					return &group.Subcommands[commandIndex].body
				}
			}
			return nil
		}
		return nil
	}
	if len(b.Subcommands) == 0 {
		if len(b.SubcommandGroups) == 0 {
			return &b.body
		}
		return nil
	}
	for idx := range b.Subcommands {
		if b.Subcommands[idx].Name == sub {
			return &b.Subcommands[idx].body
		}
	}
	return nil
}

// defaultReply is the one whose ephemerality Discord is told when the reply is deferred.
func (b *builder) defaultReply() reply {
	if len(b.SubcommandGroups) > 0 && len(b.SubcommandGroups[0].Subcommands) > 0 {
		return b.SubcommandGroups[0].Subcommands[0].Reply
	}
	if len(b.Subcommands) > 0 {
		return b.Subcommands[0].Reply
	}
	return b.Reply
}

func optionType(t string) (discordgo.ApplicationCommandOptionType, bool) {
	switch t {
	case "player", "user":
		return discordgo.ApplicationCommandOptionUser, true
	case "string":
		return discordgo.ApplicationCommandOptionString, true
	case "integer":
		return discordgo.ApplicationCommandOptionInteger, true
	case "number":
		return discordgo.ApplicationCommandOptionNumber, true
	case "boolean":
		return discordgo.ApplicationCommandOptionBoolean, true
	case "role":
		return discordgo.ApplicationCommandOptionRole, true
	case "channel":
		return discordgo.ApplicationCommandOptionChannel, true
	case "mentionable":
		return discordgo.ApplicationCommandOptionMentionable, true
	case "attachment":
		return discordgo.ApplicationCommandOptionAttachment, true
	}
	return 0, false
}

func describe(text, fallback string) string {
	if t := strings.TrimSpace(text); t != "" {
		if r := []rune(t); len(r) > 100 {
			return string(r[:100])
		}
		return t
	}
	return fallback
}

// slashOptions turns builder options into Discord's.
func slashOptions(opts []option) []*discordgo.ApplicationCommandOption {
	out := make([]*discordgo.ApplicationCommandOption, 0, len(opts))
	for _, o := range opts {
		t, ok := optionType(o.Type)
		if !ok || o.Name == "" {
			continue
		}
		d := &discordgo.ApplicationCommandOption{Name: o.Name, Description: describe(o.Description, o.Name), Type: t, Required: o.Required}
		for _, c := range o.Choices {
			ch := &discordgo.ApplicationCommandOptionChoice{Name: c.Name, Value: c.Value}
			switch t {
			case discordgo.ApplicationCommandOptionInteger:
				ch.Value = atoi(c.Value)
			case discordgo.ApplicationCommandOptionNumber:
				ch.Value = atof(c.Value)
			}
			d.Choices = append(d.Choices, ch)
		}
		switch t {
		case discordgo.ApplicationCommandOptionString:
			if o.Min != nil {
				m := int(*o.Min)
				d.MinLength = &m
			}
			if o.Max != nil {
				d.MaxLength = int(*o.Max)
			}
		case discordgo.ApplicationCommandOptionInteger, discordgo.ApplicationCommandOptionNumber:
			d.MinValue = o.Min
			if o.Max != nil {
				d.MaxValue = *o.Max
			}
		}
		out = append(out, d)
	}
	return out
}

// applicationOptions is the command's option list: its own, or one sub-command per subcommand.
func (b *builder) applicationOptions() []*discordgo.ApplicationCommandOption {
	if len(b.SubcommandGroups) > 0 {
		out := make([]*discordgo.ApplicationCommandOption, 0, len(b.SubcommandGroups))
		for _, group := range b.SubcommandGroups {
			groupOption := &discordgo.ApplicationCommandOption{
				Name: group.Name, Description: describe(group.Description, group.Name),
				Type: discordgo.ApplicationCommandOptionSubCommandGroup,
			}
			for _, sc := range group.Subcommands {
				groupOption.Options = append(groupOption.Options, &discordgo.ApplicationCommandOption{
					Name: sc.Name, Description: describe(sc.Description, sc.Name),
					Type: discordgo.ApplicationCommandOptionSubCommand, Options: slashOptions(sc.Options),
				})
			}
			out = append(out, groupOption)
		}
		return out
	}
	if len(b.Subcommands) == 0 {
		return slashOptions(b.Options)
	}
	out := make([]*discordgo.ApplicationCommandOption, 0, len(b.Subcommands))
	for _, sc := range b.Subcommands {
		out = append(out, &discordgo.ApplicationCommandOption{
			Name: sc.Name, Description: describe(sc.Description, sc.Name), Type: discordgo.ApplicationCommandOptionSubCommand, Options: slashOptions(sc.Options),
		})
	}
	return out
}

var logger = baseutility.CreateModuleLogger("CUSTOM_COMMANDS", "discord")

func atoi(s string) int     { n, _ := strconv.Atoi(s); return n }
func atof(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
func itoa(n int) string     { return strconv.Itoa(n) }
func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// cacheFor is how long a guild's commands are reused before the config is read again.
const cacheFor = 15 * time.Second

type entry struct {
	cfg types.CommandConfig
	b   builder
	cmd *types.Command
}

type guildCommands struct {
	at   time.Time
	byID map[string]*entry
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*guildCommands{}
)

// entries returns the guild's custom commands by name. When the config cannot be read the last known set is used.
func entries(guildID string) map[string]*entry {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if c := cache[guildID]; c != nil && time.Since(c.at) < cacheFor {
		return c.byID
	}
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		if c := cache[guildID]; c != nil {
			return c.byID
		}
		return nil
	}
	raw := map[string]types.CommandConfig{}
	if !cfg.Commands.IsEmpty() {
		_ = cfg.Commands.Unmarshal(&raw)
	}
	out := map[string]*entry{}
	for name, c := range raw {
		if len(c.Builder) == 0 {
			continue
		}
		var b builder
		if err := json.Unmarshal(c.Builder, &b); err != nil {
			logger.Warn(fmt.Sprintf("custom command %q in %s: %v", name, guildID, err))
			continue
		}
		if c.Name == "" {
			c.Name = name
		}
		out[name] = &entry{cfg: c, b: b, cmd: build(name, c, &b)}
	}
	cache[guildID] = &guildCommands{at: time.Now(), byID: out}
	return out
}

// findCustomCommand returns the guild's custom command called name, or nil.
func findCustomCommand(guildID, name string) *types.Command {
	if e := entries(guildID)[name]; e != nil && helpers.FindCommand(name) == nil {
		return e.cmd
	}
	return nil
}

// customCommandsFor returns every custom command of the guild by name.
func customCommandsFor(guildID string) map[string]*types.Command {
	out := map[string]*types.Command{}
	for name, e := range entries(guildID) {
		if helpers.FindCommand(name) == nil {
			out[name] = e.cmd
		}
	}
	return out
}

func build(name string, c types.CommandConfig, b *builder) *types.Command {
	cmd := &types.Command{
		Name:        name,
		Description: describe(c.Description, "Custom command"),
		Category:    "custom",
		Ephemeral:   b.defaultReply().Ephemeral,
		Data:        &discordgo.ApplicationCommand{Name: name, Description: describe(c.Description, "Custom command"), Options: b.applicationOptions(), DefaultMemberPermissions: c.DefaultMemberPermissions},
	}
	for _, r := range b.Requirements {
		cmd.Requirements = append(cmd.Requirements, types.Requirement{Type: types.RequirementType(r.Type), Key: r.Key, Optional: r.Optional})
	}
	cmd.Execute = func(s *discordgo.Session, i *discordgo.InteractionCreate) error { return runCustomCommand(s, i, name) }
	helpers.EnsureCommandLogs(cmd)
	return cmd
}

func customCommandPath(data discordgo.ApplicationCommandInteractionData, b *builder) (string, []*discordgo.ApplicationCommandInteractionDataOption) {
	options := data.Options
	if len(options) == 0 {
		return "", nil
	}
	if len(b.SubcommandGroups) > 0 && options[0].Type == discordgo.ApplicationCommandOptionSubCommandGroup {
		if len(options[0].Options) == 0 {
			return options[0].Name, nil
		}
		command := options[0].Options[0]
		return options[0].Name + " " + command.Name, command.Options
	}
	if len(b.Subcommands) > 0 && options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		return options[0].Name, options[0].Options
	}
	return "", options
}

// ============================================================================
// Registering with Discord
// ============================================================================

// syncEvery is how often changes made on the dashboard reach Discord. Nothing is sent when a guild's list is unchanged
// (see the hash in syncGuild), so a short period only costs a config read per guild.
const syncEvery = 30 * time.Second

var (
	syncOnce sync.Once
	syncMu   sync.Mutex
	synced   = map[string][32]byte{} // guildID -> hash of the command list last sent
)

// StartCustomCommandSync registers every guild's custom commands with Discord now and then keeps them current. base is the
// built-in command list: with a development guild those are registered there too, and a guild-level overwrite
// there must not drop them.
func StartCustomCommandSync(s *discordgo.Session, appID, devGuildID string, base []*discordgo.ApplicationCommand) {
	run := func() {
		for _, g := range s.State.Guilds {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error(fmt.Sprintf("custom command sync panic in %s: %v", g.ID, r))
					}
				}()
				syncGuild(s, appID, g.ID, devGuildID, base)
			}()
		}
	}
	go run()
	syncOnce.Do(func() {
		go func() {
			for range time.Tick(syncEvery) {
				run()
			}
		}()
	})
}

func syncGuild(s *discordgo.Session, appID, guildID, devGuildID string, base []*discordgo.ApplicationCommand) {
	var desired []*discordgo.ApplicationCommand
	customs := 0
	for name, e := range entries(guildID) {
		if helpers.FindCommand(name) != nil || !e.cfg.IsEnabled || !e.cfg.IsSlashEnabled || e.cmd.Data == nil {
			continue
		}
		desired = append(desired, e.cmd.Data)
		customs++
	}
	if guildID == devGuildID {
		desired = append(append([]*discordgo.ApplicationCommand{}, base...), desired...)
	}
	raw, _ := json.Marshal(desired)
	hash := sha256.Sum256(raw)

	syncMu.Lock()
	last, seen := synced[guildID]
	syncMu.Unlock()
	if seen && last == hash {
		return
	}
	if !seen && customs == 0 && guildID != devGuildID {
		// Nothing to register; only clear what an earlier run left behind.
		if existing, err := s.ApplicationCommands(appID, guildID); err != nil || len(existing) == 0 {
			if err == nil {
				syncMu.Lock()
				synced[guildID] = hash
				syncMu.Unlock()
			}
			return
		}
	}
	if desired == nil {
		desired = []*discordgo.ApplicationCommand{}
	}
	if _, err := s.ApplicationCommandBulkOverwrite(appID, guildID, desired); err != nil {
		logger.Warn(fmt.Sprintf("registering custom commands in %s: %v", guildID, err))
		return
	}
	syncMu.Lock()
	synced[guildID] = hash
	syncMu.Unlock()
}

func init() {
	helpers.FindCustomCommand = findCustomCommand
	helpers.CustomCommandsFor = customCommandsFor
	helpers.CustomCommandEphemeral = func(i *discordgo.InteractionCreate, cmd *types.Command) (bool, bool) {
		e := entries(i.GuildID)[cmd.Name]
		if e == nil || e.cmd != cmd {
			return false, false
		}
		// A custom command is private or public by the called subcommand's own default reply.
		sub, _ := customCommandPath(i.ApplicationCommandData(), &e.b)
		if b := e.b.bodyFor(sub); b != nil {
			return b.Reply.Ephemeral, true
		}
		return false, false
	}
}
