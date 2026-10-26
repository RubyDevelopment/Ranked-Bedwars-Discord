package helpers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	appLog "ranked-bedwars/src/utility"
	baseutility "ranked-bedwars/src/utility"
)

// ============================================================================
// ELO decay
// ============================================================================

// The decay engine (Elo) warns players before their ELO decays, applies the decay each interval and tells
// them. How long before, how often, how much (the rank's decay), the floor and where the notices go are the guild's
// settings on the dashboard's Season Config page. This file starts the hourly sweep and delivers the notices.

var (
	decayLogger  = baseutility.CreateModuleLogger("ELO_DECAY", "discord")
	decaySession atomic.Pointer[discordgo.Session]
	decayOnce    sync.Once
)

// decaySweepEvery is how often every guild is checked for warnings and due decays.
func init() {
	Elo.SetHooks(deliverDecayNotice, syncRankRoles)
}

// decayWatch sweeps only guilds where a player can still decay. A finished game adds a guild; a sweep drops it once
// decay is off or nobody is left above the floor.
var decayWatch = newGuildWatch("EloDecay", decaySweepEvery, func(guildID string) bool {
	Elo.SweepDecay(guildID)
	return Elo.DecayPending(guildID)
})

// StartDecaySweeper tracks every guild the session is in that already has players who can decay; the ready event
// calls it. Guilds with none are not swept until a game finishes there.
func StartDecaySweeper(s *discordgo.Session) {
	decaySession.Store(s)
	decayOnce.Do(func() {
		ids := make([]string, 0, len(s.State.Guilds))
		for _, g := range s.State.Guilds {
			ids = append(ids, g.ID)
		}
		go decayWatch.Seed(ids, Elo.DecayPending)
	})
}

// syncRankRoles refreshes the player's rank role and nickname after their rank changed.
func syncRankRoles(guildID, userID, _, _ string) {
	s := decaySession.Load()
	if s == nil {
		return
	}
	view, err := database.Must().Player.Get(guildID, userID)
	if err != nil {
		return
	}
	if _, err := SyncRegisteredPlayerProfile(s, guildID, view.Player.ID, userID); err != nil {
		decayLogger.Warn(fmt.Sprintf("profile sync for %s in guild %s: %v", userID, guildID, err))
	}
}

// deliverDecayNotice tells the player, by direct message and/or in the guild's alert channel as configured. It
// reports whether the notice got through, so a warning nobody received is tried again on the next sweep.
func deliverDecayNotice(n DecayNotice) bool {
	s := decaySession.Load()
	if s == nil {
		return false
	}
	var embed *discordgo.MessageEmbed
	switch n.Kind {
	case DecayWarning:
		embed = DecayWarningEmbed(n)
	case DecayApplied:
		embed = DecayAppliedEmbed(n)
	default:
		return false
	}

	via, channelID := n.Config.Via(), n.Config.AlertChannelID
	sent := false
	if via == "dm" || via == "both" {
		if dm, err := s.UserChannelCreate(n.UserID); err == nil {
			_, err = s.ChannelMessageSendEmbed(dm.ID, embed)
			sent = err == nil
		}
	}
	// Closed DMs fall back to the channel when one is set.
	if channelID != "" && (via == "channel" || via == "both" || !sent) {
		_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Content:         "<@" + n.UserID + ">",
			Embeds:          []*discordgo.MessageEmbed{embed},
			AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{n.UserID}},
		})
		sent = sent || err == nil
	}
	return sent
}

// decaySweepEvery is how often every guild is checked for warnings and due decays.
const decaySweepEvery = time.Hour

// EloManager owns everything rating related: scoring a finished game, perk
// grants (/claimelo), manual adjustments, reverting a voided game, rank
// resolution and inactivity decay.
//
// Scoring is flat-K, not expectation based: a win is worth the K-factor of the
// player's current rank (perk-multiplied, plus an MVP bonus), a loss costs the
// rank's loss factor, floored at 0. The old bot also shipped a textbook
// expected-score formula that no caller ever used; it is deliberately gone.
//
// ScoreGame only COMPUTES. The database's CompleteGame persists the ELO it is
// given (EloBefore/After/Change on the participant rows) together with the
// counters in one transaction, so writing it here too would double-apply it.
// The manager persists only what CompleteGame does not: rank names, the
// starting-grant flag, manual adjustments and decay.

// ============================================================================
// Constants & errors
// ============================================================================

const (
	// SeasonStartElo is the rating a fresh or wiped season row starts at. It is
	// never the lowest rank's min_elo (that once spawned players at e.g. 800).
	SeasonStartElo = 0

	maxPerkStartingElo    = 5000
	maxBoosterStacks      = 2
	perkEloBonusPerStep   = 100            // flat ELO per +1.0 of multiplier
	defaultDecayWarning   = 12 * time.Hour // how long before a decay the player is warned
	defaultDecayInterval  = 24 * time.Hour // how often a decay repeats once it has started
	maxPreviewCount       = 100
	eloConfigCacheTTL     = 30 * time.Second
	defaultDecaySweepTick = time.Hour
)

var (
	ErrNoRanks           = errors.New("no ranks configured")
	ErrInvalidRank       = errors.New("rank has an invalid K-factor")
	ErrNoStartingGrant   = errors.New("no perk grants starting ELO")
	ErrStartingGrantUsed = errors.New("starting ELO already claimed or games already played")
	ErrAtDecayFloor      = errors.New("player is at the decay floor")
	ErrDecayNotCurrent   = errors.New("decay belongs to a different season")
)

// ============================================================================
// Perks
// ============================================================================

