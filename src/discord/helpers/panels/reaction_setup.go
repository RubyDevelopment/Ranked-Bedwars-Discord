package panels

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// A reaction role works when a member adds the emoji to the message, so the emoji has to be there to click. The bot adds it itself for
// every enabled binding: once per binding (and again if the binding changes), and a failed try (the message is gone, the bot cannot see
// the channel) is not repeated for ten minutes.

const reactionRetry = 10 * time.Minute

var reactionSet sync.Map // guild/binding/channel/message/emoji -> when the bot last tried, and whether it worked

type reactionTry struct {
	at time.Time
	ok bool
}

// apiEmoji writes a binding's emoji the way Discord's reaction route wants it: the character itself, or name:id for a custom one.
func apiEmoji(bound string) string {
	e := strings.Trim(strings.TrimSpace(bound), "<>")
	e = strings.TrimPrefix(e, "a:")
	e = strings.TrimPrefix(e, ":")
	return strings.TrimSuffix(strings.ReplaceAll(e, "::", ":"), ":")
}

// ensureReactionRoleEmojis adds the bot's own reaction to every enabled binding's message, once.
func ensureReactionRoleEmojis(s *discordgo.Session, guildID string) {
	now := time.Now()
	for _, rule := range rulesFor(guildID) {
		if !rule.Enabled || rule.RoleID == "" || rule.ChannelID == "" || rule.MessageID == "" || strings.TrimSpace(rule.Emoji) == "" {
			continue
		}
		key := strings.Join([]string{guildID, rule.ID, rule.ChannelID, rule.MessageID, rule.Emoji}, "/")
		if v, ok := reactionSet.Load(key); ok {
			if t := v.(reactionTry); t.ok || now.Sub(t.at) < reactionRetry {
				continue
			}
		}
		err := s.MessageReactionAdd(rule.ChannelID, rule.MessageID, url.PathEscape(apiEmoji(rule.Emoji)))
		reactionSet.Store(key, reactionTry{at: now, ok: err == nil})
		if err != nil {
			panelLogger.Warn(fmt.Sprintf("adding the %s reaction for reaction role %s in %s: %v", rule.Emoji, rule.ID, guildID, err))
		}
	}
}
