package ratings

import (
	"encoding/json"
	"fmt"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/types"
	appLog "ranked-bedwars/src/utility"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// ============================================================================

// DecayConfig is the guild config's elo_engine.elo_decay section.
type DecayConfig struct {
	Enabled        bool `json:"enabled"`
	DecayFloor     int  `json:"decayFloor"`
	InactivityDays int  `json:"inactivityDays"`

	// Everything below is optional on the dashboard's Elo Decays tab (Queue Configuration).
	WarningHours   int    `json:"warningHours"`  // warn this long before a decay (default 12)
	IntervalDays   int    `json:"intervalDays"`  // repeat every this many days once decay has started (default 1)
	NotifyWarning  *bool  `json:"notifyWarning"` // tell the player a decay is coming (default on)
	NotifyApplied  *bool  `json:"notifyApplied"` // tell the player a decay happened (default on)
	NotifyVia      string `json:"notifyVia"`     // "dm" (default), "channel" or "both"
	AlertChannelID string `json:"alertChannelId"`
}

// warningWindow is how long before a decay the player is warned.
func (c DecayConfig) warningWindow() time.Duration {
	if c.WarningHours <= 0 {
		return defaultDecayWarning
	}
	return time.Duration(min(c.WarningHours, 24*14)) * time.Hour
}

// interval is how often a decay repeats once it has started.
func (c DecayConfig) interval() time.Duration {
	if c.IntervalDays <= 0 {
		return defaultDecayInterval
	}
	return time.Duration(min(c.IntervalDays, 365)) * 24 * time.Hour
}

func (c DecayConfig) wantsWarning() bool { return c.NotifyWarning == nil || *c.NotifyWarning }
func (c DecayConfig) wantsApplied() bool { return c.NotifyApplied == nil || *c.NotifyApplied }

// Via is where notices go: "dm", "channel" or "both".
func (c DecayConfig) Via() string {
	switch c.NotifyVia {
	case "channel", "both":
		return c.NotifyVia
	}
	return "dm"
}

// DecayNoticeKind is defined in package types (elo.go).
type DecayNoticeKind = types.DecayNoticeKind

const (
	DecayWarning DecayNoticeKind = iota // a decay is due within the warning window
	DecayApplied                        // ELO was just removed
)

// DecayNotice is raised by the sweeper for the player concerned.
type DecayNotice struct {
	Kind                  DecayNoticeKind
	GuildID, UserID       string
	Amount, Before, After int
	InactivityDays        int
	At                    time.Time   // Warning: when the decay is due
	Config                DecayConfig // the guild's decay settings when the notice was raised (where it goes)
}

// EloDeps are the manager's outside dependencies; every field is optional
// (nil = database-backed or safe default), which also lets tests run without a
// database.
type EloDeps struct {
	API        EloAPI
	Ranks      func(guildID string) ([]types.Rank, error)
	Perks      func(guildID, userID string) PerkContext // nil = no perks; needs the member's roles, so the Discord layer supplies it
	Party      func(guildID string) types.PartiesConfig
	PartyGames func(guildID, userID string) int
	Decay      func(guildID string) DecayConfig  // the guild's decay rules (elo_engine.elo_decay), the same for every queue and mode
	InGame     func(guildID, userID string) bool // players in a game, or with one still awaiting staff scoring, never decay
	Now        func() time.Time
}

// EloManager: see the package comment above.
type EloManager struct {
	deps  EloDeps
	locks sync.Map // "guild:user" -> *sync.Mutex

	mu       sync.RWMutex
	notify   func(DecayNotice) bool // reports whether the notice was delivered
	rankSync func(guildID, userID, oldRank, newRank string)
}

// Wire sets dependencies that live in packages built on top of this one (parties, games).
func (m *EloManager) Wire(set func(*EloDeps)) { set(&m.deps) }

// Elo is the process-wide manager.
var Elo = NewEloManager(EloDeps{})

// NewEloManager builds a manager, filling unset deps with the database-backed defaults.
func NewEloManager(d EloDeps) *EloManager {
	if d.Ranks == nil {
		cache := base.NewTTLCache[[]types.Rank](base.ConfigCacheTTL)
		d.Ranks = func(g string) ([]types.Rank, error) {
			return cache.Get(g, func() ([]types.Rank, error) { return helpers.GuildMetaRanks(g) })
		}
	}
	if d.Party == nil {
		d.Party = func(string) types.PartiesConfig { return types.PartiesConfig{} }
	}
	if d.PartyGames == nil {
		d.PartyGames = func(string, string) int { return 0 }
	}
	if d.Decay == nil {
		cache := base.NewTTLCache[DecayConfig](base.ConfigCacheTTL)
		d.Decay = func(g string) DecayConfig {
			c, _ := cache.Get(g, func() (DecayConfig, error) { return loadDecayConfig(g) })
			return c
		}
	}
	if d.InGame == nil {
		d.InGame = func(string, string) bool { return false }
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &EloManager{deps: d}
}

func loadDecayConfig(guildID string) (DecayConfig, error) {
	raw, err := database.Must().GuildConfig.Field(guildID, "elo_engine")
	if err != nil {
		if sdk.IsNotFound(err) {
			return DecayConfig{}, nil
		}
		return DecayConfig{}, err
	}
	var engine struct {
		Decay DecayConfig `json:"elo_decay"`
	}
	if err := json.Unmarshal(raw, &engine); err != nil {
		return DecayConfig{}, err
	}
	engine.Decay.DecayFloor = max(0, engine.Decay.DecayFloor)
	return engine.Decay, nil
}

// GuildPerks loads the guild's enabled, named perks from the game config.
func GuildPerks(guildID string) ([]Perk, error) {
	raw, err := database.Must().GameConfig.Field(guildID, "perks")
	if err != nil {
		if sdk.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	var all []Perk
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	out := all[:0]
	for _, p := range all {
		if p.IsEnabled && strings.TrimSpace(p.Name) != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// SetPerkResolver wires the lookup of a player's active perks. It needs the
// member's roles, so the Discord layer supplies it; without it nobody has perks.
func (m *EloManager) SetPerkResolver(fn func(guildID, userID string) PerkContext) {
	m.mu.Lock()
	m.deps.Perks = fn
	m.mu.Unlock()
}

// SetHooks wires the outward effects: notify delivers decay notices (returning
// whether they got through, so undelivered warnings are retried) and rankSync
// updates the player's rank role after their rank changed.
func (m *EloManager) SetHooks(notify func(DecayNotice) bool, rankSync func(guildID, userID, oldRank, newRank string)) {
	m.mu.Lock()
	m.notify, m.rankSync = notify, rankSync
	m.mu.Unlock()
}

func (m *EloManager) API() EloAPI {
	if m.deps.API != nil {
		return m.deps.API
	}
	return sdkEloAPI{database.Must()}
}

func (m *EloManager) PerksOf(guildID, userID string) PerkContext {
	m.mu.RLock()
	fn := m.deps.Perks
	m.mu.RUnlock()
	if fn == nil {
		return PerkContext{}
	}
	return fn(guildID, userID)
}

// lock serialises manager writes to one player's ELO so a claim, an
// adjustment and a decay tick can't interleave their read-modify-write.
// It does not cover the database's own CompleteGame.
func (m *EloManager) lock(guildID, userID string) func() {
	v, _ := m.locks.LoadOrStore(guildID+":"+userID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (m *EloManager) logf(format string, a ...any) {
	appLog.EngineLogger.Error("[EloManager] " + fmt.Sprintf(format, a...))
}

// Rating is the player's active-season ELO; ok=false when it can't be read.
// A player with no stats yet counts as SeasonStartElo.
func (m *EloManager) Rating(guildID, userID string) (int, bool) {
	st, err := m.API().Stats(guildID, userID)
	if err != nil {
		return 0, false
	}
	return st.Elo, true
}

// Ratings is Rating for a roster, for team balancing and captain picks.
// Players whose ELO can't be read are left out.
func (m *EloManager) Ratings(guildID string, userIDs []string) map[string]int {
	out := make(map[string]int, len(userIDs))
	for _, u := range userIDs {
		if elo, ok := m.Rating(guildID, u); ok {
			out[u] = elo
		}
	}
	return out
}

// ============================================================================
// Scoring a game
// ============================================================================

// ScoreInput is defined in package types (elo.go).
type ScoreInput = types.ScoreInput

// EloResult is defined in package types (elo.go).
type EloResult = types.EloResult

// ScoreGame computes every player's result without writing anything; hand the
// After values to the database's CompleteGame, then call ApplyRanks.
func (m *EloManager) ScoreGame(in ScoreInput) ([]EloResult, error) {
	ranked := in.Mode == nil || in.Mode.Rated()
	var ranks []types.Rank
	if ranked {
		var err error
		if ranks, err = m.deps.Ranks(in.GuildID); err != nil {
			return nil, err
		}
		if len(ranks) == 0 {
			return nil, ErrNoRanks
		}
	}

	present := map[string]bool{}
	for _, id := range in.Winners {
		present[id] = true
	}
	for _, id := range in.Losers {
		present[id] = true
	}
	for id := range present {
		markRankPending(in.GuildID, id)
	}
	partyOf := map[string][]string{}
	for _, g := range in.Parties {
		for _, id := range g.Members {
			partyOf[id] = g.Members
		}
	}
	hasMate := func(id string) bool {
		for _, mate := range partyOf[id] {
			if mate != id && present[mate] {
				return true
			}
		}
		return false
	}

	var partyCfg types.PartiesConfig
	if ranked {
		partyCfg = m.deps.Party(in.GuildID)
	}

	// Every player's stats are fetched at once: one call each, and waiting for them one after another is what made scoring slow.
	type fetched struct {
		st  *models.PlayerStatsModel
		err error
	}
	stats := make(map[string]*fetched, len(present))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for id := range present {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := m.API().StatsInSeason(in.GuildID, id, in.SeasonID)
			mu.Lock()
			stats[id] = &fetched{st, err}
			mu.Unlock()
		}()
	}
	wg.Wait()

	results := make([]EloResult, 0, len(present))
	seen := map[string]bool{}
	score := func(id string, win bool) error {
		if seen[id] {
			return nil
		}
		seen[id] = true
		f := stats[id]
		if f.err != nil {
			return fmt.Errorf("stats for %s: %w", id, f.err)
		}
		st, err := f.st, error(nil)
		res := EloResult{UserID: id, SeasonID: st.SeasonID, Win: win, MVP: win && id == in.MVP}
		if !ranked {
			res.EloOutcome = EloOutcome{Before: st.Elo, After: st.Elo, RankBefore: st.Rank, RankAfter: st.Rank}
			results = append(results, res)
			return nil
		}
		perks := m.PerksOf(in.GuildID, id)
		if perks.Err != nil {
			return fmt.Errorf("perks for %s: %w", id, perks.Err)
		}
		oi := OutcomeInput{
			Base: st.Elo, Win: win, MVP: res.MVP, Ranks: ranks,
			Perks: perks,
			Grant: !st.StartingEloClaimed && st.GamesPlayed == 0,
		}
		if hasMate(id) {
			oi.Party, oi.PartyGames = &partyCfg, m.deps.PartyGames(in.GuildID, id)
		}
		if res.EloOutcome, err = ComputeOutcome(oi); err != nil {
			return fmt.Errorf("score %s: %w", id, err)
		}
		results = append(results, res)
		return nil
	}
	for _, id := range in.Winners {
		if err := score(id, true); err != nil {
			return nil, err
		}
	}
	for _, id := range in.Losers {
		if err := score(id, false); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// ApplyRanks stores each scored player's new rank name and runs the rank-role
// hook. Call it after CompleteGame; failures are logged, not fatal, since the
// ELO itself is already saved and SyncRank can repair a rank later.
func (m *EloManager) ApplyRanks(actor, guildID string, results []EloResult) {
	decayWatch.Track(guildID)
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4) // a few at a time: each is a database call and the Discord role and nickname edits
	for _, r := range results {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done(); clearRankPending(guildID, r.UserID) }()
			if _, err := m.SyncRank(actor, guildID, r.UserID); err != nil {
				m.logf("rank sync %s/%s: %v", guildID, r.UserID, err)
			}
		}()
	}
	wg.Wait()
}

// ============================================================================
// Ranks
// ============================================================================

// Adjustment is defined in package types (elo.go).
type Adjustment = types.Adjustment

// applyLocked applies one rating effect and brings the stored rank in line. The database applies it under a row lock
// to the part it names, so games, modifications and decay stay separate; After is the resulting derived rating.
func (m *EloManager) applyLocked(actor, guildID, userID string, st *models.PlayerStatsModel, e sdk.EloEffect) (*Adjustment, error) {
	adj := &Adjustment{Before: st.Elo, After: st.Elo, RankBefore: st.Rank, RankAfter: st.Rank}
	if e.Delta != 0 || e.SetTo != nil {
		updated, err := m.API().ApplyElo(actor, guildID, userID, st.SeasonID, e)
		if err != nil {
			return nil, err
		}
		adj.After = updated.Elo
	}
	rank, err := m.syncRank(actor, guildID, userID, st, adj.After)
	adj.RankAfter = rank
	return adj, err
}

// Preview is defined in package types (elo.go).
type Preview = types.Preview

// ============================================================================
// Starting grant (/claimelo)
// ============================================================================

// ClaimStartingElo pays the perk starting grant once, before the player's
// first ranked game of the season (a wipe re-opens it). The claim record
// (amount, time, perks) is saved first and rolled back if paying fails, so a
// crash can lose a claim but never pay it twice.
func (m *EloManager) ClaimStartingElo(actor, guildID, userID string) (*Claim, error) {
	defer m.lock(guildID, userID)()
	st, err := m.API().Stats(guildID, userID)
	if err != nil {
		return nil, err
	}
	if st.StartingEloClaimed || st.GamesPlayed > 0 {
		return nil, ErrStartingGrantUsed
	}
	pc := m.PerksOf(guildID, userID)
	if pc.Err != nil {
		return nil, pc.Err
	}
	grant := pc.StartingGrant()
	if grant <= 0 {
		return nil, ErrNoStartingGrant
	}
	at := m.deps.Now()
	if _, err := m.API().Patch(actor, guildID, userID, st.SeasonID, claimRecord(grant, pc.Names(), at)); err != nil {
		return nil, err
	}
	adj, err := m.applyLocked(actor, guildID, userID, st, sdk.EloEffect{Part: "game", Delta: grant, Reason: "starting grant"})
	if adj == nil {
		if _, rbErr := m.API().Patch(actor, guildID, userID, st.SeasonID, clearedClaim()); rbErr != nil {
			m.logf("claim rollback for %s/%s failed: %v", guildID, userID, rbErr)
		}
		return nil, err
	}
	return &Claim{Adjustment: adj, Amount: grant, Perks: pc.Names(), At: at}, err // a rank-sync error still leaves the grant paid
}

// Claim is defined in package types (elo.go).
type Claim = types.Claim

// claimRecord is the stats patch that marks a starting grant as paid.
func claimRecord(amount int, perks []string, at time.Time) map[string]any {
	return map[string]any{
		"starting_elo_claimed":        true,
		"starting_elo_claimed_amount": amount,
		"starting_elo_claimed_at":     at.UTC(),
		"starting_elo_claimed_perks":  strings.Join(perks, ", "),
	}
}

// clearedClaim is the stats patch that re-opens the starting grant.
func clearedClaim() map[string]any {
	return map[string]any{
		"starting_elo_claimed":        false,
		"starting_elo_claimed_amount": 0,
		"starting_elo_claimed_at":     nil,
		"starting_elo_claimed_perks":  "",
	}
}

// ReopenStartingGrants makes the starting grant claimable (and payable by the next ranked game) again for players
// whose first game was just reversed; see GameManager.startingGrantHolders.
func (m *EloManager) ReopenStartingGrants(actor, guildID string, userIDs []string) {
	for _, userID := range userIDs {
		func() {
			defer m.lock(guildID, userID)()
			st, err := m.API().Stats(guildID, userID)
			if err == nil {
				_, err = m.API().Patch(actor, guildID, userID, st.SeasonID, clearedClaim())
			}
			if err != nil {
				m.logf("reopen starting grant for %s/%s: %v", guildID, userID, err)
			}
		}()
	}
}

// PerkMultiplier is the win-gain multiplier of the player's best perk (1 = none), for staff /win grants.
func (m *EloManager) PerkMultiplier(guildID, userID string) float64 {
	return m.PerksOf(guildID, userID).Multiplier()
}

// RecordStartingGrants stores the claim record for players whose first ranked
// game paid them the starting grant. Call it after CompleteGame; failures are
// logged, not fatal, since the ELO itself is already saved.
func (m *EloManager) RecordStartingGrants(actor, guildID string, results []EloResult) {
	for _, r := range results {
		if r.PerkStart <= 0 {
			continue
		}
		rec := claimRecord(r.PerkStart, m.PerksOf(guildID, r.UserID).Names(), m.deps.Now())
		if _, err := m.API().Patch(actor, guildID, r.UserID, r.SeasonID, rec); err != nil {
			m.logf("record starting grant for %s/%s: %v", guildID, r.UserID, err)
		}
	}
}

// ============================================================================
// Decay

// Ranks is the guild's rank ladder.
func (m *EloManager) Ranks(guildID string) ([]types.Rank, error) { return m.deps.Ranks(guildID) }

// PartyConfig is the guild's party rules, which decide how a party's players are rated together.
func (m *EloManager) PartyConfig(guildID string) types.PartiesConfig { return m.deps.Party(guildID) }
