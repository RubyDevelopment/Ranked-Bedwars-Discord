package engine

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/types"
	appLog "ranked-bedwars/src/utility"
	"slices"
	"strings"
	"time"
)

// ============================================================================

// Host is GameEffects.Host: it gives a game with final teams to the engine and
// waits for it to take it (or say it can't).
func (b *EngineBridge) Host(g match.Game, selectMap func([]match.MapOption, bool) (*match.MapSelection, error)) (bool, error) {
	if g.Custom != nil && !g.Custom.UseBot {
		return false, nil // staff chose to host this one by hand
	}
	if !b.Connected() || !helpers.GuildHasFeature(g.GuildID, "automated-hosting") {
		return false, nil // no engine, or the server's plan doesn't include bot hosting: host by hand
	}
	job, err := buildHostJob(g)
	if err != nil {
		return false, err
	}

	reply := make(chan hostReply, 1)
	b.mu.Lock()
	b.replies[g.ID] = reply
	if selectMap != nil {
		b.mapSelectors[g.ID] = selectMap
	}
	b.mu.Unlock()
	accepted := false
	defer func() {
		b.mu.Lock()
		delete(b.replies, g.ID)
		if !accepted {
			delete(b.mapSelectors, g.ID)
		}
		b.mu.Unlock()
	}()

	if err := b.Write(job); err != nil {
		return false, nil // lost the engine between the check and the send: host by hand
	}
	select {
	case r := <-reply:
		if r.err != nil {
			if r.void {
				return false, r.err // the caller voids the game
			}
			return false, nil
		}
		if !r.handled {
			return false, nil
		}
		accepted = true
		b.mu.Lock()
		b.hosted[g.ID] = g.GuildID
		b.mu.Unlock()
		return true, nil
	case <-time.After(engineAcceptTimeout):
		_ = b.Write(map[string]any{"type": "host.cancel", "game_id": g.ID, "reason": "The engine did not answer in time."})
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

	if err := b.Write(engineVerifyStart{Type: "verify.start", RequestID: id, GuildID: guildID, MCName: mcName, Code: code, Message: message}); err != nil {
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
	delete(b.mapSelectors, gameID)
	b.mu.Unlock()
	// Sent even when this process doesn't remember hosting it (it may have restarted since): the engine ignores unknown games.
	if b.Connected() {
		_ = b.Write(map[string]any{"type": "host.cancel", "game_id": gameID, "reason": reason})
	}
}

func buildHostJob(g match.Game) (*engineHostStart, error) {
	job := &engineHostStart{Type: "host.start", GameID: g.ID, GuildID: g.GuildID, GameNumber: g.Number, Mode: g.Mode.Name}

	for _, team := range g.Teams {
		t := engineHostTeam{Name: team}
		for _, p := range g.TeamPlayers(team) {
			view, err := database.Must().Player.Get(g.GuildID, p.UserID)
			if err != nil || view.Config == nil || strings.TrimSpace(view.Config.Username) == "" {
				return nil, fmt.Errorf("%s has no registered Minecraft name", p.Username)
			}
			t.Players = append(t.Players, hostPlayerOf(p, view.Config.Username, view.Config.Nickname))
		}
		job.Teams = append(job.Teams, t)
	}

	job.ModeSlot = modeGUISlot(g.Mode)

	if name := strings.TrimSpace(g.Map); name != "" && name != match.MapUndecided {
		m := &engineHostMap{Name: name}
		if maps, err := helpers.GuildMetaMaps(g.GuildID); err == nil {
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

// hostPlayerOf is how the engine knows a player: the Minecraft name they registered, and the other names they show up as
// (their display name and their nickname: a player who is nicked in game appears under it, and must not be kicked for it).
func hostPlayerOf(p match.GamePlayer, mcName string, nickname *string) engineHostPlayer {
	names := []string{}
	for _, name := range []string{p.Username, derefName(nickname)} {
		name = strings.TrimSpace(name)
		if name != "" && !strings.EqualFold(name, mcName) && !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) }) {
			names = append(names, name)
		}
	}
	return engineHostPlayer{ID: p.UserID, MCName: mcName, Names: names}
}

func derefName(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// syncPlayers answers the engine's players.sync: the roster's current Minecraft names. A player may have used /rename or
// /register since the game was made, and the party invites and the team check go by the new name.
func (b *EngineBridge) syncPlayers(gameID uint) {
	g, ok := match.Games.Get(gameID)
	if !ok {
		return
	}
	players := []engineHostPlayer{}
	for _, p := range g.Players {
		view, err := database.Must().Player.Get(g.GuildID, p.UserID)
		if err != nil || view.Config == nil || strings.TrimSpace(view.Config.Username) == "" {
			continue // keep the name the engine has
		}
		players = append(players, hostPlayerOf(p, view.Config.Username, view.Config.Nickname))
	}
	if err := b.Write(map[string]any{"type": "players.synced", "game_id": gameID, "players": players}); err != nil {
		b.logf("send player names for game %d: %v", gameID, err)
	}
}

// RefreshNames pushes the current Minecraft names to every game the engine hosts that the member plays in, as soon as they
// register, rename or change their nickname. Without it the engine only asks every so often, so a player who fixed their
// name while the party was being invited kept being invited under the old one.
func (b *EngineBridge) RefreshNames(guildID, userID string) {
	if !b.Connected() {
		return
	}
	b.mu.Lock()
	var ids []uint
	for id, guild := range b.hosted {
		if guild == guildID {
			ids = append(ids, id)
		}
	}
	b.mu.Unlock()
	for _, id := range ids {
		if g, ok := match.Games.Get(id); ok {
			if _, plays := g.Player(userID); plays {
				b.syncPlayers(id)
			}
		}
	}
}

// HostAction runs a host button (party_warp, game_warp, party_invite, pg_kick) as the game's bot and waits for the answer.
func (b *EngineBridge) HostAction(ctx context.Context, gameID uint, action, mcName, target string) (match.HostActionResult, error) {
	if !b.Connected() {
		return match.HostActionResult{}, ErrEngineOffline
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return match.HostActionResult{}, err
	}
	id := hex.EncodeToString(raw)
	reply := make(chan match.HostActionResult, 1)
	b.mu.Lock()
	b.actions[id] = reply
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.actions, id)
		b.mu.Unlock()
	}()
	if err := b.Write(map[string]any{"type": "host.action", "request_id": id, "game_id": gameID, "action": action, "mc_name": mcName, "target": target}); err != nil {
		return match.HostActionResult{}, ErrEngineOffline
	}
	select {
	case r := <-reply:
		return r, nil
	case <-ctx.Done():
		return match.HostActionResult{}, ctx.Err()
	case <-time.After(15 * time.Second):
		return match.HostActionResult{}, errors.New("the engine did not answer in time")
	}
}

func modeGUISlot(mode types.Mode) *int {
	if mode.GUISlot == nil || *mode.GUISlot < 0 {
		return nil
	}
	slot := *mode.GUISlot
	return &slot
}

// applyTeamColors gives each team the colour its map is configured with for the game's mode (Team A the first, and so on).
// The engine still lets players join any colour as long as they stay together; this is what it expects first.
func applyTeamColors(job *engineHostStart, g match.Game) {
	maps, err := helpers.GuildMetaMaps(g.GuildID)
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
func modeSettings(g match.Game) json.RawMessage {
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
