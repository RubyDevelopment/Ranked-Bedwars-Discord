// Package guildlink lets a main guild add secondary servers (a staff server, say) that run the bot as part of it.
//
// A secondary server has no guild record, config, seasons or bots of its own. Every event that comes from it is rewritten, before any
// handler sees it, to look as if it happened in the main guild: the commands, buttons, forms and prefix commands used there read the main
// guild's settings, roles, channels and data, and post their logs and alerts to the main guild's channels. Only the answer goes back to
// where it was asked. Every other event of a secondary server (members joining, voice, channel changes) is dropped: those are about the
// secondary server itself, not about the community.
package guildlink

import (
	"log"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
)

// homes maps a secondary server's snowflake to its main guild's.
var homes atomic.Pointer[map[string]string]

// Home is the main guild a secondary server belongs to.
func Home(venueID string) (string, bool) {
	m := homes.Load()
	if m == nil {
		return "", false
	}
	home, ok := (*m)[venueID]
	return home, ok
}

// IsSecondary says whether the server is a linked secondary server.
func IsSecondary(guildID string) bool { _, ok := Home(guildID); return ok }

// Venues lists the secondary servers of a main guild.
func Venues(homeID string) []string {
	var out []string
	if m := homes.Load(); m != nil {
		for venue, home := range *m {
			if home == homeID {
				out = append(out, venue)
			}
		}
	}
	return out
}

// Set records (or with an empty home, removes) a link at once, without waiting for the next refresh.
func Set(venueID, homeID string) {
	next := map[string]string{}
	if m := homes.Load(); m != nil {
		for k, v := range *m {
			next[k] = v
		}
	}
	if homeID == "" {
		delete(next, venueID)
	} else {
		next[venueID] = homeID
	}
	homes.Store(&next)
}

// Refresh reads every link from the database.
func Refresh() error {
	links, err := database.Links()
	if err != nil {
		return err
	}
	next := make(map[string]string, len(links))
	for _, l := range links {
		next[l.VenueGuildID] = l.HomeGuildID
	}
	homes.Store(&next)
	return nil
}

// Run keeps the links current: once now (retrying until it works) and then every minute, so a link made from the dashboard or by another
// bot instance is picked up. It returns when stop is closed.
func Run(stop <-chan struct{}) {
	wait := 2 * time.Second
	for {
		if err := Refresh(); err != nil {
			log.Printf("server links: %v", err)
			wait = min(wait*2, time.Minute)
		} else {
			wait = time.Minute
		}
		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
	}
}

// ============================================================================
// Where an interaction came from
// ============================================================================

type origin struct {
	venueID string
	until   time.Time
}

var origins sync.Map // interaction (or prefix message) id -> origin

// remember notes that an interaction came from a secondary server (for a quarter of an hour, as long as its token lives).
func remember(id, venueID string) {
	now := time.Now()
	origins.Store(id, origin{venueID, now.Add(15 * time.Minute)})
	origins.Range(func(k, v any) bool {
		if v.(origin).until.Before(now) {
			origins.Delete(k)
		}
		return true
	})
}

// VenueOf is the secondary server an interaction was made in, or "" when it was made in the main guild itself.
func VenueOf(interactionID string) string {
	// A prefix command's interaction is "prefix:<message id>:<command>"; it is remembered by its message.
	if rest, ok := strings.CutPrefix(interactionID, "prefix:"); ok {
		interactionID = "prefix:" + strings.SplitN(rest, ":", 2)[0]
	}
	if v, ok := origins.Load(interactionID); ok {
		return v.(origin).venueID
	}
	return ""
}

// ReplyGuild is the server a reply to this interaction is posted in: where it was asked (a secondary server), else guildID. A message
// reference must name the guild the replied-to message is in, and that is not the main guild the command was run as.
func ReplyGuild(interactionID, guildID string) string {
	if venue := VenueOf(interactionID); venue != "" {
		return venue
	}
	return guildID
}

// ============================================================================
// Rewriting events
// ============================================================================

