package engine

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/types"
	appLog "ranked-bedwars/src/utility"
)

const (
	timelineGameStarted = "game_started"
	timelineGameEnded   = "game_ended"
	timelineKill        = "kill"
	timelineBed         = "bed"
	timelineEliminated  = "eliminated"
	timelineStat        = "stat"
	timelinePhase       = "phase"
)

type timelineEventRequest struct {
	Kind       string          `json:"kind"`
	OccurredAt time.Time       `json:"occurred_at"`
	Actor      *string         `json:"actor,omitempty"`
	Target     *string         `json:"target,omitempty"`
	Team       *string         `json:"team,omitempty"`
	Final      *bool           `json:"final,omitempty"`
	FirstBlood *bool           `json:"first_blood,omitempty"`
	Phase      *int            `json:"phase,omitempty"`
	PhaseName  *string         `json:"phase_name,omitempty"`
	Stat       *string         `json:"stat,omitempty"`
	Amount     *float64        `json:"amount,omitempty"`
	Total      *float64        `json:"total,omitempty"`
	Winner     *string         `json:"winner,omitempty"`
	Inferred   *bool           `json:"inferred,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
}

// ============================================================================
// Engine bridge
// ============================================================================

// The Minecraft side of a game (bots, party, map, teams, tracking) runs in the
// separate Ranked-Bedwars-Engine process. It dials this bot over one WebSocket
// (the frames are documented in the engine's src/protocol.ts), is handed a game
// once its teams are final, and reports back until the match is over:
//
//	host.start → host.accepted → host.status/host.embed... → game.started → game.event... → game.ended
//
// A game the engine can't take (no bot free, no server profile, not connected)
// falls back to manual hosting: staff score it with /score as before.

const (
	engineAcceptTimeout = 30 * time.Second
	enginePingEvery     = 20 * time.Second
)

// Frames. Only the fields the bot reads or writes are listed.

type engineHostPlayer struct {
	ID     string   `json:"id"`
	MCName string   `json:"mc_name"`
	Names  []string `json:"names,omitempty"`
}

type engineHostTeam struct {
	Name    string             `json:"name"`
	Color   string             `json:"color,omitempty"` // the in-game colour the map gives this team in this mode
	Players []engineHostPlayer `json:"players"`
}

type engineHostMap struct {
	Name     string `json:"name"`
	GUISlot  *int   `json:"gui_slot,omitempty"`
	StableID string `json:"stable_id,omitempty"`
}

type engineHostStart struct {
	Type       string           `json:"type"`
	GameID     uint             `json:"game_id"`
	GuildID    string           `json:"guild_id"`
	GameNumber int              `json:"game_number"`
	Mode       string           `json:"mode"`
	ModeSlot   *int             `json:"mode_slot,omitempty"`
	Map        *engineHostMap   `json:"map,omitempty"`
	Settings   json.RawMessage  `json:"settings,omitempty"` // the mode's game settings preset
	MapHeight  *int             `json:"map_height,omitempty"`
	Teams      []engineHostTeam `json:"teams"`
}

type engineInbound struct {
	Type        string               `json:"type"`
	GameID      uint                 `json:"game_id"`
	Bot         string               `json:"bot"`
	Reason      string               `json:"reason"`
	Code        string               `json:"code"`
	Then        string               `json:"then"`      // host.failed: the profile's failure policy, "manual" or "void" (empty: the old rule)
	Embed       string               `json:"embed"`     // host.failed: embed to post, a built-in key or "custom:<id>"
	StepType    string               `json:"step_type"` // host.failed: the type of the pipeline step that failed
	Stage       string               `json:"stage"`
	Detail      string               `json:"detail"`
	Waiting     []string             `json:"waiting_for"`
	Winner      string               `json:"winner"`
	Inferred    bool                 `json:"inferred"`
	Kind        string               `json:"kind"`
	Lines       []match.GameChatLine `json:"lines"`
	Missing     []string             `json:"missing"`
	PlayerID    string               `json:"id"` // host.party.recovered: the player who joined after all
	Voter       string               `json:"voter"`  // host.map.vote: the Discord id of the player who voted in the party chat
	Number      int                  `json:"number"` // host.map.vote: the map number they wrote
	Maps        []match.MapOption    `json:"maps"`
	MapMenuRead bool                 `json:"map_menu_read"`
	Map         *match.MapSelection  `json:"map"`

	// chat.line / chat.bots: live chat for the dashboard
	GuildID string   `json:"guild_id"`
	Text    string   `json:"text"`
	Motd    string   `json:"motd"`
	At      int64    `json:"at"`
	Bots    []string `json:"bots"`

	// bot.event / bot.result: the live console (see chat_stream_helper.go)
	Data  json.RawMessage `json:"data"`
	OK    bool            `json:"ok"`
	Error string          `json:"error"`

	// verify.result / host.action.result
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Teams     []struct {
		Name string   `json:"name"`
		IDs  []string `json:"ids"`
	} `json:"teams"` // host.regroup: the teams as they stood
	Command  string `json:"command"`  // host.action.result: what the bot said in chat
	Cooldown int    `json:"cooldown"` // host.action.result: seconds until the buttons work again

	// host.failed: where the bot got stuck, what it last saw, and what that probably means.
	Step      string `json:"step"`
	Diagnosis string `json:"diagnosis"`
	Hint      string `json:"hint"`

	// host.embed: which configured embed to post, with its extra variables and who joined / is still missing.
	Key        string            `json:"key"`
	Vars       map[string]string `json:"vars"`
	JoinedIDs  []string          `json:"joined_ids"`
	WaitingIDs []string          `json:"waiting_ids"`
	InvitedIDs []string          `json:"invited_ids"`

	// game.event
	Killer     string  `json:"killer"`
	Victim     string  `json:"victim"`
	Final      bool    `json:"final"`
	FirstBlood bool    `json:"first_blood"`
	Team       string  `json:"team"`
	Destroyer  string  `json:"destroyer"`
	Phase      int     `json:"phase"`
	PhaseName  string  `json:"phase_name"`
	Stat       string  `json:"stat"`
	StatPlayer string  `json:"player"`
	StatAmount float64 `json:"amount"`
	StatTotal  float64 `json:"total"`

	Players []struct {
		ID            string             `json:"id"`
		Kills         int                `json:"kills"`
		Deaths        int                `json:"deaths"`
		Assists       int                `json:"assists"`
		FinalKills    int                `json:"final_kills"`
		BedsDestroyed int                `json:"beds_destroyed"`
		BedsLost      int                `json:"beds_lost"`
		FirstBloods   int                `json:"first_bloods"`
		Custom        map[string]float64 `json:"custom"`
		Rounds        int                `json:"rounds"`  // host.party.stalled: invite rounds they were still missing for
		Offline       bool               `json:"offline"` // host.party.stalled: the server last said they are not online
	} `json:"players"`
}

// engineVerifyStart asks the engine to deliver a registration code to a Minecraft player (see Verify).
type engineVerifyStart struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	GuildID   string `json:"guild_id"`
	MCName    string `json:"mc_name"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

// VerifyResult is defined in package types (engine.go).
type VerifyResult = types.VerifyResult

// hostReply is the engine's first answer to a host.start.
type hostReply struct {
	handled bool
	bot     string
	err     error
	void    bool // err voids the game instead of falling back to hosting by hand
}

// EngineBridge owns the engine connection and the games it is hosting.
type EngineBridge struct {
	secret string

	mu           sync.Mutex
	conn         *websocket.Conn
	writeMu      sync.Mutex
	replies      map[uint]chan hostReply // games waiting for host.accepted
	prepared     map[uint]*preparedHost  // games whose bot joins while the teams are picked: what the engine said so far (see Prepare)
	hosted      map[uint]string         // games the engine runs -> guild
	resuming     map[uint]string         // matches found open after a restart that the engine has not yet said it still runs -> guild
	mapSelectors map[uint]func([]match.MapOption, bool) (*match.MapSelection, error)
	verifies     map[string]chan verifyReply            // registration code deliveries waiting for verify.result
	actions      map[string]chan match.HostActionResult // host buttons waiting for host.action.result
	upgrader     websocket.Upgrader
}

// Engine is the process-wide bridge.
var Engine = &EngineBridge{
	replies:      map[uint]chan hostReply{},
	prepared:     map[uint]*preparedHost{},
	hosted:      map[uint]string{},
	resuming:     map[uint]string{},
	mapSelectors: map[uint]func([]match.MapOption, bool) (*match.MapSelection, error){},
	verifies:     map[string]chan verifyReply{},
	actions:      map[string]chan match.HostActionResult{},
	upgrader:     websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}, // authenticated by the secret, not the origin
}

