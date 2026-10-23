package helpers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// Live Minecraft chat for the dashboard
// ============================================================================

// A dashboard viewer opens GET /live/chat?guild=<id>[&bot=<name>] (through the dashboard's own server, which checks who may
// look) and gets a Server-Sent Events stream of what the guild's bots see in Minecraft chat. Nothing is sent unless someone
// is watching: each viewer is registered here, the engine is told the complete list of what is watched (chat.watching) and
// forwards chat only for that. When the last viewer of a bot leaves (tab closed, page reloaded, stream dropped) the list
// shrinks, and the engine stops sending it.

const (
	chatViewerBuffer = 256
	chatHeartbeat    = 15 * time.Second
	// chatMaxStream ends one stream so a forgotten tab can't hold it for ever; a page still open simply reconnects.
	chatMaxStream = 5 * time.Minute
)

var chatGuildID = regexp.MustCompile(`^[0-9]{5,25}$`)

// chatEvent is one Server-Sent Event.
type chatEvent struct {
	name string
	data []byte
}

type chatViewer struct {
	guild string
	bot   string // lower-case; "" = every bot of the guild
	out   chan chatEvent
}

// ChatStream fans the engine's chat out to the dashboard viewers.
type ChatStream struct {
	mu      sync.Mutex
	viewers map[*chatViewer]struct{}
	bots    map[string][]string           // guild -> the bots the engine says are on a server
	pending map[string]chan controlResult // live console actions waiting for the engine's bot.result, by request id
}

// LiveChat is the process-wide stream.
var LiveChat = &ChatStream{viewers: map[*chatViewer]struct{}{}, bots: map[string][]string{}, pending: map[string]chan controlResult{}}

func (c *ChatStream) emit(v *chatViewer, name string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	select {
	case v.out <- chatEvent{name, data}:
	default: // a viewer that can't keep up loses lines instead of holding the engine back
	}
}

// Subscribe registers a viewer and tells the engine to start sending what it watches. Call the returned func to stop.
func (c *ChatStream) Subscribe(guild, bot string) (*chatViewer, func()) {
	v := &chatViewer{guild: guild, bot: strings.ToLower(strings.TrimSpace(bot)), out: make(chan chatEvent, chatViewerBuffer)}
	c.mu.Lock()
	c.viewers[v] = struct{}{}
	known := c.bots[guild]
	c.mu.Unlock()
	c.emit(v, "status", map[string]any{"engine": Engine.Connected(), "bots": known})
	c.sync()
	var once sync.Once
	return v, func() {
		once.Do(func() {
			c.mu.Lock()
			delete(c.viewers, v)
			watched := false
			for other := range c.viewers {
				if other.guild == guild {
					watched = true
					break
				}
			}
			if !watched {
				delete(c.bots, guild) // not kept: it would be stale by the next viewer
			}
			c.mu.Unlock()
			c.sync()
		})
	}
}

// sync tells the engine the complete list of what is watched (an empty list stops everything).
func (c *ChatStream) sync() {
	c.mu.Lock()
	seen := map[string]bool{}
	watches := []map[string]string{}
	for v := range c.viewers {
		key := v.guild + "\x00" + v.bot
		if !seen[key] {
			seen[key] = true
			watches = append(watches, map[string]string{"guild_id": v.guild, "bot": v.bot})
		}
	}
	c.mu.Unlock()
	if !Engine.Connected() {
		return
	}
	_ = Engine.write(map[string]any{"type": "chat.watching", "watches": watches})
}

// engineUp / engineDown are called as the engine attaches and drops.
func (c *ChatStream) engineUp() {
	c.sync()
	c.broadcast(func(*chatViewer) bool { return true }, "status", map[string]any{"engine": true})
}

func (c *ChatStream) engineDown() {
	c.mu.Lock()
	c.bots = map[string][]string{}
	c.mu.Unlock()
	c.broadcast(func(*chatViewer) bool { return true }, "status", map[string]any{"engine": false, "bots": []string{}})
}

func (c *ChatStream) broadcast(match func(*chatViewer) bool, name string, payload any) {
	c.mu.Lock()
	targets := make([]*chatViewer, 0, len(c.viewers))
	for v := range c.viewers {
		if match(v) {
			targets = append(targets, v)
		}
	}
	c.mu.Unlock()
	for _, v := range targets {
		c.emit(v, name, payload)
	}
}

// Line delivers one chat line from the engine to the viewers of that bot.
func (c *ChatStream) Line(guild, bot, text, motd string, at int64) {
	lower := strings.ToLower(bot)
	c.broadcast(func(v *chatViewer) bool { return v.guild == guild && (v.bot == "" || v.bot == lower) }, "line",
		map[string]any{"bot": bot, "text": text, "motd": motd, "at": at})
}

