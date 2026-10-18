package helpers

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
)

// Custom events are what a server has the bot do when something happens in Discord (the dashboard's Events page): post a
// message, DM the member, give or take a role. They live in the guild config's panels.custom_events, and their messages
// are the same documents the Embed Builder makes, so they are rendered by MessageDoc.Payload like every other message.
// The trigger names are the dashboard's (lib/custom-events.ts); the gateway handlers are in events/custom_events.go.

type CustomEventAction struct {
	Type        string      `json:"type"` // message, dm, add_role, remove_role
	ChannelID   string      `json:"channel_id"`
	SameChannel bool        `json:"same_channel"` // message: post in the channel where the event happened
	RoleID      string      `json:"role_id"`
	Message     *MessageDoc `json:"message"`
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

// EventCtx is defined in package types (events.go).
type EventCtx = types.EventCtx

var customEventsCache = newTTLCache[[]CustomEvent](eloConfigCacheTTL)

// LoadCustomEvents reads the guild's events (cached briefly). A failed read counts as "none".
func LoadCustomEvents(guildID string) []CustomEvent {
	list, _ := customEventsCache.get(guildID, func() ([]CustomEvent, error) {
		raw, err := database.Must().GuildConfig.Field(guildID, "panels")
		if err != nil && !sdk.IsNotFound(err) {
			return nil, err
		}
		return parseCustomEvents(raw), nil
	})
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
func eventsFor(all []CustomEvent, trigger string, c EventCtx) []CustomEvent {
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
func eventVars(s *discordgo.Session, guildID string, ev CustomEvent, c EventCtx) map[string]string {
	v := map[string]string(GuildVars(s, guildID, nil))
	now := time.Now().UTC()
	v["time.date"], v["time.time"] = now.Format("2006-01-02"), now.Format("15:04")
	v["time.unix"], v["time.relative"] = fmt.Sprint(now.Unix()), fmt.Sprintf("<t:%d:R>", now.Unix())
	v["event.name"], v["event.reason"], v["event.count"] = ev.Name, "", ""
	v["member.count"] = v["guild.member_count"]

	if u := c.User; u != nil {
		v["member.name"], v["member.mention"], v["member.id"], v["member.tag"] = name(u, c.Member), u.Mention(), u.ID, u.Username
		v["member.avatar"] = AvatarOf(u)
		v["member.created"] = fmt.Sprintf("<t:%d:R>", AccountCreatedAt(u.ID).Unix())
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
func RunCustomEvents(s *discordgo.Session, guildID, trigger string, c EventCtx) {
	if c.User != nil && s.State != nil && s.State.User != nil && c.User.ID == s.State.User.ID {
		return
	}
	for _, ev := range eventsFor(LoadCustomEvents(guildID), trigger, c) {
		fill := VarFiller(eventVars(s, guildID, ev, c))
		for i, a := range ev.Actions {
			if err := runEventAction(s, guildID, c, a, fill); err != nil {
				logger.Warn(fmt.Sprintf("event %q step %d in %s: %v", ev.Name, i+1, guildID, err))
			}
		}
	}
}

func runEventAction(s *discordgo.Session, guildID string, c EventCtx, a CustomEventAction, fill func(string) string) error {
	switch a.Type {
	case "message":
		channel := a.ChannelID
		if a.SameChannel {
			channel = c.ChannelID
		}
		if a.Message == nil || channel == "" {
			return nil
		}
		_, err := SendPayload(s, channel, a.Message.Payload(fill))
		return err
	case "dm":
		if a.Message == nil || c.User == nil {
			return nil
		}
		ch, err := s.UserChannelCreate(c.User.ID)
		if err != nil {
			return err
		}
		_, err = SendPayload(s, ch.ID, a.Message.Payload(fill))
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

// ============================================================================
// Event Configuration
// ============================================================================

// The guild config's "event_configs" map mirrors "commands": one entry per
// registered event with an on/off switch and the roles, channels and categories
// the event's Requirements declare. An admin assigns them from the dashboard's
// Events page; the event reads them back with the helpers below.

const (
	eventConfigField = "event_configs"
	eventConfigTTL   = 30 * time.Second
)

type eventConfigEntry struct {
	configs map[string]types.EventConfig
	expires time.Time
}

var (
	eventConfigMu    sync.Mutex
	eventConfigCache = map[string]eventConfigEntry{}
)

// SeedEventConfigs adds a default EventConfig for every registered event the
// guild doesn't have one for yet, and keeps each entry's requirement slots in
// step with what the event declares (assigned IDs are kept).
func SeedEventConfigs(guildID string) error {
	existing, err := loadEventConfigs(guildID, false)
	if err != nil {
		return err
	}

	changed := false
	for _, ev := range EventRegistry {
		if ev == nil || ev.Name == "" {
			continue
		}
		cfg, ok := existing[ev.Name]
		if !ok {
			cfg = types.EventConfig{Name: ev.Name, Description: ev.Description, IsEnabled: !ev.Disabled || ev.System, IsSystem: ev.System}
			changed = true
		}
		if cfg.IsSystem != ev.System {
			cfg.IsSystem = ev.System
			if ev.System {
				cfg.IsEnabled = true
			}
			changed = true
		}
		if cfg.Description != ev.Description {
			cfg.Description = ev.Description
			changed = true
		}
		if reconcileEventRequirements(&cfg, ev.Requirements) {
			changed = true
		}
		existing[ev.Name] = cfg
	}
	if !changed {
		return nil
	}

	if _, err := database.Must().GuildConfig.SaveField(sdk.SystemActor, guildID, eventConfigField, existing); err != nil {
		return err
	}
	InvalidateEventConfig(guildID)
	return nil
}

func reconcileEventRequirements(cfg *types.EventConfig, requirements []types.Requirement) bool {
	cc := types.CommandConfig{RequiredRoles: cfg.RequiredRoles, RequiredChannels: cfg.RequiredChannels, RequiredCategories: cfg.RequiredCategories}
	changed := reconcileRequirements(&cc, requirements)
	cfg.RequiredRoles, cfg.RequiredChannels, cfg.RequiredCategories = cc.RequiredRoles, cc.RequiredChannels, cc.RequiredCategories
	return changed
}

// InvalidateEventConfig drops the cached event configs (call after an edit).
func InvalidateEventConfig(guildID string) {
	eventConfigMu.Lock()
	delete(eventConfigCache, guildID)
	eventConfigMu.Unlock()
}

func loadEventConfigs(guildID string, useCache bool) (map[string]types.EventConfig, error) {
	if useCache {
		eventConfigMu.Lock()
		e, ok := eventConfigCache[guildID]
		eventConfigMu.Unlock()
		if ok && time.Now().Before(e.expires) {
			return e.configs, nil
		}
	}

	out := map[string]types.EventConfig{}
	raw, err := database.Must().GuildConfig.Field(guildID, eventConfigField)
	if err != nil && !sdk.IsNotFound(err) {
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode %s: %w", eventConfigField, err)
		}
	}
	if out == nil {
		out = map[string]types.EventConfig{}
	}

	eventConfigMu.Lock()
	eventConfigCache[guildID] = eventConfigEntry{configs: out, expires: time.Now().Add(eventConfigTTL)}
	eventConfigMu.Unlock()
	return out, nil
}

// LoadEventConfig returns the guild's config for one event (cached briefly).
func LoadEventConfig(guildID, eventName string) (types.EventConfig, bool) {
	configs, err := loadEventConfigs(guildID, true)
	if err != nil {
		return types.EventConfig{}, false
	}
	cfg, ok := configs[eventName]
	return cfg, ok
}

// EventEnabled reports whether the guild has the event switched on. An event
// without a stored config (not seeded yet) or an unreadable config stays on.
func EventEnabled(guildID, eventName string) bool {
	cfg, ok := LoadEventConfig(guildID, eventName)
	return !ok || cfg.IsEnabled || cfg.IsSystem
}

// EventRequirementID returns the snowflake an admin assigned to the event's
// requirement key of the given type ("" when unset or unknown).
func EventRequirementID(guildID, eventName string, reqType types.RequirementType, key string) string {
	cfg, ok := LoadEventConfig(guildID, eventName)
	if !ok {
		return ""
	}
	switch reqType {
	case types.ReqRole:
		return cfg.RequiredRoles[key].ID
	case types.ReqChannel:
		return cfg.RequiredChannels[key].ID
	case types.ReqCategory:
		return cfg.RequiredCategories[key].ID
	}
	return ""
}
