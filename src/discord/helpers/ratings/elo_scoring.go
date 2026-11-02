package ratings

import (
	"context"
	"math"
	"ranked-bedwars/src/types"
	"sort"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

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

func (a sdkEloAPI) StatsInSeason(g, u string, season uint) (*models.PlayerStatsModel, error) {
	return a.s.Stats.GetOrCreate(g, u, season)
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
