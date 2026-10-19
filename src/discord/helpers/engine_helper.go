package helpers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	"github.com/gorilla/websocket"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	appLog "ranked-bedwars/src/utility"
)

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
	Type     string         `json:"type"`
	GameID   uint           `json:"game_id"`
	Bot      string         `json:"bot"`
	Reason   string         `json:"reason"`
	Code     string         `json:"code"`
	Stage    string         `json:"stage"`
	Detail   string         `json:"detail"`
	Waiting  []string       `json:"waiting_for"`
	Winner   string         `json:"winner"`
	Inferred bool           `json:"inferred"`
	Kind     string         `json:"kind"`
	Lines    []GameChatLine `json:"lines"`
	Missing  []string       `json:"missing"`

	// verify.result
	RequestID string `json:"request_id"`
	Status    string `json:"status"`

	// host.failed: where the bot got stuck, what it last saw, and what that probably means.
	Step      string `json:"step"`
	Diagnosis string `json:"diagnosis"`
	Hint      string `json:"hint"`

	// host.embed: which configured embed to post, with its extra variables and who joined / is still missing.
	Key        string            `json:"key"`
	Vars       map[string]string `json:"vars"`
	JoinedIDs  []string          `json:"joined_ids"`
	WaitingIDs []string          `json:"waiting_ids"`

	// game.event
	Killer     string  `json:"killer"`
	Victim     string  `json:"victim"`
	Final      bool    `json:"final"`
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
		FinalKills    int                `json:"final_kills"`
		BedsDestroyed int                `json:"beds_destroyed"`
		BedsLost      int                `json:"beds_lost"`
		FirstBloods   int                `json:"first_bloods"`
		Custom        map[string]float64 `json:"custom"`
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
}

// EngineBridge owns the engine connection and the games it is hosting.
type EngineBridge struct {
	secret string

	mu       sync.Mutex
	conn     *websocket.Conn
	writeMu  sync.Mutex
	replies  map[uint]chan hostReply     // games waiting for host.accepted
	hosted   map[uint]string             // games the engine runs -> guild
	verifies map[string]chan verifyReply // registration code deliveries waiting for verify.result
	upgrader websocket.Upgrader
}

// Engine is the process-wide bridge.
var Engine = &EngineBridge{
	replies:  map[uint]chan hostReply{},
	hosted:   map[uint]string{},
	verifies: map[string]chan verifyReply{},
	upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}, // authenticated by the secret, not the origin
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
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	b.logf("listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (b *EngineBridge) serveWS(w http.ResponseWriter, r *http.Request) {
	given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(given), []byte(b.secret)) != 1 {
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
			b.engineLost()
		}
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
		b.handle(msg)
	}
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

func (b *EngineBridge) write(v any) error {
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
	for id, ch := range b.replies {
		ch <- hostReply{err: errors.New("the engine disconnected")}
		delete(b.replies, id)
	}
	for id, ch := range b.verifies {
		ch <- verifyReply{err: ErrEngineOffline}
		delete(b.verifies, id)
	}
	b.mu.Unlock()
	for id := range games {
		Games.Notify(id, GameEvent{Kind: EventTrackingLost, Reason: "The engine disconnected."})
	}
}

// ============================================================================
// Hosting a game
// ============================================================================

// Host is GameEffects.Host: it gives a game with final teams to the engine and
// waits for it to take it (or say it can't).
func (b *EngineBridge) Host(g Game) (bool, error) {
	if g.Custom != nil && !g.Custom.UseBot {
		return false, nil // staff chose to host this one by hand
	}
	if !b.Connected() || !GuildHasFeature(g.GuildID, "automated-hosting") {
		return false, nil // no engine, or the server's plan doesn't include bot hosting: host by hand
	}
	job, err := buildHostJob(g)
	if err != nil {
		return false, err
	}

	reply := make(chan hostReply, 1)
	b.mu.Lock()
	b.replies[g.ID] = reply
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.replies, g.ID)
		b.mu.Unlock()
	}()

	if err := b.write(job); err != nil {
		return false, nil // lost the engine between the check and the send: host by hand
	}
	select {
	case r := <-reply:
		if r.err != nil {
			return false, nil
		}
		if !r.handled {
			return false, nil
		}
		b.mu.Lock()
		b.hosted[g.ID] = g.GuildID
		b.mu.Unlock()
		return true, nil
	case <-time.After(engineAcceptTimeout):
		_ = b.write(map[string]any{"type": "host.cancel", "game_id": g.ID, "reason": "The engine did not answer in time."})
		return false, nil
	}
}

