package match

import (
	"errors"
	"fmt"
	"ranked-bedwars/src/discord/helpers/ratings"
	"ranked-bedwars/src/types"
	"slices"
	"strings"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// ============================================================================

// ResolveTeam maps user input ("a", "Team B") to a team name of the game.
func (g Game) ResolveTeam(input string) (string, bool) {
	if i := TeamIndex(input, len(g.Teams)); i >= 0 {
		return g.Teams[i], true
	}
	for _, t := range g.Teams {
		if strings.EqualFold(strings.TrimSpace(input), t) {
			return t, true
		}
	}
	return "", false
}

// SubmitOutcome is what Submit did.
type SubmitOutcome struct {
	End  MatchEnd // EndStaffPending or EndCasual
	Team string   // the team the submitter claimed won (ranked)
	Game Game
}

// Submit is a player reporting that the match is over (with their proof). In
// a ranked mode the claim goes to staff scoring (their team is the claimed
// winner); in a casual mode the match just ends. It is refused before the
// minimum match length, twice, and from non-participants.
func (m *GameManager) Submit(id uint, userID string) (SubmitOutcome, error) {
	lg := m.live(id)
	if lg == nil {
		return SubmitOutcome{}, ErrGameNotFound
	}
	lg.mu.Lock()
	g := lg.g
	p, ok := g.Player(userID)
	switch {
	case !ok:
		lg.mu.Unlock()
		return SubmitOutcome{}, ErrNotParticipant
	case g.State != GameInGame:
		lg.mu.Unlock()
		return SubmitOutcome{}, ErrWrongState
	case lg.submitted || lg.busy:
		lg.mu.Unlock()
		return SubmitOutcome{}, ErrAlreadySubmitted
	}
	if left := m.deps.SubmitMin - m.deps.Now().Sub(g.StartedAt); left > 0 {
		lg.mu.Unlock()
		return SubmitOutcome{}, &TooEarlyError{left}
	}
	if !g.Mode.Rated() {
		lg.submitted = true
		lg.mu.Unlock()
		snap, err := m.finishCasual(lg, sdk.SystemActor)
		return SubmitOutcome{End: EndCasual, Game: snap}, err
	}
	team, ok := g.ResolveTeam(p.Team)
	if !ok {
		lg.mu.Unlock()
		return SubmitOutcome{}, ErrUnknownTeam
	}
	lg.submitted = true
	snap := g.clone()
	lg.mu.Unlock()

	category := ""
	if fn := m.fx().StaffScoring; fn != nil {
		var err error
		if category, err = fn(snap); err != nil {
			lg.mu.Lock()
			lg.submitted = false // a transient Discord failure must not block a retry
			lg.mu.Unlock()
			return SubmitOutcome{}, fmt.Errorf("move to scoring: %w", err)
		}
	}
	lg.mu.Lock()
	lg.g.End, lg.g.Winner, lg.g.StaffCategoryID = EndStaffPending, team, category
	m.transitionLocked(lg, GamePostGame)
	voice := lg.g.Channels
	voice.Text = ""
	lg.g.Channels.Voice = nil // the team voice channels go now; the chat stays until the game is scored
	snap = lg.g.clone()
	lg.mu.Unlock()
	if fn := m.fx().DeleteChannels; fn != nil && len(voice.Voice) > 0 {
		go fn(g.GuildID, voice)
	}
	m.sync(lg)
	m.notify(snap, GameEvent{Kind: EventStaffPending})
	return SubmitOutcome{End: EndStaffPending, Team: team, Game: snap}, nil
}

// ScoreOptions is defined in package types (games.go).
type ScoreOptions = types.ScoreOptions

// ScoreResult is the outcome of scoring a match.
type ScoreResult struct {
	Game    Game
	Results []ratings.EloResult
}

// Score finishes a live ranked match with the given winning team: staff
// answering a submission, or an automated tracker reporting the result.
// It works on a match that is in progress or awaiting scoring. If scoring
// fails nothing was written and it can simply be retried.
func (m *GameManager) Score(actor, guildID string, id uint, winningTeam string, o ScoreOptions) (ScoreResult, error) {
	lg := m.liveIn(guildID, id)
	if lg == nil {
		return ScoreResult{}, ErrGameNotFound
	}
	lg.mu.Lock()
	if lg.busy {
		lg.mu.Unlock()
		return ScoreResult{}, ErrBusy
	}
	if s := lg.g.State; s != GameInGame && s != GamePostGame {
		lg.mu.Unlock()
		return ScoreResult{}, ErrWrongState
	}
	team, ok := lg.g.ResolveTeam(winningTeam)
	if !ok {
		lg.mu.Unlock()
		return ScoreResult{}, ErrUnknownTeam
	}
	if strings.TrimSpace(o.Map) != "" {
		lg.g.Map = strings.TrimSpace(o.Map)
	}
	if !lg.g.Mode.Rated() {
		lg.g.Winner = team
		lg.mu.Unlock()
		snap, err := m.finishCasual(lg, actor)
		return ScoreResult{Game: snap}, err
	}
	lg.busy = true
	g := lg.g.clone()
	lg.mu.Unlock()
	defer func() {
		lg.mu.Lock()
		lg.busy = false
		lg.mu.Unlock()
	}()

	results, err := m.commitRanked(actor, g, team, o.MVP, false)
	if err != nil {
		return ScoreResult{}, err
	}
	lg.mu.Lock()
	lg.g.Winner, lg.g.End, lg.g.Results = team, EndRanked, results
	if mv := mvpOf(results); mv != "" {
		lg.g.MVP = mv
	}
	m.transitionLocked(lg, GameCleanup)
	snap := lg.g.clone()
	lg.mu.Unlock()

	m.sync(lg)
	m.notify(snap, GameEvent{Kind: EventScored})
	m.finish(lg, snap)
	return ScoreResult{Game: snap, Results: results}, nil
}

func mvpOf(rs []ratings.EloResult) string {
	for _, r := range rs {
		if r.MVP {
			return r.UserID
		}
	}
	return ""
}

// mvpFor decides the MVP: the staff pick if it is a winner, else the best
// winner by recorded performance, else nobody (no evidence, no bonus).
func mvpFor(g Game, winners []string, staffPick string) string {
	if staffPick != "" && slices.Contains(winners, staffPick) {
		return staffPick
	}
	var cands []ratings.MVPCandidate
	any := false
	for _, p := range g.Players {
		any = any || p.Kills > 0 || p.BedsDestroyed > 0 || p.FinalKills > 0
		cands = append(cands, ratings.MVPCandidate{ID: p.UserID, Kills: p.Kills, Deaths: p.Deaths, Beds: p.BedsDestroyed})
	}
	if !any {
		return ""
	}
	return ratings.SelectMVP(cands, winners, int64(g.ID))
}

// commitRanked scores g with winningTeam, writes it in one CompleteGame
// transaction, and runs the follow-ups (ranks, party counters). The
// results are only committed if CompleteGame succeeds; the follow-ups are
// best effort because the scoring itself is already durable.
func (m *GameManager) commitRanked(actor string, g Game, winningTeam, staffMVP string, rescore bool) ([]ratings.EloResult, error) {
	var winners, losers []string
	for _, p := range g.Players {
		if strings.EqualFold(p.Team, winningTeam) {
			winners = append(winners, p.UserID)
		} else {
			losers = append(losers, p.UserID)
		}
	}
	if len(winners) == 0 || len(losers) == 0 {
		return nil, fmt.Errorf("%w: %s has %d winners and %d losers", ErrUnknownTeam, winningTeam, len(winners), len(losers))
	}
	mvp := mvpFor(g, winners, staffMVP)
	mode := g.Mode
	results, err := m.deps.Elo.ScoreGame(ratings.ScoreInput{GuildID: g.GuildID, SeasonID: g.SeasonID, Winners: winners, Losers: losers, MVP: mvp, Parties: g.Parties, Mode: &mode})
	if err != nil {
		return nil, fmt.Errorf("score: %w", err)
	}
	byUser := make(map[string]ratings.EloResult, len(results))
	for _, r := range results {
		byUser[r.UserID] = r
	}

	rows := make([]models.GamePlayersModel, 0, len(g.Players))
	for _, p := range g.Players {
		r := byUser[p.UserID]
		row := participantRow(g, p)
		row.IsWinner, row.IsMvp = r.Win, r.MVP
		row.EloBefore, row.EloAfter, row.EloChange = r.Before, r.After, r.Change
		rows = append(rows, row)
	}
	if _, err := m.completeGame(actor, g, sdk.CompleteGameRequest{Status: string(types.GameStatusCompleted), Scoring: "ranked", Participants: rows}); err != nil {
		return nil, fmt.Errorf("complete game: %w", err)
	}

	m.deps.Elo.RecordStartingGrants(actor, g.GuildID, results)
	m.deps.Elo.ApplyRanks(actor, g.GuildID, results)
	if !rescore && m.deps.Parties != nil {
		for _, pg := range g.Parties {
			m.deps.Parties.RecordGame(g.GuildID, pg.Leader)
		}
	}
	return results, nil
}

func participantRow(g Game, p GamePlayer) models.GamePlayersModel {
	return models.GamePlayersModel{
		PlayerID: p.PlayerID, Team: p.Team, IsCaptain: p.Captain, InParty: g.inParty(p.UserID),
		Kills: p.Kills, Deaths: p.Deaths, Assists: p.Assists, FinalKills: p.FinalKills,
		BedsDestroyed: p.BedsDestroyed, BedsLost: p.BedsLost, FirstBloods: p.FirstBloods,
	}
}

type customStatsGameCompleter interface {
	CompleteGameWithCustomStats(actor, guildID string, id uint, in sdk.CompleteGameRequest, customStats map[uint]map[string]float64) (*models.GamesModel, error)
}

func (m *GameManager) completeGame(actor string, g Game, in sdk.CompleteGameRequest) (*models.GamesModel, error) {
	customStats := make(map[uint]map[string]float64)
	for _, player := range g.Players {
		if len(player.Custom) != 0 {
			customStats[player.PlayerID] = player.Custom
		}
	}
	if len(customStats) != 0 {
		api := m.api()
		if customAPI, ok := api.(customStatsGameCompleter); ok {
			return customAPI.CompleteGameWithCustomStats(actor, g.GuildID, g.ID, in, customStats)
		}
		return nil, errors.New("database API does not support custom game statistics")
	}
	return m.api().CompleteGame(actor, g.GuildID, g.ID, in)
}

// commitCasual writes a finished match of a mode that awards no ELO. Nobody
// won, so no wins/losses are recorded; gameplay counters are kept when the
// mode tracks stats.
func (m *GameManager) commitCasual(actor string, g Game) error {
	scoring := "none"
	if g.Mode.IsStatsTrackingEnabled {
		scoring = "casual"
	}
	rows := make([]models.GamePlayersModel, 0, len(g.Players))
	for _, p := range g.Players {
		rows = append(rows, participantRow(g, p))
	}
	_, err := m.completeGame(actor, g, sdk.CompleteGameRequest{Status: string(types.GameStatusCompleted), Scoring: scoring, Participants: rows})
	if err != nil {
		return fmt.Errorf("complete game: %w", err)
	}
	return nil
}

// finishCasual ends a live casual match.
func (m *GameManager) finishCasual(lg *liveGame, actor string) (Game, error) {
	lg.mu.Lock()
	if lg.busy {
		lg.mu.Unlock()
		return Game{}, ErrBusy
	}
	lg.busy = true
	g := lg.g.clone()
	lg.mu.Unlock()
	defer func() {
		lg.mu.Lock()
		lg.busy = false
		lg.mu.Unlock()
	}()

	if err := m.commitCasual(actor, g); err != nil {
		lg.mu.Lock()
		lg.submitted = false
		lg.mu.Unlock()
		return Game{}, err
	}
	lg.mu.Lock()
	lg.g.End = EndCasual
	m.transitionLocked(lg, GameCleanup)
	snap := lg.g.clone()
	lg.mu.Unlock()
	m.sync(lg)
	m.notify(snap, GameEvent{Kind: EventCasualDone})
	m.finish(lg, snap)
	return snap, nil
}

// finish releases a game that reached a terminal state: channels are deleted
// after a delay, the registry forgets it, and the queue is re-swept.
func (m *GameManager) finish(lg *liveGame, g Game) {
	HostEngine.Cancel(g.ID, "The game ended.") // a no-op unless the engine is still hosting it
	m.scheduleChannelCleanup(g)
	m.unregister(lg)
	if fn := m.fx().Finished; fn != nil {
		fn(g)
	}
	if fn := m.fx().Released; fn != nil {
		fn(g.GuildID, g.QueueChannelID)
	}
}

func (m *GameManager) scheduleChannelCleanup(g Game) {
	fn := m.fx().DeleteChannels
	if fn == nil || len(g.Channels.ids()) == 0 && g.Channels.Category == "" {
		go m.archiveLogs(g)
		return
	}
	// The team voice channels go at once; the game chat stays for the cleanup delay (so the result and the chat can be read), then goes too.
	voice, text := g.Channels, g.Channels
	voice.Text, text.Voice = "", nil
	if len(voice.Voice) > 0 {
		go fn(g.GuildID, voice)
	}
	cancel := m.deps.After(m.deps.CleanupDelay, func() {
		m.cleanupMu.Lock()
		delete(m.cleanups, g.ID)
		m.cleanupMu.Unlock()
		m.archiveLogs(g) // the chat is read while the channel still exists
		fn(g.GuildID, text)
	})
	m.cleanupMu.Lock()
	if old := m.cleanups[g.ID]; old != nil {
		old()
	}
	m.cleanups[g.ID] = cancel
	m.cleanupMu.Unlock()
}

// ============================================================================
// Voiding
// SplitResultMentions lists the winners and losers of a scored game as mentions.
func SplitResultMentions(results []ratings.EloResult) (winners, losers string) {
	var won, lost []string
	for _, r := range results {
		if r.Win {
			won = append(won, fmt.Sprintf("<@%s>", r.UserID))
		} else {
			lost = append(lost, fmt.Sprintf("<@%s>", r.UserID))
		}
	}
	return strings.Join(won, " "), strings.Join(lost, " ")
}
