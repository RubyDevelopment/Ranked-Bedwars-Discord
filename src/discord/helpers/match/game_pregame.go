package match

import (
	"errors"
	"fmt"
	"ranked-bedwars/src/discord/helpers/partymgr"
	"ranked-bedwars/src/discord/helpers/queue"
	"ranked-bedwars/src/types"
	"slices"
	"strings"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// ============================================================================

// RunPreGame runs the setup pipeline: roster check, rank-ladder gate,
// channels, then teams (automatic now, or the captain draft, which pauses the
// pipeline until it finishes). Any failure voids the game.
func (m *GameManager) RunPreGame(id uint) {
	lg := m.live(id)
	if lg == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			m.logf("pre-game panic in game %d: %v", id, r)
			_ = m.Void(sdk.SystemActor, lg.g.GuildID, id, "An unexpected error occurred during setup.", VoidOptions{})
		}
	}()
	if err := m.preGame(lg); err != nil {
		if errors.Is(err, errGameGone) {
			return
		}
		m.logf("pre-game for game %d: %v", id, err)
		_ = m.Void(sdk.SystemActor, lg.g.GuildID, id, "An unexpected error occurred during setup.", VoidOptions{})
	}
}

var errGameGone = errors.New("game was voided")

func (m *GameManager) preGame(lg *liveGame) error {
	lg.mu.Lock()
	if lg.g.State != GameWaiting {
		lg.mu.Unlock()
		return nil
	}
	m.transitionLocked(lg, GamePreGame)
	lg.mu.Unlock()
	m.sync(lg)

	if voided, err := m.verifyRoster(lg); err != nil || voided {
		if voided {
			return errGameGone
		}
		return err
	}

	g := m.snapshot(lg)
	if g.Mode.IsEloGainEnabled {
		if issues := m.deps.RankIssues(g.GuildID); len(issues) > 0 {
			_ = m.Void(sdk.SystemActor, g.GuildID, g.ID, "Competitive rank configuration incomplete: "+strings.Join(issues, "; "), VoidOptions{})
			return errGameGone
		}
	}

	if fn := m.fx().CreateChannels; fn != nil {
		ch, err := fn(g)
		if err != nil {
			return fmt.Errorf("create channels: %w", err)
		}
		lg.mu.Lock()
		if ch.Voice == nil {
			ch.Voice = map[string]string{}
		}
		lg.g.Channels = ch
		m.indexLocked(lg)
		lg.mu.Unlock()
		m.sync(lg)
	}

	if g.Custom != nil {
		return m.assignCustomTeams(lg)
	}
	flows := m.deps.Flows(g.GuildID)
	if captains := flows.CaptainFor(g.Mode); g.Captain && captains.IsEnabled {
		return m.beginDraft(lg, captains)
	}
	return m.assignStandardTeams(lg, flows.StandardFor(g.Mode))
}

func (m *GameManager) snapshot(lg *liveGame) Game {
	lg.mu.Lock()
	defer lg.mu.Unlock()
	return lg.g.clone()
}

