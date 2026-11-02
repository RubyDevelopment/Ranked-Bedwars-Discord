package match

import (
	"encoding/json"
	"errors"
	"fmt"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/queue"
	"slices"
	"strings"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// ============================================================================

// Operations on games that are not (or no longer) in the live registry: staff
// rescoring, partial voids, un-voiding, crash recovery and the watchdog. They
// work from the database rows, which are the truth for teams and stats.

// ErrRescoreIncomplete means a rescore took the old results back but could not
// apply the new ones; the game is open and ScoreStored finishes the job.
var ErrRescoreIncomplete = errors.New("old results were reversed but the new ones could not be applied; run the score again")

// loadStored rebuilds a Game from a database row: the archived runtime
// metadata when present, the participant rows always (they are the truth for
// teams and stats).
func (m *GameManager) loadStored(guildID string, rec *models.GamesModel) (Game, []models.GamePlayersModel, error) {
	rows, err := m.api().Participants(guildID, rec.ID)
	if err != nil {
		return Game{}, nil, err
	}
	var g Game
	if !rec.RuntimeMetadata.IsEmpty() {
		_ = rec.RuntimeMetadata.Unmarshal(&g)
	}
	names := map[string]string{}
	for _, p := range g.Players {
		names[p.UserID] = p.Username
	}
	g.ID, g.Number, g.Total, g.GuildID, g.SeasonID, g.Map = rec.ID, rec.GameNumber, rec.GuildNumber, guildID, rec.SeasonID, rec.MapName
	if g.Mode.Name == "" {
		modes, err := helpers.GuildMetaModes(guildID)
		if err != nil {
			return Game{}, nil, err
		}
		md := queue.FindMode(modes, rec.ModeName)
		if md == nil {
			return Game{}, nil, fmt.Errorf("mode %q of game %d is no longer configured", rec.ModeName, rec.ID)
		}
		g.Mode = *md
	}
	g.Players = nil
	var teams []string
	for _, r := range rows {
		uid, err := m.api().UserID(guildID, r.PlayerID)
		if err != nil {
			return Game{}, nil, fmt.Errorf("player %d: %w", r.PlayerID, err)
		}
		name := names[uid]
		if name == "" {
			name = uid
		}
		g.Players = append(g.Players, GamePlayer{
			UserID: uid, Username: name, PlayerID: r.PlayerID, Team: r.Team, Captain: r.IsCaptain,
			Kills: r.Kills, Deaths: r.Deaths, Assists: r.Assists, FinalKills: r.FinalKills,
			BedsDestroyed: r.BedsDestroyed, BedsLost: r.BedsLost, FirstBloods: r.FirstBloods,
		})
		if r.Team != teamUnassigned && !slices.Contains(teams, r.Team) {
			teams = append(teams, r.Team)
		}
	}
	slices.Sort(teams)
	g.Teams = teams
	return g, rows, nil
}

// storedWinner is the team named, else the team the stored rows say won.
func storedWinner(g Game, rows []models.GamePlayersModel, named string) (string, error) {
	if strings.TrimSpace(named) != "" {
		if t, ok := g.ResolveTeam(named); ok {
			return t, nil
		}
		return "", ErrUnknownTeam
	}
	for _, r := range rows {
		if r.IsWinner {
			return r.Team, nil
		}
	}
	return "", ErrNothingToRestore
}

func anyScored(rows []models.GamePlayersModel) bool {
	return slices.ContainsFunc(rows, func(r models.GamePlayersModel) bool { return r.Scoring != "" })
}

func (m *GameManager) commitStored(actor string, g Game, team, mvp string) (ScoreResult, error) {
	if !g.Mode.IsEloGainEnabled {
		if err := m.commitCasual(actor, g); err != nil {
			return ScoreResult{}, err
		}
		g.End, g.State = EndCasual, GameCleanup
		m.stampState(g)
		return ScoreResult{Game: g}, nil
	}
	results, err := m.commitRanked(actor, g, team, mvp, true)
	if err != nil {
		return ScoreResult{}, err
	}
	g.Winner, g.End, g.State, g.Results, g.MVP = team, EndRanked, GameCleanup, results, mvpOf(results)
	m.stampState(g)
	return ScoreResult{Game: g, Results: results}, nil
}

// stampState rewrites the game's runtime metadata to reflect g's final state.
func (m *GameManager) stampState(g Game) {
	raw, err := json.Marshal(g)
	if err != nil {
		return
	}
	if _, err := m.api().UpdateGame(sdk.SystemActor, g.GuildID, g.ID, map[string]any{"status": g.State.dbStatus(), "runtime_metadata": json.RawMessage(raw)}); err != nil {
		m.logf("stamp game %d: %v", g.ID, err)
	}
}

// Rescore changes the winner of a scored game: the old results are reversed
// and the new ones applied from the players' current ratings. mvp may be "".
// The winner must be named (the recorded one is what is being replaced).
func (m *GameManager) Rescore(actor, guildID string, id uint, winningTeam, mvp string) (ScoreResult, error) {
	if m.liveIn(guildID, id) != nil {
		return ScoreResult{}, ErrWrongState
	}
	rec, err := m.storedGame(guildID, id)
	if err != nil {
		return ScoreResult{}, err
	}
	g, rows, err := m.loadStored(guildID, rec)
	if err != nil {
		return ScoreResult{}, err
	}
	if !anyScored(rows) {
		return ScoreResult{}, ErrNotScored
	}
	team, err := storedWinner(g, rows, winningTeam)
	if err != nil {
		return ScoreResult{}, err
	}
	holders := m.startingGrantHolders(guildID, rec, nil)
	if _, err := m.api().ReverseGame(actor, guildID, id, nil, true); err != nil {
		return ScoreResult{}, fmt.Errorf("reverse results: %w", err)
	}
	m.deps.Elo.ReopenStartingGrants(actor, guildID, holders)
	m.afterReverse(actor, guildID, id, nil)
	res, err := m.commitStored(actor, g, team, mvp)
	if err != nil {
		return ScoreResult{}, fmt.Errorf("%w: %v", ErrRescoreIncomplete, err)
	}
	return res, nil
}

// PartialVoid reverses only the losing team of a scored game and leaves the
// winners' gains alone. winningTeam "" uses the recorded winner. It returns
// the losers' Discord IDs.
func (m *GameManager) PartialVoid(actor, guildID string, id uint, winningTeam string) ([]string, error) {
	if m.liveIn(guildID, id) != nil {
		return nil, ErrWrongState
	}
	rec, err := m.storedGame(guildID, id)
	if err != nil {
		return nil, err
	}
	g, rows, err := m.loadStored(guildID, rec)
	if err != nil {
		return nil, err
	}
	if !anyScored(rows) {
		return nil, ErrNotScored
	}
	team, err := storedWinner(g, rows, winningTeam)
	if err != nil {
		return nil, err
	}
	var ids []uint
	var losers []string
	for _, p := range g.Players {
		if !strings.EqualFold(p.Team, team) {
			ids = append(ids, p.PlayerID)
			losers = append(losers, p.UserID)
		}
	}
	if len(ids) == 0 {
		return nil, ErrUnknownTeam
	}
	holders := m.startingGrantHolders(guildID, rec, ids)
	if _, err := m.api().ReverseGame(actor, guildID, id, ids, false); err != nil {
		return nil, fmt.Errorf("reverse results: %w", err)
	}
	m.deps.Elo.ReopenStartingGrants(actor, guildID, holders)
	m.afterReverse(actor, guildID, id, ids)
	return losers, nil
}

// Unvoid brings back a game that was voided after it had been scored: it is
// reopened and scored again from the players' CURRENT ratings with the
// recorded winner (the old deltas can't be replayed once other games moved
// the ratings). A ranked game voided before it was ever scored has no
// recorded winner and nothing to restore.
func (m *GameManager) Unvoid(actor, guildID string, id uint) (ScoreResult, error) {
	if m.liveIn(guildID, id) != nil {
		return ScoreResult{}, ErrWrongState
	}
	rec, err := m.storedGame(guildID, id)
	if err != nil {
		return ScoreResult{}, err
	}
	if rec.Status != "voided" {
		return ScoreResult{}, ErrWrongState
	}
	g, rows, err := m.loadStored(guildID, rec)
	if err != nil {
		return ScoreResult{}, err
	}
	team := ""
	if g.Mode.IsEloGainEnabled {
		if team, err = storedWinner(g, rows, ""); err != nil {
			return ScoreResult{}, err // decided before reopening anything
		}
	}
	if _, err := m.api().ReverseGame(actor, guildID, id, nil, true); err != nil {
		return ScoreResult{}, fmt.Errorf("reopen: %w", err)
	}
	res, err := m.commitStored(actor, g, team, "")
	if err != nil {
		return ScoreResult{}, fmt.Errorf("%w: %v", ErrRescoreIncomplete, err)
	}
	return res, nil
}

// ============================================================================
// Recovery & watchdog
