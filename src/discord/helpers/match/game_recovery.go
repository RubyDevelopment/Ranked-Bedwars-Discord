package match

import (
	"context"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/discord/helpers/queue"
	"ranked-bedwars/src/types"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// RecoverSummary is defined in package types (games.go).
type RecoverSummary = types.RecoverSummary

// Recover rebuilds the registry after a restart from the games the database
// still lists as open. A running game stays running; a game awaiting staff
// scoring keeps waiting; a game that was still being set up is voided (its
// pipeline died with the process; its players are free to queue again); a
// row with no usable metadata is cancelled; a row that is already scored is
// just closed. Call it once the bot is connected.
func (m *GameManager) Recover(guildIDs []string) RecoverSummary {
	var sum RecoverSummary
	for _, guildID := range guildIDs {
		for _, status := range []string{string(types.GameStatusWaiting), string(types.GameStatusInProgress)} {
			recs, err := m.api().ListGames(guildID, status)
			if err != nil {
				m.logf("recover: list %s games of %s: %v", status, guildID, err)
				continue
			}
			for i := range recs {
				m.recoverOne(guildID, &recs[i], &sum)
			}
		}
	}
	return sum
}

func (m *GameManager) recoverOne(guildID string, rec *models.GamesModel, sum *RecoverSummary) {
	if m.live(rec.ID) != nil {
		return
	}
	var g Game
	if rec.RuntimeMetadata.IsEmpty() || rec.RuntimeMetadata.Unmarshal(&g) != nil || len(g.Players) == 0 {
		if _, err := m.api().EndGame(sdk.SystemActor, guildID, rec.ID, string(types.GameStatusCancelled)); err != nil {
			m.logf("recover: cancel game %d: %v", rec.ID, err)
		}
		sum.Cancelled++
		return
	}
	g.ID, g.Number, g.GuildID, g.SeasonID = rec.ID, rec.GameNumber, guildID, rec.SeasonID
	if g.Channels.Voice == nil {
		g.Channels.Voice = map[string]string{}
	}

	if rows, err := m.api().Participants(guildID, rec.ID); err == nil && anyScored(rows) {
		// Scored, but the row still says open: close it and let the channels go.
		g.State, g.End = GameCleanup, EndRanked
		if _, err := m.api().UpdateGame(sdk.SystemActor, guildID, rec.ID, map[string]any{"status": string(types.GameStatusCompleted)}); err != nil {
			m.logf("recover: close scored game %d: %v", rec.ID, err)
		}
		m.scheduleChannelCleanup(g)
		sum.Closed++
		return
	}

	lg := &liveGame{g: g, submitted: g.End == EndStaffPending}
	m.mu.Lock()
	m.games[g.ID] = lg
	m.mu.Unlock()
	lg.mu.Lock()
	lg.g.StateAt = m.deps.Now()
	m.indexLocked(lg)
	lg.mu.Unlock()

	switch g.State {
	case GamePostGame: // awaiting staff scoring: keep, whether or not the chat survived
		sum.Kept++
	case GameInGame:
		if exists := m.fx().ChannelsExist; exists != nil && g.Channels.Text != "" && !exists(g) {
			_ = m.Void(sdk.SystemActor, guildID, g.ID, "The game channels were deleted while the bot was offline.", VoidOptions{})
			sum.Voided++
			return
		}
		sum.Kept++
	default: // waiting / pre-game / anything unexpected
		if g.State == GamePreGame && g.Bot != "" {
			// The engine accepted this game and is hosting it (a bot is on the server, inviting the players): it keeps working
			// without the bot, so the game carries on where it left off instead of being voided. resumeHostedGames asks the
			// engine to take it up again; if the engine no longer has it, it is voided then (see EngineBridge.gone).
			sum.Kept++
			return
		}
		_ = m.Void(sdk.SystemActor, guildID, g.ID, "The bot restarted while the game was being set up.", VoidOptions{})
		sum.Voided++
	}
}

// Sweep voids games stuck in setup past PreGameLimit (and running games past
// MaxInGame, when set). It returns how many it voided.
func (m *GameManager) Sweep() int {
	now := m.deps.Now()
	type stale struct {
		guild  string
		id     uint
		reason string
	}
	var todo []stale
	// Copy the registry first: game locks are taken before the registry lock
	// elsewhere, so they must not be taken while holding it.
	m.mu.RLock()
	live := make([]*liveGame, 0, len(m.games))
	for _, lg := range m.games {
		live = append(live, lg)
	}
	m.mu.RUnlock()
	for _, lg := range live {
		lg.mu.Lock()
		g := lg.g
		lg.mu.Unlock()
		switch {
		case (g.State == GameWaiting || g.State == GamePreGame) && now.Sub(g.StateAt) > m.deps.PreGameLimit:
			todo = append(todo, stale{g.GuildID, g.ID, "Game setup timed out."})
		case g.State == GameInGame && m.deps.MaxInGame > 0 && now.Sub(g.StartedAt) > m.deps.MaxInGame:
			todo = append(todo, stale{g.GuildID, g.ID, "The game ran past the maximum match length."})
		}
	}
	n := 0
	for _, t := range todo {
		if m.Void(sdk.SystemActor, t.guild, t.id, t.reason, VoidOptions{}) == nil {
			n++
		}
	}
	return n
}

// StartSweeper runs Sweep every interval (default a minute) until ctx ends.
func (m *GameManager) StartSweeper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							m.logf("sweep panic: %v", r)
						}
					}()
					m.Sweep()
				}()
			}
		}
	}()
}

