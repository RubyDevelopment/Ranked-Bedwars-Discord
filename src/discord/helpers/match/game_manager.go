package match

import (
	"encoding/json"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/discord/helpers/partymgr"
	"ranked-bedwars/src/discord/helpers/queue"
	"ranked-bedwars/src/discord/helpers/ratings"
	"ranked-bedwars/src/types"
	appLog "ranked-bedwars/src/utility"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// ============================================================================

type liveGame struct {
	mu        sync.Mutex
	g         Game
	draft     *Draft
	submitted bool
	busy      bool // scoring in progress
	syncMu    sync.Mutex
	out       map[string]bool // players who are out of the match for good (final death): free to queue again
}

// GameManager: see the package comment above.
type GameManager struct {
	deps GameDeps

	mu        sync.RWMutex
	games     map[uint]*liveGame
	byPlayer  map[string]uint // guild:user -> id, only while the game is active
	byChannel map[string]uint

	fxMu    sync.RWMutex
	effects GameEffects

	cleanupMu sync.Mutex
	cleanups  map[uint]func()

	chatMu sync.Mutex
	chat   map[uint][]GameChatLine // the engine's Minecraft chat per game, until it is archived
}

// Games is the process-wide manager.
var Games = NewGameManager(GameDeps{})

func init() {
	// Wire "is this player in a game" into the managers that need it. Done here,
	// not in their constructors, so they don't depend on this file.

	queue.Queues.Wire(func(d *queue.QueueDeps) {
		d.InGame = Games.InGame
		d.VoidVoting = Games.VoidVoting
		d.ReturnToGame = Games.ReturnToGame
		d.FlowMaxParty = func(guildID string, mode types.Mode) *int {
			flows := Games.deps.Flows(guildID)
			if flows.IsCaptain(mode) {
				return flows.CaptainFor(mode).MaxPartySize
			}
			return flows.StandardFor(mode).MaxPartySize
		}
		d.Launch = func(group *queue.QueueGroup) error { _, err := Games.Launch(group); return err }
		d.Alert = StaffAlertPinging
		d.AlertPosts = StaffAlertPosts
	})
	partymgr.Parties.Wire(func(d *partymgr.PartyDeps) { d.InGame = Games.InGame })
	ratings.Elo.Wire(func(d *ratings.EloDeps) { d.InGame = Games.HasUnscoredGame })
	Games.deps.Parties = partymgr.Parties
	// The Discord side of a game: its channels (see the queue group's game_channels
	// config), the notices, map vote and captain draft, handing the match to the
	// engine, and re-forming lobbies once a game's players are free.
	create, remove := NewGameChannelEffects(base.Session.Load)
	discord := NewGameDiscordEffects(base.Session.Load)
	Games.SetEffects(GameEffects{
		CreateChannels: create,
		DeleteChannels: remove,
		CollectChat:    NewGameChatCollector(base.Session.Load),
		Notify:         discord.Notify,
		ApplyTeams:     discord.ApplyTeams,
		SelectMap:      discord.SelectMap,
		Host: func(g Game, selectMap func([]MapOption, bool) (*MapSelection, error)) (bool, error) {
			return HostEngine.Host(g, selectMap) // the engine package sets HostEngine after this package loads
		},
		Prepare:  func(g Game) { HostEngine.Prepare(g) },
		Released: queue.Released,
	})

}

// NewGameManager builds a manager, filling unset deps with the defaults.
func NewGameManager(d GameDeps) *GameManager {
	if d.Elo == nil {
		d.Elo = ratings.Elo
	}
	if d.ReleaseGroup == nil {
		d.ReleaseGroup = func(g, id string) { queue.Queues.RemoveGroup(g, id) }
	}
	if d.Flows == nil {
		cache := base.NewTTLCache[FlowSettings](base.ConfigCacheTTL)
		d.Flows = func(g string) FlowSettings {
			f, _ := cache.Get(g, func() (FlowSettings, error) { return loadFlows(g) })
			return f
		}
	}
	if d.RankIssues == nil {
		d.RankIssues = func(g string) []string {
			ranks, err := d.Elo.Ranks(g)
			if err != nil {
				return []string{"rank ladder could not be loaded"}
			}
			return ratings.ValidateLadder(ranks)
		}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.After == nil {
		d.After = func(dur time.Duration, f func()) func() {
			t := time.AfterFunc(dur, f)
			return func() { t.Stop() }
		}
	}
	if d.SubmitMin == 0 {
		d.SubmitMin = 3 * time.Minute
	}
	if d.PreGameLimit == 0 {
		d.PreGameLimit = 30 * time.Minute
	}
	if d.CleanupDelay == 0 {
		d.CleanupDelay = 30 * time.Second
	}
	return &GameManager{
		deps: d, games: map[uint]*liveGame{}, byPlayer: map[string]uint{}, byChannel: map[string]uint{},
		cleanups: map[uint]func(){},
	}
}

// FlowSettings returns the guild's team-formation settings.
func (m *GameManager) FlowSettings(guildID string) FlowSettings { return m.deps.Flows(guildID) }

// SetEffects wires the Discord side effects.
func (m *GameManager) SetEffects(e GameEffects) {
	m.fxMu.Lock()
	m.effects = e
	m.fxMu.Unlock()
}

func (m *GameManager) fx() GameEffects {
	m.fxMu.RLock()
	defer m.fxMu.RUnlock()
	return m.effects
}

func (m *GameManager) api() GameAPI {
	if m.deps.API != nil {
		return m.deps.API
	}
	return sdkGameAPI{database.Must()}
}

func (m *GameManager) logf(format string, a ...any) {
	appLog.EngineLogger.Error("[GameManager] " + fmt.Sprintf(format, a...))
}

func (m *GameManager) notify(g Game, ev GameEvent) {
	if fn := m.fx().Notify; fn != nil {
		fn(g, ev)
	}
}

func playerKey(guildID, userID string) string { return guildID + ":" + userID }

// ---- registry ---------------------------------------------------------------

func (m *GameManager) live(id uint) *liveGame {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.games[id]
}

// liveIn is live(id) restricted to one guild, so a command in one server can't touch another's game.
func (m *GameManager) liveIn(guildID string, id uint) *liveGame {
	lg := m.live(id)
	if lg == nil || lg.g.GuildID != guildID { // GuildID never changes after creation
		return nil
	}
	return lg
}

// indexLocked adds/removes lg's players and channels from the lookups. The
// player index only holds players of ACTIVE games: once a game is over its
// players may queue again even while it awaits staff scoring.
func (m *GameManager) indexLocked(lg *liveGame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range lg.g.Players {
		k := playerKey(lg.g.GuildID, p.UserID)
		if lg.out[p.UserID] {
			continue // eliminated: another game may already hold them
		}
		if lg.g.State.active() {
			m.byPlayer[k] = lg.g.ID
		} else if m.byPlayer[k] == lg.g.ID {
			delete(m.byPlayer, k)
		}
	}
	for _, c := range lg.g.Channels.ids() {
		m.byChannel[c] = lg.g.ID
	}
}

// ReleasePlayers frees players who are out of the match for good (a final death, or their team was eliminated): they no longer
// count as being in a game, so they can queue again at once while the match goes on without them.
func (m *GameManager) ReleasePlayers(gameID uint, userIDs ...string) {
	lg := m.live(gameID)
	if lg == nil || len(userIDs) == 0 {
		return
	}
	lg.mu.Lock()
	if lg.out == nil {
		lg.out = map[string]bool{}
	}
	for _, id := range userIDs {
		lg.out[id] = true
	}
	guildID := lg.g.GuildID
	lg.mu.Unlock()
	m.mu.Lock()
	for _, id := range userIDs {
		if k := playerKey(guildID, id); m.byPlayer[k] == gameID {
			delete(m.byPlayer, k)
		}
	}
	m.mu.Unlock()
	queue.ReleasedPlayers(guildID, userIDs...)
}

func (m *GameManager) unregister(lg *liveGame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range lg.g.Players {
		if k := playerKey(lg.g.GuildID, p.UserID); m.byPlayer[k] == lg.g.ID {
			delete(m.byPlayer, k)
		}
	}
	for c, id := range m.byChannel {
		if id == lg.g.ID {
			delete(m.byChannel, c)
		}
	}
	delete(m.games, lg.g.ID)
}

// Get returns a snapshot of a live game.
func (m *GameManager) Get(id uint) (Game, bool) {
	lg := m.live(id)
	if lg == nil {
		return Game{}, false
	}
	lg.mu.Lock()
	defer lg.mu.Unlock()
	return lg.g.clone(), true
}

// ByChannel finds the live game that owns a Discord channel (game chat or team voice).
func (m *GameManager) ByChannel(channelID string) (Game, bool) {
	m.mu.RLock()
	id, ok := m.byChannel[channelID]
	m.mu.RUnlock()
	if !ok {
		return Game{}, false
	}
	return m.Get(id)
}

// InGame reports whether the player is in an active (not yet finished, not yet submitted) game.
// The queue, party and decay code use it.
// VoidVoting reports whether the player's game has a void vote open.
func (m *GameManager) VoidVoting(guildID, userID string) bool {
	m.mu.RLock()
	id, ok := m.byPlayer[playerKey(guildID, userID)]
	m.mu.RUnlock()
	return ok && voidVoteOpen(id)
}

func (m *GameManager) InGame(guildID, userID string) bool {
	m.mu.RLock()
	id, ok := m.byPlayer[playerKey(guildID, userID)]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	// A game whose result was submitted is over for its players, even in the moment before it moves to scoring: they may queue.
	if lg := m.live(id); lg != nil {
		lg.mu.Lock()
		submitted := lg.submitted
		lg.mu.Unlock()
		return !submitted
	}
	return true
}

// HasUnscoredGame reports whether the player is in a game that is being played or is over but still waits for staff to score it.
// Their ELO and last-played time only change when the game is scored, so inactivity decay must treat them as playing: with only
// InGame a player whose game was submitted (they may queue again) was decayed for inactivity while their result was pending.
func (m *GameManager) HasUnscoredGame(guildID, userID string) bool {
	if m.InGame(guildID, userID) {
		return true
	}
	m.mu.RLock()
	live := make([]*liveGame, 0, len(m.games))
	for _, lg := range m.games {
		live = append(live, lg)
	}
	m.mu.RUnlock()
	for _, lg := range live {
		lg.mu.Lock()
		pending := lg.g.GuildID == guildID && lg.g.State == GamePostGame
		if pending {
			_, pending = lg.g.Player(userID)
		}
		lg.mu.Unlock()
		if pending {
			return true
		}
	}
	return false
}

// GuildGames lists the live games of a guild (including ones awaiting scoring).
func (m *GameManager) GuildGames(guildID string) []Game {
	m.mu.RLock()
	ids := make([]uint, 0, len(m.games))
	for id, lg := range m.games {
		if lg.g.GuildID == guildID {
			ids = append(ids, id)
		}
	}
	m.mu.RUnlock()
	slices.Sort(ids)
	var out []Game
	for _, id := range ids {
		if g, ok := m.Get(id); ok {
			out = append(out, g)
		}
	}
	return out
}

// ForceVoidSuggestions is the short list the /forcevoid autocomplete offers: the live games, the 100 most recent open or completed
// games, and (when a whole game number is typed) that game, however old. It makes at most two small database calls.
func (m *GameManager) ForceVoidSuggestions(guildID, typed string) ([]Game, error) {
	records, err := m.api().RecentGames(guildID, "waiting,in_progress,completed", 100)
	if err != nil {
		return nil, err
	}
	if n, convErr := strconv.Atoi(typed); convErr == nil && n > 0 {
		if rec, err := m.api().GameByNumber(guildID, n); err == nil && rec != nil {
			records = append(records, *rec)
		}
	}
	// Only the active season's games: older seasons' games are not offered.
	if _, seasonID, err := helpers.ActiveSeasonKey(guildID); err == nil && seasonID != 0 {
		inSeason := records[:0]
		for _, rec := range records {
			if rec.SeasonID == seasonID {
				inSeason = append(inSeason, rec)
			}
		}
		records = inSeason
	}
	return m.forceVoidGames(guildID, records), nil
}

// forceVoidGames turns game records into the games /forcevoid can act on (open or completed ones), preferring a live snapshot.
func (m *GameManager) forceVoidGames(guildID string, records []models.GamesModel) []Game {
	liveByID := make(map[uint]Game)
	for _, game := range m.GuildGames(guildID) {
		liveByID[game.ID] = game
	}

	seen := make(map[uint]struct{}, len(records))
	candidates := make([]Game, 0, len(records))
	for _, record := range records {
		switch record.Status {
		case string(types.GameStatusWaiting), string(types.GameStatusInProgress), string(types.GameStatusCompleted):
		default:
			continue
		}
		if _, exists := seen[record.ID]; exists {
			continue
		}
		seen[record.ID] = struct{}{}

		game := Game{
			ID:       record.ID,
			Number:   record.GameNumber,
			GuildID:  guildID,
			SeasonID: record.SeasonID,
			Map:      record.MapName,
			Mode:     types.Mode{Name: record.ModeName},
		}
		switch record.Status {
		case string(types.GameStatusWaiting):
			game.State = GameWaiting
		case string(types.GameStatusInProgress):
			game.State = GameInGame
		case string(types.GameStatusCompleted):
			game.State = GameCleanup
		}
		if live, exists := liveByID[record.ID]; exists {
			game = live
		}
		candidates = append(candidates, game)
	}
	return candidates
}

// ---- persistence ------------------------------------------------------------

// sync writes the game's status and runtime metadata. Syncs of one game are
// serialised and always snapshot the current state, so a late one can't
// overwrite a newer state.
func (m *GameManager) sync(lg *liveGame) {
	lg.syncMu.Lock()
	defer lg.syncMu.Unlock()
	lg.mu.Lock()
	g := lg.g.clone()
	lg.mu.Unlock()
	raw, err := json.Marshal(g)
	if err != nil {
		m.logf("marshal game %d: %v", g.ID, err)
		return
	}
	fields := map[string]any{"status": g.State.dbStatus(), "map_name": g.Map, "runtime_metadata": json.RawMessage(raw)}
	if g.Bot != "" {
		fields["bot_name"] = g.Bot
	}
	if _, err := m.api().UpdateGame(sdk.SystemActor, g.GuildID, g.ID, fields); err != nil {
		m.logf("sync game %d: %v", g.ID, err)
	}
}

// transition moves lg to a new state (caller holds lg.mu).
func (m *GameManager) transitionLocked(lg *liveGame, to GameState) {
	lg.g.State, lg.g.StateAt = to, m.deps.Now()
	m.indexLocked(lg)
}

// ============================================================================
// Launching a game (the queue hand-off)
// ============================================================================

// Launch turns a formed lobby into a game and starts its pre-game pipeline in
// the background. The lobby is released from the queue either way, so on error
// its players are free to queue again.
func (m *GameManager) Launch(group *queue.QueueGroup) (Game, error) {
	g, err := m.Start(group)
	if err != nil {
		return Game{}, err
	}
	go m.RunPreGame(g.ID)
	go m.refreshProfiles(g)
	return g, nil
}

// refreshProfiles brings every player's nickname (it shows their ELO) and rank role in line with their rating in the game's
// season. A new season starts everyone over, and nothing else rewrites the nickname until their first result there, so it
// would keep showing last season's ELO through the draft.
func (m *GameManager) refreshProfiles(g Game) {
	for _, p := range g.Players {
		ratings.RefreshRankRoles(g.GuildID, p.UserID)
	}
}

// Start validates the lobby, creates the game and its participant rows, and
// registers it in the WAITING state. Nothing happens in Discord yet.
func (m *GameManager) Start(group *queue.QueueGroup) (Game, error) { return m.start(group, nil) }

// start is Start for a queue lobby (custom nil) or a staff-built game (see StartCustom).
func (m *GameManager) start(group *queue.QueueGroup, custom *CustomGame) (Game, error) {
	if group == nil || len(group.Players) == 0 || group.ChannelID == "" || group.CategoryID == "" {
		return Game{}, ErrInvalidGroup
	}
	defer m.deps.ReleaseGroup(group.GuildID, group.ID)
	if !helpers.GuildPlanActive(group.GuildID) {
		return Game{}, helpers.ErrPlanInactive
	}

	if custom == nil { // a staff-built game sets its own team sizes
		teams, perTeam := TeamLayout(group.Mode)
		slots := queue.RequiredSlots(group.Mode)
		if slots <= 0 || slots%teams != 0 || len(group.Players) != slots || perTeam < 1 {
			return Game{}, fmt.Errorf("%w: %d players for a %d-slot mode", ErrBadRoster, len(group.Players), slots)
		}
	}

	seen := map[string]bool{}
	for _, p := range group.Players {
		if seen[p.UserID] {
			return Game{}, fmt.Errorf("%w: duplicate player %s", ErrInvalidGroup, p.UserID)
		}
		seen[p.UserID] = true
	}
	// Claim every player up front (all or nothing) so two launches racing for
	// the same player can't both get past the check; the claim becomes the
	// real index entry once the game exists, or is dropped on failure.
	if busy := m.reserve(group.GuildID, group.Players); busy != "" {
		return Game{}, &BusyError{busy}
	}
	registered := false
	defer func() {
		if !registered {
			m.unreserve(group.GuildID, group.Players)
		}
	}()

	season, err := m.api().ActiveSeason(group.GuildID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return Game{}, ErrNoSeason
		}
		return Game{}, err
	}

	ids := make([]string, len(group.Players))
	for i, p := range group.Players {
		ids[i] = p.UserID
	}
	ratings := m.deps.Elo.Ratings(group.GuildID, ids)

	now := m.deps.Now()
	g := Game{
		GuildID: group.GuildID, SeasonID: season, Mode: group.Mode, Map: MapUndecided, State: GameWaiting,
		Captain:        custom == nil && m.deps.Flows(group.GuildID).IsCaptain(group.Mode),
		Custom:         custom,
		QueueChannelID: group.ChannelID, QueueCategoryID: group.CategoryID,
		CreatedAt: now, StateAt: now, Channels: GameChannels{Voice: map[string]string{}},
	}
	for _, pg := range group.Parties {
		g.Parties = append(g.Parties, partymgr.PartyGroup{Leader: pg.Leader, Members: slices.Clone(pg.Members)})
	}

	var missing []string
	rows := make([]models.GamePlayersModel, 0, len(group.Players))
	for _, p := range group.Players {
		pid, err := m.api().PlayerID(group.GuildID, p.UserID)
		if err != nil {
			if sdk.IsNotFound(err) {
				missing = append(missing, p.UserID)
				continue
			}
			return Game{}, err
		}
		g.Players = append(g.Players, GamePlayer{UserID: p.UserID, Username: p.Username, PlayerID: pid, Team: teamUnassigned, EloBefore: ratings[p.UserID]})
		rows = append(rows, models.GamePlayersModel{PlayerID: pid, Team: teamUnassigned, InParty: g.inParty(p.UserID)})
	}
	if len(missing) > 0 {
		return Game{}, &NotRegisteredError{missing}
	}

	rec, err := m.api().CreateGame(sdk.SystemActor, group.GuildID, sdk.CreateGameRequest{SeasonID: &season, MapName: g.Map, ModeName: group.Mode.Name, Participants: rows})
	if err != nil {
		return Game{}, err
	}
	g.ID, g.Number, g.Total = rec.ID, rec.GameNumber, rec.GuildNumber

	lg := &liveGame{g: g}
	m.mu.Lock()
	m.games[g.ID] = lg
	m.mu.Unlock()
	registered = true
	lg.mu.Lock()
	m.indexLocked(lg)
	snap := lg.g.clone()
	lg.mu.Unlock()
	m.sync(lg)
	return snap, nil
}

// reserved marks a player claimed by a launch that has no game ID yet.
const reserved = 0

// reserve claims all players for a launch, or none: it returns the first
// player already in (or claimed for) a game.
func (m *GameManager) reserve(guildID string, players []queue.QueueMember) (busy string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range players {
		if _, in := m.byPlayer[playerKey(guildID, p.UserID)]; in {
			return p.UserID
		}
	}
	for _, p := range players {
		m.byPlayer[playerKey(guildID, p.UserID)] = reserved
	}
	return ""
}

func (m *GameManager) unreserve(guildID string, players []queue.QueueMember) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range players {
		if k := playerKey(guildID, p.UserID); m.byPlayer[k] == reserved {
			delete(m.byPlayer, k)
		}
	}
}