// verifyRoster replaces players who left the queue channel with eligible
// waiting players, or voids the game when it can't be filled.
func (m *GameManager) verifyRoster(lg *liveGame) (voided bool, err error) {
	g := m.snapshot(lg)
	present := m.fx().PresentInQueue
	if present == nil {
		return false, nil
	}
	here := present(g)
	var missing []GamePlayer
	for _, p := range g.Players {
		if !here[p.UserID] {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}

	var subs []queue.QueueMember
	if fn := m.fx().Substitutes; fn != nil {
		for _, s := range fn(g, len(missing)) {
			if len(subs) == len(missing) {
				break
			}
			if _, in := g.Player(s.UserID); in || m.InGame(g.GuildID, s.UserID) || slices.ContainsFunc(subs, func(o queue.QueueMember) bool { return o.UserID == s.UserID }) {
				continue
			}
			if _, e := m.api().PlayerID(g.GuildID, s.UserID); e != nil {
				continue
			}
			subs = append(subs, s)
		}
	}
	if len(subs) < len(missing) {
		_ = m.Void(sdk.SystemActor, g.GuildID, g.ID, "A player left before the game could start and no replacement was available.", VoidOptions{})
		return true, nil
	}

	ratings := m.deps.Elo.Ratings(g.GuildID, memberIDs(subs))
	for i, old := range missing {
		sub := subs[i]
		pid, e := m.api().PlayerID(g.GuildID, sub.UserID)
		if e != nil {
			return false, e
		}
		if e := m.api().RemoveParticipant(sdk.SystemActor, g.GuildID, g.ID, old.PlayerID); e != nil {
			return false, e
		}
		if e := m.api().AddParticipant(sdk.SystemActor, g.GuildID, g.ID, models.GamePlayersModel{PlayerID: pid, Team: teamUnassigned}); e != nil {
			return false, e
		}
		lg.mu.Lock()
		for j := range lg.g.Players {
			if lg.g.Players[j].UserID == old.UserID {
				lg.g.Players[j] = GamePlayer{UserID: sub.UserID, Username: sub.Username, PlayerID: pid, Team: teamUnassigned, EloBefore: ratings[sub.UserID]}
			}
		}
		// A party that lost a member can't be assumed intact: drop groups touching the leaver.
		lg.g.Parties = slices.DeleteFunc(lg.g.Parties, func(pg partymgr.PartyGroup) bool { return slices.Contains(pg.Members, old.UserID) })
		m.mu.Lock()
		delete(m.byPlayer, playerKey(g.GuildID, old.UserID))
		m.mu.Unlock()
		m.indexLocked(lg)
		lg.mu.Unlock()
	}
	m.sync(lg)
	return false, nil
}

func memberIDs(ms []queue.QueueMember) []string {
	out := make([]string, len(ms))
	for i, x := range ms {
		out[i] = x.UserID
	}
	return out
}

func (m *GameManager) assignStandardTeams(lg *liveGame, cfg StandardFlow) error {
	g := m.snapshot(lg)
	teams, perTeam := TeamLayout(g.Mode)
	ids := g.userIDs()

	var assigned [][]string
	if !cfg.IsEnabled { // plain queue order
		for t := 0; t < teams; t++ {
			assigned = append(assigned, slices.Clone(ids[t*perTeam:(t+1)*perTeam]))
		}
	} else {
		opts := TeamOptions{
			Teams: teams, PerTeam: perTeam, Randomness: cfg.RandomnessPercent,
			Ratings: m.deps.Elo.Ratings(g.GuildID, ids), BalanceParties: cfg.BalancePartyGroups, AllowSplit: cfg.AllowSplitParties,
			Method: m.deps.Elo.PartyConfig(g.GuildID).EloAggregationMethod, Rand: m.deps.Rand,
		}
		if cfg.KeepPartyTogether {
			opts.Parties = g.Parties
		}
		var err error
		if assigned, err = AssignTeams(ids, opts); err != nil {
			return err
		}
	}
	return m.setTeams(lg, assigned, nil)
}

// setTeams stores the final teams (captains are marked), persists each
// player's team, applies them in Discord, picks the host and starts the match.
func (m *GameManager) setTeams(lg *liveGame, teams [][]string, captains []string) error {
	lg.mu.Lock()
	if lg.g.State != GamePreGame {
		lg.mu.Unlock()
		return errGameGone
	}
	names := make([]string, len(teams))
	for t, members := range teams {
		names[t] = TeamName(t)
		for _, id := range members {
			for i := range lg.g.Players {
				if lg.g.Players[i].UserID == id {
					lg.g.Players[i].Team = names[t]
					lg.g.Players[i].Captain = slices.Contains(captains, id)
				}
			}
		}
	}
	lg.g.Teams = names
	lg.g.Host = m.pickHost(lg.g)
	lg.draft = nil
	g := lg.g.clone()
	lg.mu.Unlock()

	for _, p := range g.Players {
		if err := m.api().UpdateParticipant(sdk.SystemActor, g.GuildID, g.ID, p.PlayerID, map[string]any{"team": p.Team, "is_captain": p.Captain, "in_party": g.inParty(p.UserID)}); err != nil {
			return fmt.Errorf("save team of %s: %w", p.UserID, err)
		}
	}
	m.sync(lg)
	if fn := m.fx().ApplyTeams; fn != nil {
		if err := fn(g); err != nil {
			return fmt.Errorf("apply teams: %w", err)
		}
	}
	m.notify(g, GameEvent{Kind: EventTeams})

	selectMap := m.fx().SelectMap
	chooseMap := func(available []MapOption, mapMenuRead bool) (*MapSelection, error) {
		if selectMap == nil {
			return nil, nil
		}
		selection, err := selectMap(g, available, mapMenuRead)
		if err == nil && selection != nil && strings.TrimSpace(selection.Name) != "" {
			if setErr := m.SetMap(g.ID, selection.Name); setErr != nil {
				return nil, setErr
			}
			g.Map = selection.Name
		}
		return selection, err
	}
	if fn := m.fx().Host; fn != nil {
		handled, err := fn(g, chooseMap)
		if err != nil {
			_ = m.Void(sdk.SystemActor, g.GuildID, g.ID, err.Error(), VoidOptions{})
			return errGameGone
		}
		if handled {
			return nil // the engine starts the match
		}
	}
	// Hosted by hand the old bot did not vote: it listed every map with its height and teams and the players chose. A guild can switch the vote on.
	if flow := flowOf(g.GuildID); selectMap != nil && flow.MapVote.Manual != nil && *flow.MapVote.Manual {
		if _, err := chooseMap(nil, false); err != nil {
			m.logf("select map for game %d: %v", g.ID, err)
		}
	}
	return m.BeginMatch(g.ID)
}

// Notify posts an out-of-band event (the engine bridge's progress) to the game's chat.
func (m *GameManager) Notify(id uint, ev GameEvent) {
	if g, ok := m.Get(id); ok {
		m.notify(g, ev)
	}
}

// pickHost: the first party leader in the game, else the highest-rated player.
func (m *GameManager) pickHost(g Game) string {
	if g.Custom != nil {
		if _, ok := g.Player(g.Custom.HostedBy); ok {
			return g.Custom.HostedBy
		}
	}
	for _, pg := range g.Parties {
		if _, ok := g.Player(pg.Leader); ok {
			return pg.Leader
		}
	}
	best := ""
	bestElo := -1
	for _, p := range g.Players {
		if p.EloBefore > bestElo {
			best, bestElo = p.UserID, p.EloBefore
		}
	}
	return best
}

// BeginMatch starts play: PRE_GAME -> IN_GAME.
func (m *GameManager) BeginMatch(id uint) error {
	lg := m.live(id)
	if lg == nil {
		return ErrGameNotFound
	}
	lg.mu.Lock()
	if lg.g.State != GamePreGame || len(lg.g.Teams) == 0 {
		lg.mu.Unlock()
		return ErrWrongState
	}
	m.transitionLocked(lg, GameInGame)
	lg.g.StartedAt = m.deps.Now()
	g := lg.g.clone()
	lg.mu.Unlock()

	if _, err := m.api().StartGame(sdk.SystemActor, g.GuildID, g.ID); err != nil {
		m.logf("start game %d: %v", g.ID, err)
	}
	m.sync(lg)
	m.notify(g, GameEvent{Kind: EventStarted})
	return nil
}

// SetBot records the hosting bot's Minecraft account.
func (m *GameManager) SetBot(id uint, name string) error {
	lg := m.live(id)
	if lg == nil {
		return ErrGameNotFound
	}
	lg.mu.Lock()
	lg.g.Bot = strings.TrimSpace(name)
	lg.mu.Unlock()
	m.sync(lg)
	return nil
}

// SetMap records the map the match is played on.
func (m *GameManager) SetMap(id uint, name string) error {
	lg := m.live(id)
	if lg == nil {
		return ErrGameNotFound
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("map name is empty")
	}
	lg.mu.Lock()
	lg.g.Map = name
	lg.mu.Unlock()
	m.sync(lg)
	return nil
}

// ---- captain draft ----------------------------------------------------------

func (m *GameManager) beginDraft(lg *liveGame, cfg CaptainFlow) error {
	g := m.snapshot(lg)
	teams, perTeam := TeamLayout(g.Mode)
	d, err := NewDraft(g.userIDs(), teams, perTeam, m.deps.Elo.Ratings(g.GuildID, g.userIDs()), g.Parties, DraftConfig{
		PickingType: cfg.DefaultPickingType, TopEloChance: cfg.TopEloCaptainChance, OneCaptainPerParty: cfg.OneCaptainPerParty,
		AlternatingMinPlayers: cfg.AlternatingMinPlayers, AutoAssignParties: cfg.AutoAssignParties,
	}, m.deps.Rand)
	if err != nil {
		return err
	}
	lg.mu.Lock()
	lg.draft = d
	lg.mu.Unlock()
	if fn := m.fx().Prepare; fn != nil {
		fn(g) // the bot joins the server while the captains pick
	}
	m.notify(g, GameEvent{Kind: EventDraftNeeded})
	return m.finishDraftIfDone(lg)
}

// DraftView is defined in package types (games.go).
type DraftView = types.DraftView

// Draft returns the running captain draft of a game.
func (m *GameManager) Draft(id uint) (DraftView, error) {
	lg := m.live(id)
	if lg == nil {
		return DraftView{}, ErrGameNotFound
	}
	lg.mu.Lock()
	defer lg.mu.Unlock()
	if lg.draft == nil {
		return DraftView{}, ErrNoDraftYet
	}
	d := lg.draft
	view := DraftView{Pool: d.Pool(), Type: d.Type, Captains: d.Captains()}
	view.Turn = d.Turn()
	view.AutoFilled = slices.Clone(d.filled)
	for _, t := range d.Teams {
		view.Teams = append(view.Teams, slices.Clone(t))
	}
	return view, nil
}

// DraftPick makes a captain's pick; when it completes the draft the teams are applied and the match starts.
func (m *GameManager) DraftPick(id uint, captain, player string) error {
	lg := m.live(id)
	if lg == nil {
		return ErrGameNotFound
	}
	lg.mu.Lock()
	if lg.draft == nil {
		lg.mu.Unlock()
		return ErrNoDraftYet
	}
	_, err := lg.draft.Pick(captain, player)
	lg.mu.Unlock()
	if err != nil {
		return err
	}
	m.finishDraftAsync(lg)
	return nil
}

// finishDraftAsync completes a finished draft in the background: applying the teams runs the map vote and
// hands the game to the engine, which takes far longer than the 3 seconds a Discord interaction may wait.
func (m *GameManager) finishDraftAsync(lg *liveGame) {
	go func() { _ = m.finishDraftIfDone(lg) }()
}

// DraftAutoPick picks for the captain whose turn it is (their pick timed out).
func (m *GameManager) DraftAutoPick(id uint) (captain, player string, err error) {
	lg := m.live(id)
	if lg == nil {
		return "", "", ErrGameNotFound
	}
	lg.mu.Lock()
	if lg.draft == nil {
		lg.mu.Unlock()
		return "", "", ErrNoDraftYet
	}
	captain, player, err = lg.draft.AutoPick()
	lg.mu.Unlock()
	if err != nil {
		return "", "", err
	}
	m.finishDraftAsync(lg)
	return captain, player, nil
}

func (m *GameManager) finishDraftIfDone(lg *liveGame) error {
	lg.mu.Lock()
	d := lg.draft
	if d == nil || d.Turn() >= 0 { // Turn also auto-fills the last team in the 4-captain pattern
		lg.mu.Unlock()
		return nil
	}
	teams := make([][]string, len(d.Teams))
	for i, t := range d.Teams {
		teams[i] = slices.Clone(t)
	}
	captains := d.Captains()
	lg.mu.Unlock()
	if err := m.setTeams(lg, teams, captains); err != nil && !errors.Is(err, errGameGone) {
		m.logf("finish draft for game %d: %v", lg.g.ID, err)
		_ = m.Void(sdk.SystemActor, lg.g.GuildID, lg.g.ID, "An unexpected error occurred while finishing the draft.", VoidOptions{})
		return err
	}
	return nil
}

// ============================================================================
// Ending a match