// AddStats adds tracked performance to a player of a live game (a tracker or
// staff feeding kills/deaths/beds); it feeds the season counters and the MVP pick.
func (m *GameManager) AddStats(id uint, userID string, d GamePlayer) error {
	lg := m.live(id)
	if lg == nil {
		return ErrGameNotFound
	}
	lg.mu.Lock()
	defer lg.mu.Unlock()
	for i := range lg.g.Players {
		p := &lg.g.Players[i]
		if p.UserID != userID {
			continue
		}
		p.Kills += d.Kills
		p.Deaths += d.Deaths
		p.Assists += d.Assists
		p.FinalKills += d.FinalKills
		p.BedsDestroyed += d.BedsDestroyed
		p.BedsLost += d.BedsLost
		p.FirstBloods += d.FirstBloods
		for id, n := range d.Custom {
			if p.Custom == nil {
				p.Custom = map[string]float64{}
			}
			p.Custom[id] += n
		}
		return nil
	}
	return ErrNotParticipant
}

// ============================================================================
// Lookups for commands
// ============================================================================

var gameSweeperOnce sync.Once

// RecoverGamesOnStartup runs once the bot is connected: it picks up the games the database still lists as open (a game
// stuck in setup is voided, a scored one is closed), deletes the channels of every game that is already scored, voided or
// cancelled but whose channels were never removed (the bot was down when they were due), and starts the watchdog that voids
// games stuck in setup.
func RecoverGamesOnStartup(s *discordgo.Session, guildIDs []string) {
	base.Session.Store(s) // the game effects reach Discord through this; until a voice event arrives it would be empty
	gameSweeperOnce.Do(func() { Games.StartSweeper(context.Background(), time.Minute) })
	sum := Games.Recover(guildIDs)
	removed := 0
	for _, guildID := range guildIDs {
		removed += Games.CleanupFinishedGames(s, guildID)
	}
	Games.logf("startup: %d games kept, %d voided, %d cancelled, %d closed; %d leftover game channels deleted", sum.Kept, sum.Voided, sum.Cancelled, sum.Closed, removed)
	resumeHostedGames(guildIDs)
	sweepQueuesOnStartup(s, guildIDs) // once the games are known: players who are in a game are then not eligible
}

