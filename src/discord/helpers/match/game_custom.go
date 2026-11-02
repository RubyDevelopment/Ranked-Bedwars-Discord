package match

import (
	"errors"
	"fmt"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/discord/helpers/queue"
	"ranked-bedwars/src/types"
	"slices"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// ============================================================================

// A custom game is built by staff (/hostgame) instead of formed from a queue:
// they pick the mode, the teams and, optionally, the voice channel each team
// plays in, and whether the match counts toward ELO and season stats. Nothing is
// drafted or randomised, and it never touches a queue.

// CustomGame is defined in package types (games.go).
type CustomGame = types.CustomGame

// CustomSpec is defined in package types (games.go).
type CustomSpec = types.CustomSpec

var (
	ErrNotCustom = errors.New("that game was not built by staff")
	ErrBotHosted = errors.New("a bot is hosting that game, its roster can't be changed")
)

// customMode is the queue mode with the team layout and scoring staff chose.
func customMode(mode types.Mode, teams [][]queue.QueueMember, ranked, stats bool) types.Mode {
	total, largest := 0, 1
	for _, t := range teams {
		total += len(t)
		largest = max(largest, len(t))
	}
	mode.TeamCount = max(2, len(teams))
	mode.PlayersPerTeam = largest
	mode.MaxPlayers = total
	mode.IsEloGainEnabled = ranked
	mode.IsStatsTrackingEnabled = stats
	return mode
}

// StartCustom creates a staff-built game and starts its pre-game in the background.
func (m *GameManager) StartCustom(spec CustomSpec) (Game, error) {
	if len(spec.Teams) < 2 {
		return Game{}, fmt.Errorf("%w: a game needs at least two teams", ErrBadRoster)
	}
	var players []queue.QueueMember
	ids := make([][]string, len(spec.Teams))
	for i, t := range spec.Teams {
		if len(t) == 0 {
			return Game{}, fmt.Errorf("%w: team %s has no players", ErrBadRoster, TeamName(i))
		}
		for _, p := range t {
			players = append(players, p)
			ids[i] = append(ids[i], p.UserID)
		}
	}

	group := &queue.QueueGroup{
		ID: base.NewID(), GuildID: spec.GuildID, ChannelID: spec.QueueChannelID, CategoryID: spec.CategoryID,
		Mode: customMode(spec.Mode, spec.Teams, spec.Ranked, spec.Stats), Players: players,
	}
	custom := &CustomGame{
		HostedBy: spec.HostedBy, Ranked: spec.Ranked, Stats: spec.Stats, UseBot: spec.UseBot,
		Teams: ids, Voice: slices.Clone(spec.Voice),
	}
	g, err := m.start(group, custom)
	if err != nil {
		return Game{}, err
	}
	go m.RunPreGame(g.ID)
	return g, nil
}

// assignCustomTeams is the custom game's team step: the teams are the ones staff built.
func (m *GameManager) assignCustomTeams(lg *liveGame) error {
	g := m.snapshot(lg)
	return m.setTeams(lg, g.Custom.Teams, nil)
}

// CustomGames lists the live staff-built games of a guild.
func (m *GameManager) CustomGames(guildID string) []Game {
	var out []Game
	for _, g := range m.GuildGames(guildID) {
		if g.Custom != nil {
			out = append(out, g)
		}
	}
	return out
}

// customLive finds a live custom game of the guild, locked for editing its roster or options.
func (m *GameManager) customLive(guildID string, id uint) (*liveGame, error) {
	lg := m.liveIn(guildID, id)
	if lg == nil {
		return nil, ErrGameNotFound
	}
	if lg.g.Custom == nil {
		return nil, ErrNotCustom
	}
	return lg, nil
}

// SetCustomOptions changes what a running custom game counts toward (nil = unchanged).
func (m *GameManager) SetCustomOptions(guildID string, id uint, ranked, stats *bool) error {
	lg, err := m.customLive(guildID, id)
	if err != nil {
		return err
	}
	lg.mu.Lock()
	if !lg.g.State.active() && lg.g.State != GamePostGame {
		lg.mu.Unlock()
		return ErrWrongState
	}
	if ranked != nil {
		lg.g.Custom.Ranked, lg.g.Mode.IsEloGainEnabled = *ranked, *ranked
	}
	if stats != nil {
		lg.g.Custom.Stats, lg.g.Mode.IsStatsTrackingEnabled = *stats, *stats
	}
	lg.mu.Unlock()
	m.sync(lg)
	return nil
}

// MovePlayer moves a player to another team (team is an index). Not for bot-hosted games.
func (m *GameManager) MovePlayer(guildID string, id uint, userID string, team int) error {
	lg, err := m.customLive(guildID, id)
	if err != nil {
		return err
	}
	lg.mu.Lock()
	if lg.g.Bot != "" {
		lg.mu.Unlock()
		return ErrBotHosted
	}
	if team < 0 || team >= len(lg.g.Custom.Teams) {
		lg.mu.Unlock()
		return ErrUnknownTeam
	}
	idx := slices.IndexFunc(lg.g.Players, func(p GamePlayer) bool { return p.UserID == userID })
	if idx < 0 {
		lg.mu.Unlock()
		return ErrNotParticipant
	}
	for i := range lg.g.Custom.Teams {
		lg.g.Custom.Teams[i] = slices.DeleteFunc(lg.g.Custom.Teams[i], func(u string) bool { return u == userID })
	}
	lg.g.Custom.Teams[team] = append(lg.g.Custom.Teams[team], userID)
	name := TeamName(team)
	started := len(lg.g.Teams) > 0 // teams are final: the participant rows carry them
	if started {
		lg.g.Players[idx].Team = name
	}
	player := lg.g.Players[idx]
	lg.mu.Unlock()

	if started {
		if err := m.api().UpdateParticipant(sdk.SystemActor, guildID, id, player.PlayerID, map[string]any{"team": name}); err != nil {
			return err
		}
	}
	m.sync(lg)
	return nil
}

// Regroup moves players to the teams they actually stood on when a bot-hosted match started without the drafted teams
// (teams maps a team name to its players' Discord ids). Scoring goes by these teams.
func (m *GameManager) Regroup(guildID string, id uint, teams map[string][]string) error {
	lg := m.live(id)
	if lg == nil {
		return ErrGameNotFound
	}
	type move struct {
		playerID uint
		team     string
	}
	var moves []move
	lg.mu.Lock()
	for team, ids := range teams {
		for _, userID := range ids {
			if i := slices.IndexFunc(lg.g.Players, func(p GamePlayer) bool { return p.UserID == userID }); i >= 0 && lg.g.Players[i].Team != team {
				lg.g.Players[i].Team = team
				moves = append(moves, move{lg.g.Players[i].PlayerID, team})
			}
		}
	}
	lg.mu.Unlock()
	for _, mv := range moves {
		if err := m.api().UpdateParticipant(sdk.SystemActor, guildID, id, mv.playerID, map[string]any{"team": mv.team}); err != nil {
			return err
		}
	}
	m.sync(lg)
	if fn := m.fx().ApplyTeams; fn != nil && len(moves) > 0 {
		lg.mu.Lock()
		snap := lg.g.clone()
		lg.mu.Unlock()
		if err := fn(snap); err != nil { // the team voice channels and permissions follow the new teams
			m.logf("regroup game %d: moving players to their team voice channels: %v", id, err)
		}
	}
	return nil
}

// ReplacePlayer swaps a player for another on the same team. Not for bot-hosted games.
func (m *GameManager) ReplacePlayer(guildID string, id uint, out, in, inName string) error {
	lg, err := m.customLive(guildID, id)
	if err != nil {
		return err
	}
	if m.InGame(guildID, in) {
		return &BusyError{in}
	}
	newID, err := m.api().PlayerID(guildID, in)
	if err != nil {
		if isNotFound(err) {
			return &NotRegisteredError{[]string{in}}
		}
		return err
	}

	lg.mu.Lock()
	if lg.g.Bot != "" {
		lg.mu.Unlock()
		return ErrBotHosted
	}
	idx := slices.IndexFunc(lg.g.Players, func(p GamePlayer) bool { return p.UserID == out })
	if idx < 0 {
		lg.mu.Unlock()
		return ErrNotParticipant
	}
	old := lg.g.Players[idx]
	rating := m.deps.Elo.Ratings(guildID, []string{in})[in]
	lg.g.Players[idx] = GamePlayer{UserID: in, Username: inName, PlayerID: newID, Team: old.Team, Captain: old.Captain, EloBefore: rating}
	for i := range lg.g.Custom.Teams {
		for j, u := range lg.g.Custom.Teams[i] {
			if u == out {
				lg.g.Custom.Teams[i][j] = in
			}
		}
	}
	if lg.g.Custom.HostedBy == out {
		lg.g.Custom.HostedBy = in
	}
	if lg.g.Host == out {
		lg.g.Host = in
	}
	m.mu.Lock()
	delete(m.byPlayer, playerKey(guildID, out))
	m.mu.Unlock()
	m.indexLocked(lg)
	teamName, started := old.Team, len(lg.g.Teams) > 0
	lg.mu.Unlock()

	if started {
		if err := m.api().RemoveParticipant(sdk.SystemActor, guildID, id, old.PlayerID); err != nil {
			return err
		}
		row := participantRow(Game{}, GamePlayer{PlayerID: newID, Team: teamName})
		if err := m.api().AddParticipant(sdk.SystemActor, guildID, id, row); err != nil {
			return err
		}
	}
	m.sync(lg)
	return nil
}

// SetTeamVoice points a team at an existing voice channel (team is an index).
func (m *GameManager) SetTeamVoice(guildID string, id uint, team int, channelID string) error {
	lg, err := m.customLive(guildID, id)
	if err != nil {
		return err
	}
	lg.mu.Lock()
	if team < 0 || team >= len(lg.g.Custom.Teams) {
		lg.mu.Unlock()
		return ErrUnknownTeam
	}
	for len(lg.g.Custom.Voice) < len(lg.g.Custom.Teams) {
		lg.g.Custom.Voice = append(lg.g.Custom.Voice, "")
	}
	lg.g.Custom.Voice[team] = channelID
	name := TeamName(team)
	if lg.g.Channels.Voice == nil {
		lg.g.Channels.Voice = map[string]string{}
	}
	lg.g.Channels.Voice[name] = channelID
	if !slices.Contains(lg.g.Channels.Borrowed, channelID) {
		lg.g.Channels.Borrowed = append(lg.g.Channels.Borrowed, channelID)
	}
	m.indexLocked(lg)
	lg.mu.Unlock()
	m.sync(lg)
	return nil
}

// ============================================================================
// Game Discord effects