const (
	// MapUndecided is the map name of a game before its map is chosen.
	MapUndecided   = "TBD"
	teamUnassigned = "Unassigned"
)

func isCaptainMode(m types.Mode) bool {
	t := strings.ToLower(strings.TrimSpace(m.Type))
	return t == "captains" || t == "captain" || t == "elo" // "captain" and "elo" are legacy names of the captain flow
}

// isRandomMode reports whether the mode's type is "random": a plain shuffle when it picks no formation.
func isRandomMode(m types.Mode) bool {
	return strings.EqualFold(strings.TrimSpace(m.Type), "random")
}

func (g Game) inParty(userID string) bool {
	for _, pg := range g.Parties {
		if slices.Contains(pg.Members, userID) {
			return true
		}
	}
	return false
}

// ============================================================================
// Pre-game

func init() {
	helpers.GameNumberInChannel = func(channelID string) (int, bool) {
		if game, ok := Games.ByChannel(channelID); ok {
			return game.Number, true
		}
		return 0, false
	}
}

// ReturnToGame puts a player who wandered into a queue channel back into their team's voice channel. It returns the game's id and
// whether the player was moved (false: no game, no team channel yet, or the move failed).
func (m *GameManager) ReturnToGame(s *discordgo.Session, guildID, userID string) (uint, bool) {
	m.mu.RLock()
	id, ok := m.byPlayer[playerKey(guildID, userID)]
	m.mu.RUnlock()
	if !ok {
		return 0, false
	}
	g, ok := m.Get(id)
	if !ok {
		return id, false
	}
	p, ok := g.Player(userID)
	if !ok {
		return id, false
	}
	vc := g.Channels.Voice[p.Team]
	if vc == "" {
		return id, false
	}
	return id, s.GuildMemberMove(guildID, userID, &vc) == nil
}
