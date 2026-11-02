package match

import (
	"encoding/json"
	"fmt"
	"ranked-bedwars/src/types"
	"slices"
	"strings"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// ============================================================================

// VoidOptions is defined in package types (games.go).
type VoidOptions = types.VoidOptions

// Void cancels a game. A live game is stopped and its channels cleaned up; no
// stats were applied so none are touched. A game that was already completed
// has its results reversed (stats, ELO, ranks). Voiding twice is an error.
func (m *GameManager) Void(actor, guildID string, id uint, reason string, o VoidOptions) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "No reason provided"
	}
	if lg := m.liveIn(guildID, id); lg != nil {
		return m.voidLive(lg, actor, reason, o)
	}
	return m.voidStored(actor, guildID, id, reason)
}

func (m *GameManager) voidLive(lg *liveGame, actor, reason string, o VoidOptions) error {
	lg.mu.Lock()
	if lg.busy {
		lg.mu.Unlock()
		return ErrBusy
	}
	if lg.g.State == GameVoided {
		lg.mu.Unlock()
		return ErrAlreadyVoided
	}
	guildID, id := lg.g.GuildID, lg.g.ID
	lg.busy = true
	lg.mu.Unlock()

	if _, err := m.api().EndGame(actor, guildID, id, string(types.GameStatusVoided)); err != nil {
		lg.mu.Lock()
		lg.busy = false
		lg.mu.Unlock()
		return fmt.Errorf("end game: %w", err)
	}

	lg.mu.Lock()
	lg.g.End, lg.g.VoidReason, lg.g.VoidedBy = EndVoided, reason, actor
	m.transitionLocked(lg, GameVoided)
	lg.draft = nil
	lg.busy = false
	g := lg.g.clone()
	lg.mu.Unlock()

	m.logf("game %d voided by %s: %s", g.ID, actor, reason)
	m.sync(lg)
	m.notify(g, GameEvent{Kind: EventVoided, Reason: reason})
	if o.KeepChannels {
		g.Channels = GameChannels{}
	}
	m.finish(lg, g)
	return nil
}

// voidStored voids a game that is not live: one that was scored (reverse it),
// or an orphaned open row left by a crash.
func (m *GameManager) voidStored(actor, guildID string, id uint, reason string) error {
	rec, err := m.storedGame(guildID, id)
	if err != nil {
		return err
	}
	if rec.Status == string(types.GameStatusVoided) {
		return ErrAlreadyVoided
	}
	if rec.EndedAt != nil { // completed: take the results back first
		if err := m.reverseStored(actor, guildID, rec, nil); err != nil {
			return err
		}
	}
	if rec.EndedAt == nil {
		if _, err := m.api().EndGame(actor, guildID, id, string(types.GameStatusVoided)); err != nil {
			return err
		}
	} else if _, err := m.api().UpdateGame(actor, guildID, id, map[string]any{"status": string(types.GameStatusVoided)}); err != nil {
		return err
	}
	m.stampVoid(guildID, rec, reason, actor)
	return nil
}

// stampVoid records why/by whom in the game's runtime metadata (merged into what is there).
func (m *GameManager) stampVoid(guildID string, rec *models.GamesModel, reason, by string) {
	meta := map[string]any{}
	if !rec.RuntimeMetadata.IsEmpty() {
		_ = rec.RuntimeMetadata.Unmarshal(&meta)
	}
	meta["state"], meta["end"], meta["void_reason"], meta["voided_by"] = string(GameVoided), string(EndVoided), reason, by
	raw, err := json.Marshal(meta)
	if err != nil {
		return
	}
	if _, err := m.api().UpdateGame(sdk.SystemActor, guildID, rec.ID, map[string]any{"runtime_metadata": json.RawMessage(raw)}); err != nil {
		m.logf("stamp void on game %d: %v", rec.ID, err)
	}
}

// storedGame loads a game row from the database, 404 -> ErrGameNotFound.
func (m *GameManager) storedGame(guildID string, id uint) (*models.GamesModel, error) {
	rec, err := m.api().GetGame(guildID, id)
	if err != nil {
		if sdk.IsNotFound(err) {
			return nil, ErrGameNotFound
		}
		return nil, err
	}
	return rec, nil
}

// reverseStored reverses a completed game's results for playerIDs (nil = all)
// and repairs everything derived from them.
func (m *GameManager) reverseStored(actor, guildID string, rec *models.GamesModel, playerIDs []uint) error {
	holders := m.startingGrantHolders(guildID, rec, playerIDs)
	if _, err := m.api().ReverseGame(actor, guildID, rec.ID, playerIDs, false); err != nil {
		return fmt.Errorf("reverse results: %w", err)
	}
	m.deps.Elo.ReopenStartingGrants(actor, guildID, holders)
	m.afterReverse(actor, guildID, rec.ID, playerIDs)
	return nil
}

// startingGrantHolders lists the players (Discord IDs) whose starting-ELO grant was paid by this game's scoring.
// Call it BEFORE ReverseGame: the reversal takes the grant's ELO back (it is part of the game's change) but leaves
// the claim flag set, so without ReopenStartingGrants the player would lose the grant for good (a rescore would
// not repay it and /claimelo would refuse). A grant claimed by /claimelo comes before the game ended, so it is
// told apart by its timestamp (RecordStartingGrants stamps it right after the game completes).
func (m *GameManager) startingGrantHolders(guildID string, rec *models.GamesModel, playerIDs []uint) []string {
	if rec.EndedAt == nil {
		return nil
	}
	rows, err := m.api().Participants(guildID, rec.ID)
	if err != nil {
		m.logf("load participants of game %d: %v", rec.ID, err)
		return nil
	}
	var out []string
	for _, p := range rows {
		if p.Scoring != "ranked" || len(playerIDs) > 0 && !slices.Contains(playerIDs, p.PlayerID) {
			continue
		}
		uid, err := m.api().UserID(guildID, p.PlayerID)
		if err != nil {
			continue
		}
		st, err := m.deps.Elo.API().Stats(guildID, uid)
		if err == nil && st.StartingEloClaimed && st.StartingEloClaimedAt != nil && !st.StartingEloClaimedAt.Before(*rec.EndedAt) {
			out = append(out, uid)
		}
	}
	return out
}

// afterReverse repairs what ReverseGame can't: the rank names / roles of the players whose ELO changed.
func (m *GameManager) afterReverse(actor, guildID string, gameID uint, playerIDs []uint) {
	rows, err := m.api().Participants(guildID, gameID)
	if err != nil {
		m.logf("load participants of game %d: %v", gameID, err)
		return
	}
	for _, p := range rows {
		if len(playerIDs) > 0 && !slices.Contains(playerIDs, p.PlayerID) {
			continue
		}
		if uid, err := m.api().UserID(guildID, p.PlayerID); err == nil {
			if _, err := m.deps.Elo.SyncRank(actor, guildID, uid); err != nil {
				m.logf("rank sync %s: %v", uid, err)
			}
		}
	}
}

// ============================================================================
// Stored Games