// ErrEngineOffline is returned when no engine is attached (or it dropped mid-request).
var ErrEngineOffline = errors.New("engine not connected")

type verifyReply struct {
	result VerifyResult
	err    error
}

// Verify has the engine put code (inside message) into the party chat of the Minecraft player mcName: a bot
// invites them, waits for them to join and speaks. It blocks until the engine answers or ctx ends.
func (b *EngineBridge) Verify(ctx context.Context, guildID, mcName, code, message string) (VerifyResult, error) {
	if !b.Connected() {
		return VerifyResult{}, ErrEngineOffline
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return VerifyResult{}, err
	}
	id := hex.EncodeToString(raw)

	reply := make(chan verifyReply, 1)
	b.mu.Lock()
	b.verifies[id] = reply
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.verifies, id)
		b.mu.Unlock()
	}()

	if err := b.write(engineVerifyStart{Type: "verify.start", RequestID: id, GuildID: guildID, MCName: mcName, Code: code, Message: message}); err != nil {
		return VerifyResult{}, ErrEngineOffline
	}
	select {
	case r := <-reply:
		return r.result, r.err
	case <-ctx.Done():
		return VerifyResult{}, ctx.Err()
	}
}

// Cancel tells the engine to drop a game (it was voided, or scored by hand).
func (b *EngineBridge) Cancel(gameID uint, reason string) {
	b.mu.Lock()
	delete(b.hosted, gameID)
	b.mu.Unlock()
	// Sent even when this process doesn't remember hosting it (it may have restarted since): the engine ignores unknown games.
	if b.Connected() {
		_ = b.write(map[string]any{"type": "host.cancel", "game_id": gameID, "reason": reason})
	}
}

func buildHostJob(g Game) (*engineHostStart, error) {
	job := &engineHostStart{Type: "host.start", GameID: g.ID, GuildID: g.GuildID, GameNumber: g.Number, Mode: g.Mode.Name}

	for _, team := range g.Teams {
		t := engineHostTeam{Name: team}
		for _, p := range g.TeamPlayers(team) {
			view, err := database.Must().Player.Get(g.GuildID, p.UserID)
			if err != nil || view.Config == nil || strings.TrimSpace(view.Config.Username) == "" {
				return nil, fmt.Errorf("%s has no registered Minecraft name", p.Username)
			}
			names := []string{}
			if p.Username != "" && !strings.EqualFold(p.Username, view.Config.Username) {
				names = append(names, p.Username)
			}
			t.Players = append(t.Players, engineHostPlayer{ID: p.UserID, MCName: view.Config.Username, Names: names})
		}
		job.Teams = append(job.Teams, t)
	}

	if g.Mode.GUISlot > 0 { // the mode's slot in the server's mode menu
		slot := g.Mode.GUISlot
		job.ModeSlot = &slot
	}

	if name := strings.TrimSpace(g.Map); name != "" && name != MapUndecided {
		m := &engineHostMap{Name: name}
		if maps, err := GuildMetaMaps(g.GuildID); err == nil {
			for _, cfg := range maps {
				if strings.EqualFold(strings.TrimSpace(cfg.Name), name) {
					slot := cfg.GUISlot
					m.GUISlot, m.StableID = &slot, cfg.StableID
					if cfg.MapHeight > 0 {
						height := cfg.MapHeight
						job.MapHeight = &height
					}
					break
				}
			}
		}
		job.Map = m
		applyTeamColors(job, g)
	}
	job.Settings = modeSettings(g)
	return job, nil
}

