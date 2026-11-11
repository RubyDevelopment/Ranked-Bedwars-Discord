package ratings

import (
	"fmt"
	"math"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers/guildlink"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/types"
	baseutility "ranked-bedwars/src/utility"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"
)

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
var decayWatch = base.NewGuildWatch("EloDecay", decaySweepEvery, func(guildID string) bool {
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
			if guildlink.IsSecondary(g.ID) {
				continue // a linked secondary server has no data of its own
			}
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
	if _, err := helpers.SyncRegisteredPlayerProfile(s, guildID, view.Player.ID, userID); err != nil {
		decayLogger.Warn(fmt.Sprintf("profile sync for %s in guild %s: %v", userID, guildID, err))
	}
}

// RefreshRankRoles re-syncs the player's rank role and nickname with their stored rank, for changes (a wipe, an unwipe)
// that write the rank themselves and so never trigger the sync that follows an ordinary rank change.
func RefreshRankRoles(guildID, userID string) { syncRankRoles(guildID, userID, "", "") }

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

	helpers.WithGuildFooterIconForGuild(s, n.GuildID, embed)
	via, channelID := n.Config.Via(), n.Config.AlertChannelID
	if channelID == "" { // the queue sets none: the shared decay alerts channel (Events page, or any command that declares it)
		channelID = helpers.EventRequirementID(n.GuildID, helpers.DecaySweeperEventName, types.ReqChannel, types.DecayAlertsKey)
	}
	if channelID == "" { // assigned on the /decays command instead
		channelID, _ = helpers.RequirementSlotID(n.GuildID, "decays", types.ReqChannel, types.DecayAlertsKey)
	}
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

// DecayResult is defined in package types (elo.go).
type DecayResult = types.DecayResult

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
	id, err := m.API().CreateDecay(actor, guildID, userID, models.PlayerDecaysModel{
		SeasonID: st.SeasonID, AmountDeducted: amount, EloBefore: st.Elo, EloAfter: after, DaysInactive: days, Reason: reason,
	})
	if err != nil {
		return nil, err
	}
	if _, err := m.API().ApplyElo(actor, guildID, userID, st.SeasonID, sdk.EloEffect{Part: "decay", Delta: amount}); err != nil {
		if delErr := m.API().DeleteDecay(actor, guildID, id); delErr != nil {
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
	if _, err := m.API().Patch(actor, guildID, userID, st.SeasonID, stamps); err != nil {
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
	cfg := m.deps.Decay(guildID)
	if !cfg.Enabled || cfg.InactivityDays <= 0 {
		return 0
	}
	// Ask for fewer days than the setting so rows inside a warning window come back too.
	candidateDays := max(0, cfg.InactivityDays-int(math.Ceil(cfg.warningWindow().Hours()/24)))
	ranks, err := m.deps.Ranks(guildID)
	if err != nil || len(ranks) == 0 {
		return 0
	}
	rows, err := m.API().DecayCandidates(guildID, candidateDays)
	if err != nil {
		m.logf("decay candidates for %s: %v", guildID, err)
		return 0
	}
	now := m.deps.Now()
	applied := 0
	for _, row := range rows {
		if row.LastPlayedAt == nil || row.Elo <= cfg.DecayFloor {
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
		userID, err := m.API().Snowflake(guildID, row.PlayerID)
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
	if _, err := m.API().Patch(sdk.SystemActor, guildID, userID, row.SeasonID, map[string]any{"elo_decay_warning_for": due.UTC()}); err != nil {
		m.logf("persist decay warning %s/%s: %v", guildID, userID, err)
	}
}

// autoDecay re-reads the player under their lock before deducting, so a game
// that finished since the candidate list was fetched cancels the tick.
func (m *EloManager) autoDecay(guildID, userID string, cfg DecayConfig, ranks []types.Rank) bool {
	defer m.lock(guildID, userID)()
	if m.deps.InGame(guildID, userID) { // a game started or ended since the sweep looked: its result is not in the stats yet
		return false
	}
	st, err := m.API().Stats(guildID, userID)
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
	res, err := m.applyDecay(sdk.SystemActor, guildID, userID, st, amount, "", m.daysInactive(st), true)
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

// DecayPending reports whether any player of the guild could still decay: decay is on and a player who has played sits
// above the floor. An error counts as yes, so a hiccup never drops a guild.
func (m *EloManager) DecayPending(guildID string) bool {
	cfg := m.deps.Decay(guildID)
	if !cfg.Enabled || cfg.InactivityDays <= 0 {
		return false
	}
	rows, err := m.API().DecayCandidates(guildID, 0)
	if err != nil {
		return true
	}
	for _, row := range rows {
		if row.Elo > cfg.DecayFloor {
			return true
		}
	}
	return false
}

// A strike step can take ELO (see PlanStrike). Like decay, it is its own part of the rating: games and modifications
// are never rewritten, and removing the strike gives back exactly what it took.

// StrikePenalty takes up to amount ELO off the player as a strike penalty (the database never takes the rating below
// 0) and returns the result; Before-After is what was actually taken.
func (m *EloManager) StrikePenalty(actor, guildID, userID string, amount int) (*Adjustment, error) {
	defer m.lock(guildID, userID)()
	st, err := m.API().Stats(guildID, userID)
	if err != nil {
		return nil, err
	}
	return m.applyLocked(actor, guildID, userID, st, sdk.EloEffect{Part: "strike", Delta: max(0, amount), Reason: "strike"})
}

// RefundStrike gives back the ELO a removed strike took, if it was taken from the player's current season.
func (m *EloManager) RefundStrike(actor, guildID, userID string, strike *models.PlayerStrikesModel) error {
	taken := -strike.EloChange
	if taken <= 0 {
		return nil
	}
	defer m.lock(guildID, userID)()
	st, err := m.API().Stats(guildID, userID)
	if err != nil || st.SeasonID != strike.SeasonID {
		return err
	}
	_, err = m.applyLocked(actor, guildID, userID, st, sdk.EloEffect{Part: "strike", Delta: -taken, Reason: "strike removed"})
	return err
}