// Perk is one entry of the game config's "perks" array.
type Perk struct {
	Name          string  `json:"name"`
	IsEnabled     bool    `json:"is_enabled"`
	EloMultiplier float64 `json:"elo_multiplier"`
	StartingElo   float64 `json:"starting_elo"`
	// MapVotingMultiplier is how much a map vote of a holder counts (1 = one vote).
	MapVotingMultiplier float64  `json:"map_voting_multiplier"`
	AllowedRoles        []string `json:"allowed_roles"`
	DeniedRoles         []string `json:"denied_roles"`
	Source              string   `json:"source,omitempty"`

	// Booster marks a perk gated on the server-booster roles (see MarkBoosters).
	Booster bool `json:"-"`
}

// PerkContext is the perks that apply to one player right now.
type PerkContext struct {
	Perks         []Perk
	BoosterStacks int // 0..2, from the booster roles
}

// QualifiesFor reports whether a member holding roleIDs gets this perk: it must
// be enabled and named, no denied role may be held (denied wins), and an empty
// allowed list means everyone. resolve maps a config entry (a snowflake or a
// role key) to a role ID; nil treats entries as snowflakes. Vote perks
// (Source "topgg_vote") are granted by vote state, not roles, so callers
// should not pass them here.
func (p Perk) QualifiesFor(roleIDs map[string]bool, resolve func(string) string) bool {
	if !p.IsEnabled || strings.TrimSpace(p.Name) == "" {
		return false
	}
	ref := func(s string) string {
		if resolve != nil {
			if r := resolve(s); r != "" {
				return r
			}
		}
		return s
	}
	for _, d := range p.DeniedRoles {
		if d != "" && roleIDs[ref(d)] {
			return false
		}
	}
	allowed := false
	for _, a := range p.AllowedRoles {
		if a == "" {
			continue
		}
		allowed = true
		if roleIDs[ref(a)] {
			return true
		}
	}
	return !allowed
}

// MarkBoosters flags perks whose allowed roles include a booster role.
func MarkBoosters(perks []Perk, boosterRoleIDs ...string) []Perk {
	set := make(map[string]bool, len(boosterRoleIDs))
	for _, id := range boosterRoleIDs {
		if id != "" {
			set[id] = true
		}
	}
	out := make([]Perk, len(perks))
	copy(out, perks)
	for i := range out {
		for _, r := range out[i].AllowedRoles {
			if set[r] {
				out[i].Booster = true
			}
		}
	}
	return out
}

func normMultiplier(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 1
	}
	return math.Round(v*1e6) / 1e6
}

func clampStartingElo(v float64) int {
	if math.IsNaN(v) {
		return 0
	}
	return int(math.Max(0, math.Min(maxPerkStartingElo, math.Round(v))))
}

// betterPerk orders perks: higher multiplier, then higher starting ELO, then name.
func betterPerk(a, b Perk) bool {
	if ma, mb := normMultiplier(a.EloMultiplier), normMultiplier(b.EloMultiplier); ma != mb {
		return ma > mb
	}
	if sa, sb := clampStartingElo(a.StartingElo), clampStartingElo(b.StartingElo); sa != sb {
		return sa > sb
	}
	return a.Name < b.Name
}

func topPerk(perks []Perk) *Perk {
	var best *Perk
	for i := range perks {
		if best == nil || betterPerk(perks[i], *best) {
			best = &perks[i]
		}
	}
	return best
}

func perkFlatBonus(p *Perk) int {
	if p == nil {
		return 0
	}
	m := normMultiplier(p.EloMultiplier)
	if m <= 1 {
		return 0
	}
	return int(math.Round(perkEloBonusPerStep * (m - 1)))
}

// Multiplier is the win-gain multiplier: the single best perk, never stacked.
func (c PerkContext) Multiplier() float64 {
	if p := topPerk(c.Perks); p != nil {
		return normMultiplier(p.EloMultiplier)
	}
	return 1
}

// Names lists the perks' names, for the claim record.
func (c PerkContext) Names() []string {
	out := make([]string, 0, len(c.Perks))
	for _, p := range c.Perks {
		out = append(out, strings.TrimSpace(p.Name))
	}
	return out
}

// StartingGrant is the one-time ELO granted at a player's first ranked game or
// by /claimelo: every perk's configured starting ELO stacks, plus the best
// perk's multiplier-tier bonus once, plus the best booster perk's bonus per
// booster stack.
func (c PerkContext) StartingGrant() int {
	if len(c.Perks) == 0 {
		return 0
	}
	total := 0
	for _, p := range c.Perks {
		total += clampStartingElo(p.StartingElo)
	}
	total += perkFlatBonus(topPerk(c.Perks))
	if stacks := min(max(c.BoosterStacks, 0), maxBoosterStacks); stacks > 0 {
		var boosters []Perk
		for _, p := range c.Perks {
			if p.Booster {
				boosters = append(boosters, p)
			}
		}
		total += perkFlatBonus(topPerk(boosters)) * stacks
	}
	return total
}

// ============================================================================
// Ranks
// ============================================================================

// RankFor returns the rank covering elo: the highest tier whose min_elo the
// rating has reached, the lowest tier for anything below the ladder, nil when
// no ranks are configured.
func RankFor(ranks []types.Rank, elo int) *types.Rank {
	if len(ranks) == 0 {
		return nil
	}
	sorted := SortedRanks(ranks)
	if r := RankForElo(sorted, elo); r != nil {
		return r
	}
	return &sorted[0]
}

func rankKFactors(r *types.Rank) (win, loss int, err error) {
	win, loss = r.KFactorWin, r.KFactorLoss
	if loss < 0 {
		loss = -loss
	}
	if win <= 0 || loss <= 0 {
		return 0, 0, fmt.Errorf("%w: %q", ErrInvalidRank, r.RankName)
	}
	return win, loss, nil
}