// applyTeamColors gives each team the colour its map is configured with for the game's mode (Team A the first, and so on).
// The engine still lets players join any colour as long as they stay together; this is what it expects first.
func applyTeamColors(job *engineHostStart, g Game) {
	maps, err := GuildMetaMaps(g.GuildID)
	if err != nil {
		return
	}
	for _, cfg := range maps {
		if !strings.EqualFold(strings.TrimSpace(cfg.Name), strings.TrimSpace(g.Map)) {
			continue
		}
		colors := cfg.TeamsFor(g.Mode.Name)
		if len(colors) < len(job.Teams) {
			appLog.EngineLogger.Warn(fmt.Sprintf("[EngineBridge] map %q has %d team colours for mode %q, game %d has %d teams", cfg.Name, len(colors), g.Mode.Name, g.ID, len(job.Teams)))
		}
		for i := range job.Teams {
			if i < len(colors) {
				job.Teams[i].Color = colors[i]
			}
		}
		return
	}
}

// modeSettings is the game settings preset attached to the game's mode (its settings_id), as stored.
func modeSettings(g Game) json.RawMessage {
	if g.Mode.SettingsID == nil {
		return nil
	}
	raw, err := database.Must().GameConfig.Field(g.GuildID, "settings")
	if err != nil || len(raw) == 0 {
		return nil
	}
	var presets []struct {
		ID     uint            `json:"id"`
		Config json.RawMessage `json:"config"`
	}
	if json.Unmarshal(raw, &presets) != nil {
		return nil
	}
	for _, p := range presets {
		if p.ID == *g.Mode.SettingsID {
			return p.Config
		}
	}
	return nil
}

// ============================================================================
// Frames from the engine
// ============================================================================

func (b *EngineBridge) handle(msg engineInbound) {
	switch msg.Type {
	case "hello":
		b.logf("engine ready")

	case "host.accepted":
		b.reply(msg.GameID, hostReply{handled: true, bot: msg.Bot})
		if g, ok := Games.Get(msg.GameID); ok {
			_ = Games.SetBot(g.ID, msg.Bot)
		}

	case "host.failed":
		b.hostFailed(msg)

	case "verify.result":
		b.mu.Lock()
		ch := b.verifies[msg.RequestID]
		b.mu.Unlock()
		if ch != nil {
			select {
			case ch <- verifyReply{result: VerifyResult{Status: msg.Status, Reason: msg.Reason}}:
			default:
			}
		}

	case "host.status":
		Games.Notify(msg.GameID, GameEvent{Kind: EventHostStatus, Stage: msg.Stage, Reason: msg.Detail, Waiting: msg.Waiting})

	case "host.embed":
		Games.Notify(msg.GameID, GameEvent{Kind: EventEmbed, Key: msg.Key, Vars: msg.Vars, JoinedIDs: msg.JoinedIDs, WaitingIDs: msg.WaitingIDs})

	case "game.event":
		Games.Notify(msg.GameID, GameEvent{Kind: EventMatch, Match: &MatchEvent{
			Kind: msg.Kind, Killer: msg.Killer, Victim: msg.Victim, Final: msg.Final, Team: msg.Team, Destroyer: msg.Destroyer,
			Phase: msg.Phase, PhaseName: msg.PhaseName, Stat: msg.Stat, Player: msg.StatPlayer, Amount: msg.StatAmount, Total: msg.StatTotal,
		}})

	case "game.started":
		if err := Games.BeginMatch(msg.GameID); err != nil && !errors.Is(err, ErrWrongState) {
			b.logf("begin match %d: %v", msg.GameID, err)
		}

	case "game.chat":
		Games.StashChat(msg.GameID, msg.Lines)

	case "game.ended":
		b.gameEnded(msg)

	case "game.tracking_lost":
		b.mu.Lock()
		delete(b.hosted, msg.GameID)
		b.mu.Unlock()
		Games.Notify(msg.GameID, GameEvent{Kind: EventTrackingLost, Reason: msg.Reason})
	}
}

func (b *EngineBridge) reply(gameID uint, r hostReply) {
	b.mu.Lock()
	ch := b.replies[gameID]
	b.mu.Unlock()
	if ch != nil {
		select {
		case ch <- r:
		default:
		}
	}
}

