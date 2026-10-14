package events

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// The gateway side of the dashboard's Events page: one handler per kind of thing Discord reports, each turning it into a
// trigger name (lib/custom-events.ts) and the variables of that event, then handing it to helpers.RunCustomEvents. They
// are plumbing, not events a server switches on here (each custom event has its own switch).

type extra = map[string]string

func on(name string, execute any) {
	helpers.RegisterEvent(&types.Event{Name: "Custom Events: " + name, System: true, Type: "custom_events", Execute: execute})
}

func run(s *discordgo.Session, guildID, trigger string, c helpers.EventCtx) {
	if guildID != "" {
		helpers.RunCustomEvents(s, guildID, trigger, c)
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
		run(s, m.GuildID, "member_join", helpers.EventCtx{User: m.User, Member: m.Member})
	})
	on("Member Leave", func(s *discordgo.Session, m *discordgo.GuildMemberRemove) {
		run(s, m.GuildID, "member_leave", helpers.EventCtx{User: m.User, Member: m.Member})
	})
	on("Member Ban", func(s *discordgo.Session, m *discordgo.GuildBanAdd) {
		reason := ""
		if ban, err := s.GuildBan(m.GuildID, m.User.ID); err == nil && ban != nil {
			reason = ban.Reason
		}
		run(s, m.GuildID, "member_ban", helpers.EventCtx{User: m.User, Extra: extra{"event.reason": reason}})
	})
	on("Member Unban", func(s *discordgo.Session, m *discordgo.GuildBanRemove) {
		run(s, m.GuildID, "member_unban", helpers.EventCtx{User: m.User})
	})
	on("Member Update", memberUpdate)

	// ── Messages ──
	on("Message Create", func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.GuildID == "" || m.Author == nil {
			return
		}
		member := m.Member
		if member != nil {
			member.User = m.Author
		}
		run(s, m.GuildID, "message_create", helpers.EventCtx{User: m.Author, Member: member, ChannelID: m.ChannelID, Extra: messageVars(m.GuildID, m.Message, m.Author)})
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
		run(s, m.GuildID, "message_edit", helpers.EventCtx{User: m.Author, Member: m.Member, ChannelID: m.ChannelID, Extra: x})
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
		run(s, m.GuildID, "message_delete", helpers.EventCtx{User: author, ChannelID: m.ChannelID, Extra: messageVars(m.GuildID, m.BeforeDelete, author)})
	})
	on("Message Bulk Delete", func(s *discordgo.Session, m *discordgo.MessageDeleteBulk) {
		run(s, m.GuildID, "message_bulk_delete", helpers.EventCtx{ChannelID: m.ChannelID, Extra: extra{"event.count": fmt.Sprint(len(m.Messages))}})
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
		run(s, m.GuildID, "reaction_add", helpers.EventCtx{User: user, Member: member, ChannelID: m.ChannelID, Extra: x})
	})
	on("Reaction Remove", func(s *discordgo.Session, m *discordgo.MessageReactionRemove) {
		user, _ := who(s, m.GuildID, m.UserID)
		x := messageVars(m.GuildID, &discordgo.Message{ID: m.MessageID, ChannelID: m.ChannelID}, nil)
		x["message.content"], x["reaction.emoji"] = "", emojiText(m.Emoji)
		run(s, m.GuildID, "reaction_remove", helpers.EventCtx{User: user, ChannelID: m.ChannelID, Extra: x})
	})

	// ── Channels ──
	on("Channel Create", func(s *discordgo.Session, m *discordgo.ChannelCreate) {
		if m.Channel == nil || m.IsThread() {
			return
		}
		run(s, m.GuildID, "channel_create", helpers.EventCtx{ChannelID: m.ID, Extra: extra{"channel.name": m.Name}})
	})
	on("Channel Delete", func(s *discordgo.Session, m *discordgo.ChannelDelete) {
		if m.Channel == nil || m.IsThread() {
			return
		}
		run(s, m.GuildID, "channel_delete", helpers.EventCtx{Extra: extra{"channel.name": m.Name, "channel.id": m.ID, "channel.mention": "#" + m.Name}})
	})
	on("Channel Update", func(s *discordgo.Session, m *discordgo.ChannelUpdate) {
		if m.Channel == nil || m.IsThread() {
			return
		}
		run(s, m.GuildID, "channel_update", helpers.EventCtx{ChannelID: m.ID, Extra: extra{"channel.name": m.Name}})
	})
	on("Thread Create", func(s *discordgo.Session, m *discordgo.ThreadCreate) {
		if m.Channel == nil || !m.NewlyCreated {
			return
		}
		run(s, m.GuildID, "thread_create", helpers.EventCtx{ChannelID: m.ID, Extra: extra{"channel.name": m.Name}})
	})
	on("Thread Delete", func(s *discordgo.Session, m *discordgo.ThreadDelete) {
		if m.Channel == nil {
			return
		}
		run(s, m.GuildID, "thread_delete", helpers.EventCtx{Extra: extra{"channel.name": m.Name, "channel.id": m.ID, "channel.mention": m.Name}})
	})

	// ── Roles ──
	on("Role Create", func(s *discordgo.Session, m *discordgo.GuildRoleCreate) {
		run(s, m.GuildID, "role_create", helpers.EventCtx{Extra: roleVars(s, m.GuildID, m.Role.ID, m.Role)})
	})
	on("Role Delete", func(s *discordgo.Session, m *discordgo.GuildRoleDelete) {
		run(s, m.GuildID, "role_delete", helpers.EventCtx{Extra: roleVars(s, m.GuildID, m.RoleID, nil)})
	})
	on("Role Update", func(s *discordgo.Session, m *discordgo.GuildRoleUpdate) {
		run(s, m.GuildID, "role_update", helpers.EventCtx{Extra: roleVars(s, m.GuildID, m.Role.ID, m.Role)})
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
			run(s, m.GuildID, "voice_join", helpers.EventCtx{User: user, Member: member, ChannelID: now, Extra: x})
		case before != "" && now == "":
			run(s, m.GuildID, "voice_leave", helpers.EventCtx{User: user, Member: member, ChannelID: before, Extra: x})
		case before != "" && now != "":
			run(s, m.GuildID, "voice_move", helpers.EventCtx{User: user, Member: member, ChannelID: now, Extra: x})
		}
	})

	// ── Invites ──
	on("Invite Create", func(s *discordgo.Session, m *discordgo.InviteCreate) {
		inviter := "Unknown"
		if m.Inviter != nil {
			inviter = m.Inviter.Username
		}
		run(s, m.GuildID, "invite_create", helpers.EventCtx{User: m.Inviter, ChannelID: m.ChannelID, Extra: extra{"invite.code": m.Code, "invite.url": "https://discord.gg/" + m.Code, "invite.inviter": inviter}})
	})
	on("Invite Delete", func(s *discordgo.Session, m *discordgo.InviteDelete) {
		run(s, m.GuildID, "invite_delete", helpers.EventCtx{ChannelID: m.ChannelID, Extra: extra{"invite.code": m.Code, "invite.url": "https://discord.gg/" + m.Code}})
	})

	// ── Scheduled events ──
	scheduled := func(e *discordgo.GuildScheduledEvent) extra {
		return extra{
			"scheduled.name": e.Name, "scheduled.description": e.Description,
			"scheduled.link":  fmt.Sprintf("https://discord.com/events/%s/%s", e.GuildID, e.ID),
			"scheduled.start": fmt.Sprintf("<t:%d:R>", e.ScheduledStartTime.Unix()),
		}
	}
	on("Scheduled Event Create", func(s *discordgo.Session, m *discordgo.GuildScheduledEventCreate) {
		run(s, m.GuildID, "scheduled_event_create", helpers.EventCtx{Extra: scheduled(m.GuildScheduledEvent)})
	})
	on("Scheduled Event Delete", func(s *discordgo.Session, m *discordgo.GuildScheduledEventDelete) {
		run(s, m.GuildID, "scheduled_event_delete", helpers.EventCtx{Extra: scheduled(m.GuildScheduledEvent)})
	})
	var seenMu sync.Mutex
	seen := map[string]discordgo.GuildScheduledEventStatus{}
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
			run(s, m.GuildID, "scheduled_event_start", helpers.EventCtx{Extra: scheduled(m.GuildScheduledEvent)})
		case discordgo.GuildScheduledEventStatusCompleted:
			run(s, m.GuildID, "scheduled_event_end", helpers.EventCtx{Extra: scheduled(m.GuildScheduledEvent)})
		}
	})

	// ── Server ──
	on("Guild Update", func(s *discordgo.Session, m *discordgo.GuildUpdate) {
		if m.Guild != nil {
			run(s, m.ID, "guild_update", helpers.EventCtx{})
		}
	})
	on("AutoMod", func(s *discordgo.Session, m *discordgo.AutoModerationActionExecution) {
		user, member := who(s, m.GuildID, m.UserID)
		rule := "AutoMod"
		if r, err := s.AutoModerationRule(m.GuildID, m.RuleID); err == nil && r != nil {
			rule = r.Name
		}
		run(s, m.GuildID, "automod_action", helpers.EventCtx{User: user, Member: member, ChannelID: m.ChannelID, Extra: extra{"automod.rule": rule, "automod.content": m.Content, "automod.keyword": m.MatchedKeyword}})
	})
}

// memberUpdate turns what changed about a member into the member events: boosting, nickname, roles and timeouts. It needs
// the member as the bot saw it before (BeforeUpdate), so a change to a member the bot never saw is not reported.
func memberUpdate(s *discordgo.Session, m *discordgo.GuildMemberUpdate) {
	before := m.BeforeUpdate
	if before == nil || m.Member == nil {
		return
	}
	c := func(x extra) helpers.EventCtx { return helpers.EventCtx{User: m.User, Member: m.Member, Extra: x} }

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
		if !slices.Contains(before.Roles, id) {
			run(s, m.GuildID, "member_role_added", c(roleVars(s, m.GuildID, id, nil)))
		}
	}
	for _, id := range before.Roles {
		if !slices.Contains(m.Roles, id) {
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