// resumeHostedGames asks the engine to take up again the matches it was hosting before the restart. The engine keeps a match going
// while the bot is away; once it answers, tracking and the automatic result carry on as if nothing had happened.
func resumeHostedGames(guildIDs []string) {
	resumer, ok := HostEngine.(interface{ Resume([]Game) })
	if !ok {
		return
	}
	var open []Game
	for _, guildID := range guildIDs {
		for _, g := range Games.GuildGames(guildID) {
			if (g.State == GameInGame || g.State == GamePreGame) && g.Bot != "" {
				open = append(open, g)
			}
		}
	}
	if len(open) > 0 {
		resumer.Resume(open)
	}
}

// sweepQueuesOnStartup checks everyone already sitting in a queue voice channel when the bot comes up. They joined while the bot
// was offline, so no voice event ever screened them: each is checked now, like a fresh join (registered, not banned, not in a
// game, rank and role gates), those who cannot queue are removed with the reason, and a lobby forms for the rest.
func sweepQueuesOnStartup(s *discordgo.Session, guildIDs []string) {
	for _, guildID := range guildIDs {
		// The guild's voice states arrive just after the ready event: wait for them (a guild that never appears is skipped).
		ready := false
		for range 60 {
			if g, err := s.State.Guild(guildID); err == nil && g != nil && g.Name != "" {
				ready = true
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if !ready {
			continue
		}
		snap, err := queue.Queues.Snapshot(guildID)
		if err != nil || snap == nil {
			continue
		}
		for channelID := range snap.Bindings {
			queue.SweepQueueChannel(s, guildID, channelID)
		}
	}
}

// CleanupFinishedGames deletes the channels that still exist for the guild's recent games that are over (scored, voided or
// cancelled). It returns how many channels it deleted. Games the manager still runs are left alone.
func (m *GameManager) CleanupFinishedGames(s *discordgo.Session, guildID string) int {
	existing := map[string]bool{}
	if channels, err := s.GuildChannels(guildID); err == nil {
		for _, c := range channels {
			existing[c.ID] = true
		}
	} else {
		m.logf("cleanup: list channels of %s: %v", guildID, err)
		return 0
	}
	if threads, err := s.GuildThreadsActive(guildID); err == nil {
		for _, t := range threads.Threads {
			existing[t.ID] = true
		}
	}
	deleted := 0
	for _, status := range []string{string(types.GameStatusCompleted), string(types.GameStatusVoided), string(types.GameStatusCancelled)} {
		recs, err := m.api().RecentGames(guildID, status, 100)
		if err != nil {
			m.logf("cleanup: list %s games of %s: %v", status, guildID, err)
			continue
		}
		for i := range recs {
			rec := &recs[i]
			if m.live(rec.ID) != nil || rec.RuntimeMetadata.IsEmpty() {
				continue
			}
			var g Game
			if rec.RuntimeMetadata.Unmarshal(&g) != nil {
				continue
			}
			for _, id := range g.Channels.ownedIDs() {
				if !existing[id] {
					continue
				}
				if _, err := s.ChannelDelete(id); err == nil {
					deleted++
				}
			}
		}
	}
	return deleted
}

// eloPrefix is the "[655] " a registered member's display name starts with.
var eloPrefix = regexp.MustCompile(`^\[\d+\]\s*`)

// inviteName is the Minecraft name to invite a player by: the one they registered, else their display name without the
// ELO tag and without a "| nickname" suffix.
func inviteName(guildID string, p GamePlayer) string {
	if view, err := database.Must().Player.Get(guildID, p.UserID); err == nil && view.Config != nil && strings.TrimSpace(view.Config.Username) != "" {
		return strings.TrimSpace(view.Config.Username)
	}
	name := eloPrefix.ReplaceAllString(strings.TrimSpace(p.Username), "")
	name, _, _ = strings.Cut(name, " | ")
	return strings.TrimSpace(name)
}