// hostFailed: before the engine accepted the game, "no bot / no server" means host by hand;
// anything else (or any failure after accepting) voids the game with the engine's reason.
func (b *EngineBridge) hostFailed(msg engineInbound) {
	b.mu.Lock()
	_, waiting := b.replies[msg.GameID]
	delete(b.hosted, msg.GameID)
	b.mu.Unlock()

	if waiting && (msg.Code == "no_bot" || msg.Code == "no_server") {
		b.reply(msg.GameID, hostReply{handled: false})
		return
	}
	if waiting {
		// Not accepted yet: Host falls back to manual hosting. Voiding too would race with staff hosting it.
		b.reply(msg.GameID, hostReply{err: errors.New(msg.Reason)})
		return
	}
	if msg.Code == "cancelled" {
		return // we asked for it
	}
	reason := strings.TrimSpace(msg.Reason)
	if reason == "" {
		reason = "The hosting bot failed."
	}
	if g, ok := Games.Get(msg.GameID); ok {
		if msg.Diagnosis != "" || msg.Hint != "" || msg.Step != "" {
			Games.Notify(g.ID, GameEvent{Kind: EventEmbed, Key: "hostProblem", Vars: map[string]string{
				"reason": reason, "step": msg.Step, "diagnosis": clip(msg.Diagnosis, 900), "hint": msg.Hint,
			}})
		}
		switch msg.Code {
		case "party":
			Games.Notify(g.ID, GameEvent{Kind: EventEmbed, Key: "partyTimeout", WaitingIDs: msg.Missing})
			b.strikeNoShows(g, msg.Missing)
		case "teams":
			Games.Notify(g.ID, GameEvent{Kind: EventEmbed, Key: "teamTimeout", WaitingIDs: msg.Missing})
		}
		_ = Games.Void(actorSystem, g.GuildID, g.ID, reason, VoidOptions{})
	}
}

// strikeNoShows gives a strike to every player who never joined the party, when the mode auto-strikes.
func (b *EngineBridge) strikeNoShows(g Game, ids []string) {
	if !g.Mode.IsAutoStrikingEnabled || g.Custom != nil {
		return
	}
	for _, id := range ids {
		p, ok := g.Player(id)
		if !ok || p.PlayerID == 0 {
			continue
		}
		_, err := database.Must().Strikes.Create(actorSystem, g.GuildID, sdk.UintToString(p.PlayerID), sdk.NewStrike{
			StaffID: "000000000000000000",
			Reason:  fmt.Sprintf("Failed to join the party in time (automatic) - game #%d", g.Number),
		})
		if err != nil {
			b.logf("no-show strike for %s in game %d: %v", id, g.ID, err)
		}
	}
}

// gameEnded records what the engine tracked and scores the game for the team that won.
func (b *EngineBridge) gameEnded(msg engineInbound) {
	b.mu.Lock()
	delete(b.hosted, msg.GameID)
	b.mu.Unlock()

	g, ok := Games.Get(msg.GameID)
	if !ok {
		return
	}
	for _, p := range msg.Players {
		custom := make(map[string]int, len(p.Custom))
		for id, n := range p.Custom {
			custom[id] = int(math.Round(n))
		}
		_ = Games.AddStats(g.ID, p.ID, GamePlayer{
			Kills: p.Kills, Deaths: p.Deaths, FinalKills: p.FinalKills,
			BedsDestroyed: p.BedsDestroyed, BedsLost: p.BedsLost, FirstBloods: p.FirstBloods, Custom: custom,
		})
	}
	if strings.TrimSpace(msg.Winner) == "" {
		Games.Notify(g.ID, GameEvent{Kind: EventTrackingLost, Reason: "The match ended but the bot couldn't tell who won."})
		return
	}
	Games.Notify(g.ID, GameEvent{Kind: EventMatchEnded, Vars: map[string]string{"winner": msg.Winner}})
	if _, err := Games.Score(actorSystem, g.GuildID, g.ID, msg.Winner, ScoreOptions{}); err != nil {
		b.logf("score game %d for %q: %v", g.ID, msg.Winner, err)
		Games.Notify(g.ID, GameEvent{Kind: EventTrackingLost, Reason: "The result couldn't be scored automatically: " + err.Error()})
	}
}