// Event delivers one console log entry (a GUI opening, a kick, an action that was run...) to the viewers of that bot.
func (c *ChatStream) Event(guild, bot, kind string, at int64, data json.RawMessage) {
	lower := strings.ToLower(bot)
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	c.broadcast(func(v *chatViewer) bool { return v.guild == guild && (v.bot == "" || v.bot == lower) }, "event",
		map[string]any{"bot": bot, "kind": kind, "at": at, "data": data})
}

// Bots records which of a guild's bots are on a server and tells that guild's viewers.
func (c *ChatStream) Bots(guild string, bots []string) {
	if bots == nil {
		bots = []string{}
	}
	c.mu.Lock()
	c.bots[guild] = bots
	c.mu.Unlock()
	c.broadcast(func(v *chatViewer) bool { return v.guild == guild }, "status", map[string]any{"engine": true, "bots": bots})
}

// ServeHTTP is GET /live/chat. LIVE_CHAT_SECRET is the shared secret of the dashboard's server; unset, the feature is off.
func (c *ChatStream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("LIVE_CHAT_SECRET")
	if secret == "" {
		http.NotFound(w, r)
		return
	}
	given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(given), []byte(secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	guild, bot := r.URL.Query().Get("guild"), strings.TrimSpace(r.URL.Query().Get("bot"))
	if !chatGuildID.MatchString(guild) || len(bot) > 32 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	viewer, stop := c.Subscribe(guild, bot)
	defer stop() // runs when the dashboard hangs up, which is what tells the engine to stop sending
	beat := time.NewTicker(chatHeartbeat)
	defer beat.Stop()
	deadline := time.NewTimer(chatMaxStream)
	defer deadline.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
			flusher.Flush()
			return
		case <-beat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case e := <-viewer.out:
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, e.data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ============================================================================
// Live console: actions on a bot
// ============================================================================

// A viewer with permission POSTs /live/control {guild, bot, action, params} (through the dashboard's server). The action goes to the
// engine as bot.control, and the engine's bot.result is the response. Only the actions below are forwarded; the engine checks the
// params of each (see Ranked-Bedwars-Engine/src/console.js).

const (
	controlTimeout   = 35 * time.Second
	controlBodyLimit = 8 << 10
)

// controlActions are the actions a viewer may ask for.
var controlActions = map[string]bool{
	"chat": true, "hotbar.select": true, "hotbar.use": true, "window.click": true, "window.close": true, "window.state": true,
	"inventory": true, "state": true, "control": true, "control.stop": true, "look": true, "swing": true, "block.use": true,
	"respawn": true, "connect": true, "disconnect": true, "keepalive": true,
}

type controlResult struct {
	OK    bool            `json:"ok"`
	Bot   string          `json:"bot,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Result hands the engine's answer to the request waiting for it.
func (c *ChatStream) Result(requestID string, r controlResult) {
	c.mu.Lock()
	ch := c.pending[requestID]
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- r:
		default:
		}
	}
}

// Control sends an action to the engine and waits for its answer.
func (c *ChatStream) Control(guild, bot, action string, params json.RawMessage) controlResult {
	if !Engine.Connected() {
		return controlResult{Error: "The engine is not connected."}
	}
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	requestID := hex.EncodeToString(id)
	ch := make(chan controlResult, 1)
	c.mu.Lock()
	c.pending[requestID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, requestID)
		c.mu.Unlock()
	}()
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	if err := Engine.write(map[string]any{"type": "bot.control", "request_id": requestID, "guild_id": guild, "bot": bot, "action": action, "params": params}); err != nil {
		return controlResult{Error: "Could not reach the engine."}
	}
	select {
	case r := <-ch:
		return r
	case <-time.After(controlTimeout):
		return controlResult{Error: "The engine did not answer in time."}
	}
}

// ServeControl is POST /live/control, guarded by the same LIVE_CHAT_SECRET as the chat stream.
func (c *ChatStream) ServeControl(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		Guild  string          `json:"guild"`
		Bot    string          `json:"bot"`
		Action string          `json:"action"`
		Params json.RawMessage `json:"params"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, controlBodyLimit))
	if err != nil || json.Unmarshal(raw, &body) != nil || !chatGuildID.MatchString(body.Guild) || len(strings.TrimSpace(body.Bot)) > 32 || !controlActions[body.Action] {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	result := c.Control(body.Guild, strings.TrimSpace(body.Bot), body.Action, body.Params)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}
