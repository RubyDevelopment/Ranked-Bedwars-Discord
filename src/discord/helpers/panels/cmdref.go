package panels

import (
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Messages that point at a command are best when the command is clickable. Discord writes that as </name:id>, which needs the command's id;
// the ids are read from Discord once in a while, and a command the bot cannot find is written as plain `/name`.

const commandIDsFor = 10 * time.Minute

type commandIDs struct {
	at  time.Time
	ids map[string]string
}

var commandIDCache = struct {
	sync.Mutex
	byGuild map[string]commandIDs
}{byGuild: map[string]commandIDs{}}

func loadCommandIDs(s *discordgo.Session, guildID string) map[string]string {
	commandIDCache.Lock()
	cached, ok := commandIDCache.byGuild[guildID]
	commandIDCache.Unlock()
	if ok && time.Since(cached.at) < commandIDsFor {
		return cached.ids
	}
	ids := map[string]string{}
	if s != nil && s.State != nil && s.State.User != nil {
		app := s.State.User.ID
		if global, err := s.ApplicationCommands(app, ""); err == nil {
			for _, c := range global {
				ids[c.Name] = c.ID
			}
		}
		if guildID != "" {
			if own, err := s.ApplicationCommands(app, guildID); err == nil {
				for _, c := range own {
					ids[c.Name] = c.ID
				}
			}
		}
	}
	commandIDCache.Lock()
	commandIDCache.byGuild[guildID] = commandIDs{at: time.Now(), ids: ids}
	commandIDCache.Unlock()
	return ids
}

// commandRefs writes commands for a guild's messages: "stats" or "party info" becomes a clickable mention when the command is known.
func commandRefs(s *discordgo.Session, guildID string) func(name string) string {
	ids := loadCommandIDs(s, guildID)
	return func(name string) string {
		if fields := strings.Fields(name); len(fields) > 0 {
			if id := ids[fields[0]]; id != "" {
				return "</" + name + ":" + id + ">"
			}
		}
		return "`/" + name + "`"
	}
}

// CommandRefs is commandRefs for other packages: it writes a command for a message in a guild.
func CommandRefs(s *discordgo.Session, guildID string) func(name string) string { return commandRefs(s, guildID) }