func (b *EngineBridge) logf(format string, a ...any) {
	appLog.EngineLogger.Info("[EngineBridge] " + fmt.Sprintf(format, a...))
}

// Connected reports whether an engine is attached.
func (b *EngineBridge) Connected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn != nil
}

// ============================================================================
// Serving the engine
// ============================================================================

// Listen serves the bridge on addr (":8765") until ctx ends. Without a secret
// it refuses to start: anyone who can reach the port could otherwise host games.
func (b *EngineBridge) Listen(ctx context.Context, addr, secret string) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("ENGINE_BRIDGE_SECRET must be set to serve the engine bridge")
	}
	b.secret = secret

	mux := http.NewServeMux()
	mux.HandleFunc("/engine/ws", b.serveWS)
	mux.Handle("/live/chat", LiveChat)                          // the dashboard's live bot chat (LIVE_CHAT_SECRET)
	mux.HandleFunc("/live/control", LiveChat.ServeControl)      // the dashboard's live console: actions on a bot (same secret)
	mux.HandleFunc("/live/appearance", helpers.ServeAppearance) // the dashboard saved a server's bot name, icon or banner (same secret)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	b.logf("bridge listening on %s", addr)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (b *EngineBridge) serveWS(w http.ResponseWriter, r *http.Request) {
	b.logf("engine connection attempt from %s", r.RemoteAddr)
	given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(given), []byte(b.secret)) != 1 {
		b.logf("engine from %s rejected: wrong ENGINE_BRIDGE_SECRET", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	b.mu.Lock()
	old := b.conn
	b.conn = conn
	b.mu.Unlock()
	if old != nil {
		_ = old.Close() // a restarted engine replaces its stale connection
	}
	b.logf("engine connected")
	LiveChat.engineUp()

	stop := make(chan struct{})
	go b.ping(conn, stop)
	defer func() {
		close(stop)
		_ = conn.Close()
		b.mu.Lock()
		current := b.conn == conn
		if current {
			b.conn = nil
		}
		b.mu.Unlock()
		if current {
			b.logf("engine disconnected")
			LiveChat.engineDown()
			b.engineLost()
		}
	}()

	// Frames about a game (its events, its scoring, the Discord posts they cause) are handled one after another, in order, on a worker. Live
	// console traffic is answered on the spot: a game event that waits for the database must never hold back the chat lines behind it.
	queue := make(chan engineInbound, engineQueueSize)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for msg := range queue {
			b.handle(msg)
		}
	}()
	defer func() {
		close(queue)
		<-workerDone
	}()

	_ = conn.SetReadDeadline(time.Now().Add(3 * enginePingEvery))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(3 * enginePingEvery)) })
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * enginePingEvery))
		var msg engineInbound
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if liveFrame(msg.Type) {
			b.handle(msg)
			continue
		}
		queue <- msg
	}
}