// ValidateLadder lists everything wrong with a rank ladder (empty = usable).
func ValidateLadder(ranks []types.Rank) []string {
	if len(ranks) == 0 {
		return []string{"Add at least one competitive rank"}
	}
	var issues []string
	for i, r := range ranks {
		label := strings.TrimSpace(r.RankName)
		if label == "" {
			issues = append(issues, fmt.Sprintf("rank #%d is missing a name", i+1))
			label = fmt.Sprintf("rank #%d", i+1)
		}
		if r.MinElo < 0 {
			issues = append(issues, fmt.Sprintf("%q needs a min ELO of 0 or higher", label))
		}
		if r.MaxElo != 0 && r.MaxElo <= r.MinElo {
			issues = append(issues, fmt.Sprintf("%q needs a max ELO above its min ELO (or leave it open on the top tier)", label))
		}
		if r.KFactorWin <= 0 {
			issues = append(issues, fmt.Sprintf("%q needs a win K-factor above 0", label))
		}
		if r.KFactorLoss == 0 {
			issues = append(issues, fmt.Sprintf("%q needs a loss K-factor above 0", label))
		}
		if r.MVPBonus < 0 {
			issues = append(issues, fmt.Sprintf("%q needs an MVP bonus of 0 or higher", label))
		}
		if r.Decay < 0 {
			issues = append(issues, fmt.Sprintf("%q needs a decay of 0 or higher", label))
		}
		if strings.TrimSpace(r.RoleID) == "" {
			issues = append(issues, fmt.Sprintf("%q needs a Discord role assigned", label))
		}
	}
	sorted := SortedRanks(ranks)
	if sorted[0].MinElo > 0 {
		issues = append(issues, fmt.Sprintf("lowest rank %q must start at ELO 0", sorted[0].RankName))
	}
	for i := 0; i+1 < len(sorted); i++ {
		cur, next := sorted[i], sorted[i+1]
		switch {
		case cur.MaxElo == 0:
			issues = append(issues, fmt.Sprintf("%q needs a max ELO (only the top tier may leave it open)", cur.RankName))
		case cur.MaxElo+1 < next.MinElo:
			issues = append(issues, fmt.Sprintf("gap between %q and %q: raise %q max ELO to %d", cur.RankName, next.RankName, cur.RankName, next.MinElo-1))
		case cur.MaxElo >= next.MinElo:
			issues = append(issues, fmt.Sprintf("%q and %q overlap", cur.RankName, next.RankName))
		}
	}
	return issues
}

// ============================================================================
// Scoring maths (pure)
// ============================================================================

// EloOutcome is defined in package types (elo.go).
type EloOutcome = types.EloOutcome

// OutcomeInput is everything one player's result depends on.
type OutcomeInput struct {
	Base     int
	Win, MVP bool
	Ranks    []types.Rank
	Perks    PerkContext

	// Party is the guild party config when this player queued with a
	// teammate who is in the same match; nil = no party modifier.
	Party      *types.PartiesConfig
	PartyGames int // the party's games played, against the modifier threshold

	// Grant is true while the starting grant is unclaimed and no game has been played.
	Grant bool
}

// partyDeltas applies the party ELO modifier once the party has played enough
// games. The multiplier scales both directions. The flat adjustment is a bonus
// to the party's result: added to a win, subtracted from a loss, so a negative
// value penalises the party both ways (the old bot added it to both, which
// made a penalty cheaper to lose with). A 0 multiplier means "unset" and is
// treated as 1.
func partyDeltas(cfg types.PartiesConfig, games, win, loss int) (int, int) {
	if !cfg.EloModifierIsEnabled || games < cfg.EloModifierGamesThreshold {
		return win, loss
	}
	mult := cfg.EloModifierMultiplier
	if mult <= 0 {
		mult = 1
	}
	flat := cfg.EloModifierFlatAdjustment
	return max(0, int(math.Round(float64(win)*mult))+flat), max(0, int(math.Round(float64(loss)*mult))-flat)
}

// ComputeOutcome scores one player. The K-factors come from the rank the player
// holds when the game is played (after any starting grant is paid, so a first
// game is scored at the rating the player actually has).
func ComputeOutcome(in OutcomeInput) (EloOutcome, error) {
	base := max(0, in.Base)
	rank := RankFor(in.Ranks, base)
	if rank == nil {
		return EloOutcome{}, ErrNoRanks
	}
	out := EloOutcome{Before: base, RankBefore: rank.RankName}

	working := base
	if in.Grant {
		out.PerkStart = in.Perks.StartingGrant()
		working += out.PerkStart
		if out.PerkStart > 0 {
			rank = RankFor(in.Ranks, working)
		}
	}

	kWin, kLoss, err := rankKFactors(rank)
	if err != nil {
		return EloOutcome{}, err
	}
	if in.Party != nil {
		kWin, kLoss = partyDeltas(*in.Party, in.PartyGames, kWin, kLoss)
	}

	if in.Win {
		out.Gain = max(0, int(math.Round(float64(kWin)*in.Perks.Multiplier())))
		if in.MVP {
			out.MVPBonus = max(0, rank.MVPBonus)
		}
		out.After = working + out.Gain + out.MVPBonus
	} else {
		out.Loss = min(kLoss, working)
		out.After = working - out.Loss
	}
	out.Change = out.After - base
	out.RankAfter = RankFor(in.Ranks, out.After).RankName
	return out, nil
}

// MVPCandidate is defined in package types (elo.go).
type MVPCandidate = types.MVPCandidate

