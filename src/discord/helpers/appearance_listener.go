package helpers

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/bwmarrin/discordgo"

	baseutility "ranked-bedwars/src/utility"
)

// ============================================================================
// Appearance endpoint
// ============================================================================
//
// When someone saves the bot's name, icon, banner or about text in the dashboard, the dashboard's server calls
// POST /live/appearance on the bot's HTTP port (the one the live console uses, with the same LIVE_CHAT_SECRET). The bot
// then applies that server's appearance at once. Nothing polls and nothing runs on a timer.

// appearanceSession is the bot's Discord session, set once it is ready.
var appearanceSession atomic.Pointer[discordgo.Session]

// ServeAppearance is POST /live/appearance with {"guild": "<server id>"}. It answers 202 as soon as the request is checked;
// the appearance is applied in the background (it fetches images and calls Discord, which can take a few seconds).
func ServeAppearance(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("LIVE_CHAT_SECRET")
	if secret == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(given), []byte(secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	guild, ok := parseAppearanceGuild(r.Body)
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	session := appearanceSession.Load()
	if session == nil {
		http.Error(w, "the bot is not ready yet", http.StatusServiceUnavailable)
		return
	}
	go func() {
		logger := baseutility.CreateModuleLogger("APPEARANCE_SYNC", "AppearanceEndpoint")
		logger.Info(fmt.Sprintf("The dashboard changed the appearance of guild %s: applying it", guild))
		if err := updateGuildAppearance(session, guild, logger); err != nil {
			logger.Error(fmt.Sprintf("Failed to apply appearance for guild %s: %v", guild, err))
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

// parseAppearanceGuild reads the server id from a request body, accepting only a Discord snowflake.
func parseAppearanceGuild(body io.Reader) (string, bool) {
	raw, err := io.ReadAll(io.LimitReader(body, 1024))
	if err != nil {
		return "", false
	}
	var payload struct {
		Guild string `json:"guild"`
	}
	if json.Unmarshal(raw, &payload) != nil || !chatGuildID.MatchString(payload.Guild) {
		return "", false
	}
	return payload.Guild, true
}