// homeMember is the person as a member of the main guild: their roles there decide what they may do, never the ones they hold in the
// secondary server. Someone who is not in the main guild gets a member with no roles, so every staff check refuses them.
func homeMember(s *discordgo.Session, homeID string, user *discordgo.User) *discordgo.Member {
	if user == nil {
		return nil
	}
	var m *discordgo.Member
	if s != nil {
		if s.State != nil {
			m, _ = s.State.Member(homeID, user.ID)
		}
		if m == nil {
			m, _ = s.GuildMember(homeID, user.ID)
		}
	}
	if m == nil {
		return &discordgo.Member{GuildID: homeID, User: user}
	}
	out := *m
	out.GuildID = homeID
	out.User = user
	out.Permissions = 0 // worked out again from the main guild's roles
	return &out
}

// Translate rewrites an event from a secondary server to look like it came from its main guild. keep is false for an event that must not
// be handled at all (everything from a secondary server except what people do with the bot).
func Translate(s *discordgo.Session, event any) (out any, keep bool) {
	m := homes.Load()
	if m == nil || len(*m) == 0 {
		return event, true
	}
	switch e := event.(type) {
	case *discordgo.InteractionCreate:
		if e == nil || e.Interaction == nil {
			return event, true
		}
		home, ok := (*m)[e.GuildID]
		if !ok {
			return event, true
		}
		i := *e.Interaction
		remember(i.ID, i.GuildID)
		i.GuildID = home
		if i.Member != nil {
			i.Member = homeMember(s, home, i.Member.User)
		}
		return &discordgo.InteractionCreate{Interaction: &i}, true
	case *discordgo.MessageCreate:
		if e == nil || e.Message == nil {
			return event, true
		}
		home, ok := (*m)[e.GuildID]
		if !ok {
			return event, true
		}
		msg := *e.Message
		remember("prefix:"+msg.ID, msg.GuildID)
		msg.GuildID = home
		if msg.Member != nil || msg.Author != nil {
			msg.Member = homeMember(s, home, msg.Author)
		}
		return &discordgo.MessageCreate{Message: &msg}, true
	}
	if venue := eventGuildID(event); venue != "" {
		if _, ok := (*m)[venue]; ok {
			return event, false
		}
	}
	return event, true
}

var guildType = reflect.TypeOf(&discordgo.Guild{})

// eventGuildID is the guild an event is about ("" for events that are about no guild), found without naming every event type.
func eventGuildID(event any) string {
	v := reflect.ValueOf(event)
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	return guildIDOf(v)
}

func guildIDOf(v reflect.Value) string {
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return ""
	}
	t := v.Type()
	for n := 0; n < t.NumField(); n++ {
		f := t.Field(n)
		if f.Name == "GuildID" && f.Type.Kind() == reflect.String {
			return v.Field(n).String()
		}
		if !f.Anonymous {
			continue
		}
		fv := v.Field(n)
		if f.Type == guildType {
			if !fv.IsNil() {
				return fv.Interface().(*discordgo.Guild).ID
			}
			continue
		}
		if fv.Kind() == reflect.Pointer && !fv.IsNil() {
			fv = fv.Elem()
		}
		if id := guildIDOf(fv); id != "" {
			return id
		}
	}
	return ""
}

// Wrap makes a discordgo event handler (func(*discordgo.Session, *SomeEvent)) see the rewritten event, and not see events that are to be
// dropped. A value that is not such a handler is returned as it is.
func Wrap(handler any) any {
	fn := reflect.ValueOf(handler)
	if !fn.IsValid() || fn.Kind() != reflect.Func || fn.Type().NumIn() != 2 || fn.Type().NumOut() != 0 {
		return handler
	}
	if fn.Type().In(0) != reflect.TypeOf((*discordgo.Session)(nil)) {
		return handler
	}
	return reflect.MakeFunc(fn.Type(), func(args []reflect.Value) []reflect.Value {
		s, _ := args[0].Interface().(*discordgo.Session)
		out, keep := Translate(s, args[1].Interface())
		if !keep {
			return nil
		}
		if out != args[1].Interface() {
			args = []reflect.Value{args[0], reflect.ValueOf(out)}
		}
		return fn.Call(args)
	}).Interface()
}