// SelectMVP picks the match MVP from the winners: most kills, then fewest
// deaths, then most beds destroyed, then a seeded pick (pass the game ID so a
// replay chooses the same player). "" when there is no eligible candidate.
func SelectMVP(cands []MVPCandidate, winners []string, seed int64) string {
	pool := cands
	if len(winners) > 0 {
		win := make(map[string]bool, len(winners))
		for _, w := range winners {
			win[w] = true
		}
		pool = nil
		for _, c := range cands {
			if win[c.ID] {
				pool = append(pool, c)
			}
		}
	}
	if len(pool) == 0 {
		return ""
	}
	keep := func(better func(a, b MVPCandidate) bool) {
		best := pool[0]
		for _, c := range pool[1:] {
			if better(c, best) {
				best = c
			}
		}
		var next []MVPCandidate
		for _, c := range pool {
			if !better(best, c) {
				next = append(next, c)
			}
		}
		pool = next
	}
	keep(func(a, b MVPCandidate) bool { return a.Kills > b.Kills })
	keep(func(a, b MVPCandidate) bool { return a.Deaths < b.Deaths })
	keep(func(a, b MVPCandidate) bool { return a.Beds > b.Beds })
	sort.Slice(pool, func(i, j int) bool { return pool[i].ID < pool[j].ID })
	return pool[uint64(seed)%uint64(len(pool))].ID
}

// NextDecayAt is when a player's next decay tick is due: inactivityDays after
// their last game, then every `interval` once decay has started (a decay counts as
// part of the current streak only if it happened after the last game).
func NextDecayAt(lastPlayed time.Time, lastDecay *time.Time, inactivityDays int, interval time.Duration) time.Time {
	if lastDecay != nil && lastDecay.After(lastPlayed) {
		return lastDecay.Add(interval)
	}
	return lastPlayed.Add(time.Duration(inactivityDays) * 24 * time.Hour)
}

// ============================================================================
// Persistence port
// ============================================================================

// EloAPI is defined in package types (elo.go).
type EloAPI = types.EloAPI

type sdkEloAPI struct{ s *sdk.Services }

func (a sdkEloAPI) Stats(g, u string) (*models.PlayerStatsModel, error) {
	return a.s.Stats.GetOrCreate(g, u, 0)
}

func (a sdkEloAPI) ApplyElo(actor, g, u string, season uint, e sdk.EloEffect) (*models.PlayerStatsModel, error) {
	return a.s.Stats.ApplyElo(actor, g, u, season, e)
}

func (a sdkEloAPI) Patch(actor, g, u string, season uint, f map[string]any) (*models.PlayerStatsModel, error) {
	return a.s.Stats.Patch(actor, g, u, season, f)
}

func (a sdkEloAPI) Reset(actor, g, u string, season uint, start int, rank string) (*models.PlayerStatsModel, error) {
	return a.s.Stats.Reset(actor, g, u, season, start, rank)
}

func (a sdkEloAPI) DecayCandidates(g string, days int) ([]models.PlayerStatsModel, error) {
	var all []models.PlayerStatsModel
	for off := 0; ; off += 100 {
		page, err := a.s.Client().DecayCandidates(context.Background(), g, sdk.Q("inactive_days", days, "limit", 100, "offset", off))
		if err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if len(page.Data) < 100 {
			return all, nil
		}
	}
}

func (a sdkEloAPI) Snowflake(g string, playerID uint) (string, error) {
	v, err := a.s.Player.Get(g, sdk.UintToString(playerID))
	if err != nil {
		return "", err
	}
	return v.Player.SnowflakeID, nil
}

func (a sdkEloAPI) CreateDecay(actor, g, u string, d models.PlayerDecaysModel) (uint, error) {
	out, err := a.s.Client().CreateDecay(context.Background(), actor, g, u, d)
	if err != nil {
		return 0, err
	}
	return out.ID, nil
}

func (a sdkEloAPI) GetDecay(g string, id uint) (*models.PlayerDecaysModel, error) {
	return a.s.Decays.Get(g, id)
}

func (a sdkEloAPI) DeleteDecay(actor, g string, id uint) error {
	return a.s.Decays.Delete(actor, g, id)
}

// ============================================================================
// Manager
// ============================================================================