// liveFrame says whether an engine frame is live console traffic: cheap, independent of any game, and wanted at once.
func liveFrame(kind string) bool {
	switch kind {
	case "chat.line", "chat.bots", "bot.event", "bot.result":
		return true
	}
	return false
}

func (b *EngineBridge) ping(conn *websocket.Conn, stop <-chan struct{}) {
	t := time.NewTicker(enginePingEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			b.writeMu.Lock()
			err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			b.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (b *EngineBridge) Write(v any) error {
	b.mu.Lock()
	conn := b.conn
	b.mu.Unlock()
	if conn == nil {
		return errors.New("engine not connected")
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	return conn.WriteJSON(v)
}

// engineLost tells the games the engine was running that they are on their own now.
func (b *EngineBridge) engineLost() {
	b.mu.Lock()
	games := b.hosted
	b.hosted = map[uint]string{}
	b.prepared = map[uint]*preparedHost{}
	b.mapSelectors = map[uint]func([]match.MapOption, bool) (*match.MapSelection, error){}
	for id, ch := range b.replies {
		ch <- hostReply{err: errors.New("the engine disconnected")}
		delete(b.replies, id)
	}
	for id, ch := range b.verifies {
		ch <- verifyReply{err: ErrEngineOffline}
		delete(b.verifies, id)
	}
	for id, ch := range b.actions {
		ch <- match.HostActionResult{Error: "The engine disconnected."}
		delete(b.actions, id)
	}
	b.mu.Unlock()
	for id := range games {
		match.Games.Notify(id, match.GameEvent{Kind: match.EventTrackingLost, Reason: "The engine disconnected."})
	}
}

// ============================================================================
// Hosting a game

func init() {
	match.HostEngine = Engine
	helpers.OnProfileSynced = Engine.RefreshNames
}

// Control asks the engine to run a console action and waits for its answer.
func (b *EngineBridge) Control(guild, bot, action string, params json.RawMessage) match.ControlResult {
	return LiveChat.Control(guild, bot, action, params)
}