// DecayConfig is the guild config's elo_engine.elo_decay section.
type DecayConfig struct {
	Enabled        bool `json:"enabled"`
	DecayFloor     int  `json:"decayFloor"`
	InactivityDays int  `json:"inactivityDays"`

	// Everything below is optional on the dashboard's Season Config page.
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
	Decay      func(guildID string) DecayConfig            // the guild-wide default (elo_engine.elo_decay): used by modes whose queues set no decay of their own
	QueueDecay func(guildID string) map[string]DecayConfig // each queue's own decay, by lower-case mode name
	InGame     func(guildID, userID string) bool           // in-game players never decay
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

// Elo is the process-wide manager.
var Elo = NewEloManager(EloDeps{})

// NewEloManager builds a manager, filling unset deps with the database-backed defaults.
func NewEloManager(d EloDeps) *EloManager {
	if d.Ranks == nil {
		cache := newTTLCache[[]types.Rank](eloConfigCacheTTL)
		d.Ranks = func(g string) ([]types.Rank, error) {
			return cache.get(g, func() ([]types.Rank, error) { return GuildMetaRanks(g) })
		}
	}
	if d.Party == nil {
		d.Party = func(g string) types.PartiesConfig { return Parties.Config(g) }
	}
	if d.PartyGames == nil {
		d.PartyGames = func(g, u string) int {
			if p := Parties.PartyOf(g, u); p != nil {
				return p.GamesPlayed
			}
			return 0
		}
	}
	if d.Decay == nil {
		cache := newTTLCache[DecayConfig](eloConfigCacheTTL)
		d.Decay = func(g string) DecayConfig {
			c, _ := cache.get(g, func() (DecayConfig, error) { return loadDecayConfig(g) })
			return c
		}
	}
	if d.QueueDecay == nil {
		cache := newTTLCache[map[string]DecayConfig](eloConfigCacheTTL)
		d.QueueDecay = func(g string) map[string]DecayConfig {
			out, _ := cache.get(g, func() (map[string]DecayConfig, error) { return loadQueueDecay(g) })
			return out
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

// loadQueueDecay reads the decay each enabled queue sets, keyed by the lower-case name of the mode it plays. When several
// queues play one mode, the first that sets a decay wins.
func loadQueueDecay(guildID string) (map[string]DecayConfig, error) {
	snap, err := Queues.Snapshot(guildID)
	if err != nil || snap == nil {
		return nil, err
	}
	out := map[string]DecayConfig{}
	for _, q := range snap.Queues {
		if !q.IsEnabled {
			continue
		}
		for _, ch := range q.Settings {
			if !ch.IsEnabled || ch.EloDecay == nil {
				continue
			}
			mode := FindMode(snap.Modes, ch.Mode)
			if mode == nil {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(mode.Name))
			if _, taken := out[key]; taken {
				continue
			}
			cfg := DecayConfig(*ch.EloDecay)
			cfg.DecayFloor = max(0, cfg.DecayFloor)
			out[key] = cfg
		}
	}
	return out, nil
}

// decayFor is the decay that applies to a player whose last game was in mode: that mode's queue's own rules, else the
// guild-wide default.
func (m *EloManager) decayFor(guildID, mode string) DecayConfig {
	if cfg, ok := m.deps.QueueDecay(guildID)[strings.ToLower(strings.TrimSpace(mode))]; ok {
		return cfg
	}
	return m.deps.Decay(guildID)
}

// decayConfigs is every decay rule set a guild can have in play: the default and each queue's own.
func (m *EloManager) decayConfigs(guildID string) []DecayConfig {
	out := []DecayConfig{m.deps.Decay(guildID)}
	for _, cfg := range m.deps.QueueDecay(guildID) {
		out = append(out, cfg)
	}
	return out
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

func (m *EloManager) api() EloAPI {
	if m.deps.API != nil {
		return m.deps.API
	}
	return sdkEloAPI{database.Must()}
}

func (m *EloManager) perks(guildID, userID string) PerkContext {
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
	st, err := m.api().Stats(guildID, userID)
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
	ranked := in.Mode == nil || in.Mode.IsEloGainEnabled
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

	results := make([]EloResult, 0, len(present))
	seen := map[string]bool{}
	score := func(id string, win bool) error {
		if seen[id] {
			return nil
		}
		seen[id] = true
		st, err := m.api().Stats(in.GuildID, id)
		if err != nil {
			return fmt.Errorf("stats for %s: %w", id, err)
		}
		res := EloResult{UserID: id, SeasonID: st.SeasonID, Win: win, MVP: win && id == in.MVP}
		if !ranked {
			res.EloOutcome = EloOutcome{Before: st.Elo, After: st.Elo, RankBefore: st.Rank, RankAfter: st.Rank}
			results = append(results, res)
			return nil
		}
		oi := OutcomeInput{
			Base: st.Elo, Win: win, MVP: res.MVP, Ranks: ranks,
			Perks: m.perks(in.GuildID, id),
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
	for _, r := range results {
		if _, err := m.SyncRank(actor, guildID, r.UserID); err != nil {
			m.logf("rank sync %s/%s: %v", guildID, r.UserID, err)
		}
	}
}

// RevertGame undoes a voided game: each player's recorded change is taken off
// their CURRENT ELO (never restored to the old absolute value, which would
// erase games played since), floored at 0, and a starting grant paid by that
// game is made claimable again. The caller reverts the counters.
// highest_elo is not lowered: the previous peak isn't recorded anywhere.
func (m *EloManager) RevertGame(actor, guildID string, results []EloResult) error {
	var errs []error
	for _, r := range results {
		if r.Change == 0 && r.PerkStart == 0 {
			continue
		}
		if err := m.revertOne(actor, guildID, r); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.UserID, err))
		}
	}
	return errors.Join(errs...)
}

func (m *EloManager) revertOne(actor, guildID string, r EloResult) error {
	defer m.lock(guildID, r.UserID)()
	st, err := m.api().Stats(guildID, r.UserID)
	if err != nil {
		return err
	}
	updated, err := m.api().ApplyElo(actor, guildID, r.UserID, st.SeasonID, sdk.EloEffect{Part: "game", Delta: -r.Change})
	if err != nil {
		return err
	}
	if r.PerkStart > 0 {
		if _, err := m.api().Patch(actor, guildID, r.UserID, st.SeasonID, clearedClaim()); err != nil {
			return err
		}
	}
	_, err = m.syncRank(actor, guildID, r.UserID, st, updated.Elo)
	return err
}

// ============================================================================
// Ranks
// ============================================================================

// SyncRank makes the stored rank name match the player's ELO and returns it.
func (m *EloManager) SyncRank(actor, guildID, userID string) (string, error) {
	st, err := m.api().Stats(guildID, userID)
	if err != nil {
		return "", err
	}
	return m.syncRank(actor, guildID, userID, st, st.Elo)
}

func (m *EloManager) syncRank(actor, guildID, userID string, st *models.PlayerStatsModel, elo int) (string, error) {
	ranks, err := m.deps.Ranks(guildID)
	if err != nil {
		return st.Rank, err
	}
	r := RankFor(ranks, elo)
	if r == nil || r.RankName == "" || r.RankName == st.Rank {
		return st.Rank, nil
	}
	if _, err := m.api().Patch(actor, guildID, userID, st.SeasonID, map[string]any{"rank": r.RankName}); err != nil {
		return st.Rank, err
	}
	m.mu.RLock()
	hook := m.rankSync
	m.mu.RUnlock()
	if hook != nil {
		hook(guildID, userID, st.Rank, r.RankName)
	}
	return r.RankName, nil
}

// ============================================================================
// Manual adjustments (staff /win, /loss, /modify, /wipe)
// ============================================================================

// Adjustment is defined in package types (elo.go).
type Adjustment = types.Adjustment

// Adjust adds delta (negative to remove) to the player's ELO, floored at 0.
func (m *EloManager) Adjust(actor, guildID, userID string, delta int) (*Adjustment, error) {
	defer m.lock(guildID, userID)()
	st, err := m.api().Stats(guildID, userID)
	if err != nil {
		return nil, err
	}
	return m.applyLocked(actor, guildID, userID, st, sdk.EloEffect{Part: "adjustment", Delta: delta, Category: "modify"})
}

// Set sets the player's ELO outright (floored at 0).
func (m *EloManager) Set(actor, guildID, userID string, elo int) (*Adjustment, error) {
	defer m.lock(guildID, userID)()
	st, err := m.api().Stats(guildID, userID)
	if err != nil {
		return nil, err
	}
	target := max(0, elo)
	return m.applyLocked(actor, guildID, userID, st, sdk.EloEffect{Part: "adjustment", SetTo: &target, Category: "modify"})
}

// applyLocked applies one rating effect and brings the stored rank in line. The database applies it under a row lock
// to the part it names, so games, modifications and decay stay separate; After is the resulting derived rating.
func (m *EloManager) applyLocked(actor, guildID, userID string, st *models.PlayerStatsModel, e sdk.EloEffect) (*Adjustment, error) {
	adj := &Adjustment{Before: st.Elo, After: st.Elo, RankBefore: st.Rank, RankAfter: st.Rank}
	if e.Delta != 0 || e.SetTo != nil {
		updated, err := m.api().ApplyElo(actor, guildID, userID, st.SeasonID, e)
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

// PreviewWins simulates n ordinary ranked wins (no MVP, no party, no
// starting grant), re-reading the K-factor after every win because a win can
// move the player into a higher rank.
func (m *EloManager) PreviewWins(guildID, userID string, n int) (*Preview, error) {
	return m.preview(guildID, userID, n, true)
}

// PreviewLosses is PreviewWins for losses.
func (m *EloManager) PreviewLosses(guildID, userID string, n int) (*Preview, error) {
	return m.preview(guildID, userID, n, false)
}

func (m *EloManager) preview(guildID, userID string, n int, win bool) (*Preview, error) {
	st, err := m.api().Stats(guildID, userID)
	if err != nil {
		return nil, err
	}
	return m.simulate(guildID, userID, st.Elo, n, win)
}

func (m *EloManager) simulate(guildID, userID string, start, n int, win bool) (*Preview, error) {
	ranks, err := m.deps.Ranks(guildID)
	if err != nil {
		return nil, err
	}
	pc := m.perks(guildID, userID)
	p := &Preview{Count: min(max(n, 1), maxPreviewCount), Start: start, End: start}
	for i := 0; i < p.Count; i++ {
		o, err := ComputeOutcome(OutcomeInput{Base: p.End, Win: win, Ranks: ranks, Perks: pc})
		if err != nil {
			return nil, err
		}
		if i == 0 {
			p.RankStart = o.RankBefore
		}
		p.Total += abs(o.Change)
		p.End, p.RankEnd = o.After, o.RankAfter
	}
	return p, nil
}

// GrantWins gives a player n wins' worth of ELO (see PreviewWins). The caller
// records the wins counter.
func (m *EloManager) GrantWins(actor, guildID, userID string, n int) (*Preview, error) {
	return m.grant(actor, guildID, userID, n, true)
}

// GrantLosses takes n losses' worth of ELO from a player.
func (m *EloManager) GrantLosses(actor, guildID, userID string, n int) (*Preview, error) {
	return m.grant(actor, guildID, userID, n, false)
}

func (m *EloManager) grant(actor, guildID, userID string, n int, win bool) (*Preview, error) {
	defer m.lock(guildID, userID)()
	st, err := m.api().Stats(guildID, userID)
	if err != nil {
		return nil, err
	}
	p, err := m.simulate(guildID, userID, st.Elo, n, win)
	if err != nil {
		return nil, err
	}
	category := "loss"
	if win {
		category = "win"
	}
	if _, err := m.applyLocked(actor, guildID, userID, st, sdk.EloEffect{Part: "adjustment", Delta: p.End - st.Elo, Category: category, Reason: fmt.Sprintf("%d staff-granted", p.Count)}); err != nil {
		return nil, err
	}
	return p, nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Reset wipes the player's season stats back to SeasonStartElo at the lowest
// rank and re-opens the starting grant.
func (m *EloManager) Reset(actor, guildID, userID string) (*models.PlayerStatsModel, error) {
	defer m.lock(guildID, userID)()
	st, err := m.api().Stats(guildID, userID)
	if err != nil {
		return nil, err
	}
	ranks, err := m.deps.Ranks(guildID)
	if err != nil {
		return nil, err
	}
	name := "Unranked"
	if r := RankFor(ranks, SeasonStartElo); r != nil && r.RankName != "" {
		name = r.RankName
	}
	return m.api().Reset(actor, guildID, userID, st.SeasonID, SeasonStartElo, name)
}

// ============================================================================
// Starting grant (/claimelo)
// ============================================================================

// ClaimStartingElo pays the perk starting grant once, before the player's
// first ranked game of the season (a wipe re-opens it). The claim record
// (amount, time, perks) is saved first and rolled back if paying fails, so a
// crash can lose a claim but never pay it twice.
func (m *EloManager) ClaimStartingElo(actor, guildID, userID string) (*Claim, error) {
	defer m.lock(guildID, userID)()
	st, err := m.api().Stats(guildID, userID)
	if err != nil {
		return nil, err
	}
	if st.StartingEloClaimed || st.GamesPlayed > 0 {
		return nil, ErrStartingGrantUsed
	}
	pc := m.perks(guildID, userID)
	grant := pc.StartingGrant()
	if grant <= 0 {
		return nil, ErrNoStartingGrant
	}
	at := m.deps.Now()
	if _, err := m.api().Patch(actor, guildID, userID, st.SeasonID, claimRecord(grant, pc.Names(), at)); err != nil {
		return nil, err
	}
	adj, err := m.applyLocked(actor, guildID, userID, st, sdk.EloEffect{Part: "game", Delta: grant, Reason: "starting grant"})
	if adj == nil {
		if _, rbErr := m.api().Patch(actor, guildID, userID, st.SeasonID, clearedClaim()); rbErr != nil {
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
			st, err := m.api().Stats(guildID, userID)
			if err == nil {
				_, err = m.api().Patch(actor, guildID, userID, st.SeasonID, clearedClaim())
			}
			if err != nil {
				m.logf("reopen starting grant for %s/%s: %v", guildID, userID, err)
			}
		}()
	}
}

// PerkMultiplier is the win-gain multiplier of the player's best perk (1 = none), for staff /win grants.
func (m *EloManager) PerkMultiplier(guildID, userID string) float64 {
	return m.perks(guildID, userID).Multiplier()
}

// RecordStartingGrants stores the claim record for players whose first ranked
// game paid them the starting grant. Call it after CompleteGame; failures are
// logged, not fatal, since the ELO itself is already saved.
func (m *EloManager) RecordStartingGrants(actor, guildID string, results []EloResult) {
	for _, r := range results {
		if r.PerkStart <= 0 {
			continue
		}
		rec := claimRecord(r.PerkStart, m.perks(guildID, r.UserID).Names(), m.deps.Now())
		if _, err := m.api().Patch(actor, guildID, r.UserID, r.SeasonID, rec); err != nil {
			m.logf("record starting grant for %s/%s: %v", guildID, r.UserID, err)
		}
	}
}

// ============================================================================
// Decay
// ============================================================================

// DecayResult is defined in package types (elo.go).
type DecayResult = types.DecayResult

// AddDecay is a staff-applied decay: it removes up to amount ELO, stopping at
// the decay floor, and records it like an automatic one.
func (m *EloManager) AddDecay(actor, guildID, userID string, amount int, reason string) (*DecayResult, error) {
	defer m.lock(guildID, userID)()
	st, err := m.api().Stats(guildID, userID)
	if err != nil {
		return nil, err
	}
	floor := m.decayFor(guildID, st.LastPlayedMode).DecayFloor
	applied := min(amount, st.Elo-floor)
	if applied <= 0 {
		return nil, ErrAtDecayFloor
	}
	return m.applyDecay(actor, guildID, userID, st, applied, reason, m.daysInactive(st), false)
}

// RemoveDecay deletes a decay record and gives back the ELO it took (unless
// it was already voided). The decay must be from the player's current season.
func (m *EloManager) RemoveDecay(actor, guildID string, decayID uint) (*Adjustment, error) {
	d, err := m.api().GetDecay(guildID, decayID)
	if err != nil {
		return nil, err
	}
	userID, err := m.api().Snowflake(guildID, d.PlayerID)
	if err != nil {
		return nil, err
	}
	defer m.lock(guildID, userID)()
	st, err := m.api().Stats(guildID, userID)
	if err != nil {
		return nil, err
	}
	if st.SeasonID != d.SeasonID {
		return nil, ErrDecayNotCurrent
	}
	if err := m.api().DeleteDecay(actor, guildID, decayID); err != nil {
		return nil, err
	}
	if d.IsVoided {
		return &Adjustment{Before: st.Elo, After: st.Elo, RankBefore: st.Rank, RankAfter: st.Rank}, nil
	}
	return m.applyLocked(actor, guildID, userID, st, sdk.EloEffect{Part: "decay", Delta: -d.AmountDeducted})
}

func (m *EloManager) daysInactive(st *models.PlayerStatsModel) int {
	if st.LastPlayedAt == nil {
		return 0
	}
	return max(0, int(m.deps.Now().Sub(*st.LastPlayedAt).Hours()/24))
}

// applyDecay records the decay first and rolls the record back if the ELO
// write fails, so history never shows a decay that didn't happen.
func (m *EloManager) applyDecay(actor, guildID, userID string, st *models.PlayerStatsModel, amount int, reason string, days int, auto bool) (*DecayResult, error) {
	if reason == "" {
		reason = "Inactivity Decay"
	}
	after := max(0, st.Elo-amount)
	id, err := m.api().CreateDecay(actor, guildID, userID, models.PlayerDecaysModel{
		SeasonID: st.SeasonID, AmountDeducted: amount, EloBefore: st.Elo, EloAfter: after, DaysInactive: days, Reason: reason,
	})
	if err != nil {
		return nil, err
	}
	if _, err := m.api().ApplyElo(actor, guildID, userID, st.SeasonID, sdk.EloEffect{Part: "decay", Delta: amount}); err != nil {
		if delErr := m.api().DeleteDecay(actor, guildID, id); delErr != nil {
			m.logf("decay %d rollback failed: %v", id, delErr)
		}
		return nil, err
	}
	// Only automatic decay starts/continues the decay streak; a staff decay on an active player must not make
	// NextDecayAt schedule an automatic tick 24h later.
	stamps := map[string]any{"elo_decay_warning_for": nil}
	if auto {
		stamps["last_elo_decay_at"] = m.deps.Now().UTC()
	}
	if _, err := m.api().Patch(actor, guildID, userID, st.SeasonID, stamps); err != nil {
		m.logf("decay timestamps for %s/%s: %v", guildID, userID, err)
	}
	if _, err := m.syncRank(actor, guildID, userID, st, after); err != nil {
		m.logf("decay rank sync %s/%s: %v", guildID, userID, err)
	}
	return &DecayResult{ID: id, Amount: amount, Before: st.Elo, After: after}, nil
}

// SweepDecay warns players whose decay is due within 12h (once per deadline)
// and applies decay that is due, at most one tick per player per 24h. Players
// who never played this season, sit at the floor, hold a rank with no decay
// amount, or are mid-game are skipped. It returns how many players decayed.
func (m *EloManager) SweepDecay(guildID string) int {
	// Ask for fewer days than the smallest setting so rows inside a warning window come back too.
	candidateDays, any := 0, false
	for _, cfg := range m.decayConfigs(guildID) {
		if !cfg.Enabled || cfg.InactivityDays <= 0 {
			continue
		}
		days := max(0, cfg.InactivityDays-int(math.Ceil(cfg.warningWindow().Hours()/24)))
		if !any || days < candidateDays {
			candidateDays, any = days, true
		}
	}
	if !any {
		return 0
	}
	ranks, err := m.deps.Ranks(guildID)
	if err != nil || len(ranks) == 0 {
		return 0
	}
	rows, err := m.api().DecayCandidates(guildID, candidateDays)
	if err != nil {
		m.logf("decay candidates for %s: %v", guildID, err)
		return 0
	}
	now := m.deps.Now()
	applied := 0
	for _, row := range rows {
		cfg := m.decayFor(guildID, row.LastPlayedMode)
		if !cfg.Enabled || cfg.InactivityDays <= 0 || row.LastPlayedAt == nil || row.Elo <= cfg.DecayFloor {
			continue
		}
		next := NextDecayAt(*row.LastPlayedAt, row.LastEloDecayAt, cfg.InactivityDays, cfg.interval())
		due := !now.Before(next)
		if !due && now.Before(next.Add(-cfg.warningWindow())) {
			continue
		}
		amount := min(RankFor(ranks, row.Elo).Decay, row.Elo-cfg.DecayFloor)
		if amount <= 0 {
			continue
		}
		userID, err := m.api().Snowflake(guildID, row.PlayerID)
		if err != nil || m.deps.InGame(guildID, userID) {
			continue
		}
		if !due {
			if cfg.wantsWarning() {
				m.warnDecay(guildID, userID, row, next, amount, cfg)
			}
			continue
		}
		if m.autoDecay(guildID, userID, cfg, ranks) {
			applied++
		}
	}
	return applied
}

func (m *EloManager) warnDecay(guildID, userID string, row models.PlayerStatsModel, due time.Time, amount int, cfg DecayConfig) {
	if row.EloDecayWarningFor != nil && row.EloDecayWarningFor.Unix() == due.Unix() {
		return
	}
	m.mu.RLock()
	notify := m.notify
	m.mu.RUnlock()
	if notify == nil || !notify(DecayNotice{Kind: DecayWarning, GuildID: guildID, UserID: userID, Amount: amount, Before: row.Elo, After: row.Elo - amount, InactivityDays: cfg.InactivityDays, At: due, Config: cfg}) {
		return
	}
	if _, err := m.api().Patch(actorSystem, guildID, userID, row.SeasonID, map[string]any{"elo_decay_warning_for": due.UTC()}); err != nil {
		m.logf("persist decay warning %s/%s: %v", guildID, userID, err)
	}
}

// autoDecay re-reads the player under their lock before deducting, so a game
// that finished since the candidate list was fetched cancels the tick.
func (m *EloManager) autoDecay(guildID, userID string, cfg DecayConfig, ranks []types.Rank) bool {
	defer m.lock(guildID, userID)()
	st, err := m.api().Stats(guildID, userID)
	if err != nil || st.LastPlayedAt == nil || st.Elo <= cfg.DecayFloor {
		return false
	}
	now := m.deps.Now()
	if now.Before(NextDecayAt(*st.LastPlayedAt, st.LastEloDecayAt, cfg.InactivityDays, cfg.interval())) {
		return false
	}
	if st.LastEloDecayAt != nil && now.Sub(*st.LastEloDecayAt) < cfg.interval() {
		return false
	}
	amount := min(RankFor(ranks, st.Elo).Decay, st.Elo-cfg.DecayFloor)
	if amount <= 0 {
		return false
	}
	res, err := m.applyDecay(actorSystem, guildID, userID, st, amount, "", m.daysInactive(st), true)
	if err != nil {
		m.logf("decay %s/%s: %v", guildID, userID, err)
		return false
	}
	m.mu.RLock()
	notify := m.notify
	m.mu.RUnlock()
	if notify != nil && cfg.wantsApplied() {
		notify(DecayNotice{Kind: DecayApplied, GuildID: guildID, UserID: userID, Amount: res.Amount, Before: res.Before, After: res.After, InactivityDays: cfg.InactivityDays, Config: cfg})
	}
	return true
}

// DecayPending reports whether any player of the guild could still decay: decay is on for some rule set and a
// player who has played sits above that rule's floor. An error counts as yes, so a hiccup never drops a guild.
func (m *EloManager) DecayPending(guildID string) bool {
	enabled := false
	for _, cfg := range m.decayConfigs(guildID) {
		if cfg.Enabled && cfg.InactivityDays > 0 {
			enabled = true
		}
	}
	if !enabled {
		return false
	}
	rows, err := m.api().DecayCandidates(guildID, 0)
	if err != nil {
		return true
	}
	for _, row := range rows {
		if cfg := m.decayFor(guildID, row.LastPlayedMode); cfg.Enabled && cfg.InactivityDays > 0 && row.Elo > cfg.DecayFloor {
			return true
		}
	}
	return false
}
