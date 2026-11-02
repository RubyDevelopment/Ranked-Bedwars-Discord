package match

import (
	"errors"
	"math/rand/v2"
	"ranked-bedwars/src/discord/helpers/partymgr"
	"ranked-bedwars/src/types"
	"slices"
	"sort"
	"strings"
)

// ============================================================================

// Team formation for a match: automatic assignment (random / ELO-balanced,
// party-aware) and the captain draft. Everything here is pure: players are
// Discord user IDs, ratings come in as a map, randomness comes from a
// *rand.Rand so tests can pin it. The old bot shuffled with
// sort(() => random - 0.5), which is a biased shuffle; this uses real
// Fisher-Yates via rand.Shuffle, and a textbook snake instead of the old
// direction-flipping loop.

var (
	ErrBadRoster    = errors.New("roster does not fit the team layout")
	ErrNotYourTurn  = errors.New("it is not that captain's turn")
	ErrNotInPool    = errors.New("player is not available to pick")
	ErrDraftOver    = errors.New("the draft is complete")
	ErrNoDraftYet   = errors.New("no draft is running")
	ErrNotACaptain  = errors.New("not a captain")
	ErrDraftTooFew  = errors.New("not enough players for a draft")
	teamLetterStrip = strings.NewReplacer("team", "", " ", "", "-", "", "_", "")
)

// TeamName is the display name of team i: "Team A", "Team B", ...
func TeamName(i int) string { return "Team " + string(rune('A'+i)) }

// TeamIndex resolves user input ("A", "team b", "Team-C") to a team index in
// [0, teamCount), or -1.
func TeamIndex(input string, teamCount int) int {
	s := teamLetterStrip.Replace(strings.ToLower(strings.TrimSpace(input)))
	if len(s) != 1 || s[0] < 'a' {
		return -1
	}
	if i := int(s[0] - 'a'); i < teamCount {
		return i
	}
	return -1
}

func rngOr(r *rand.Rand) *rand.Rand {
	if r != nil {
		return r
	}
	return rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
}

// ============================================================================
// Automatic assignment
// ============================================================================

// TeamOptions is defined in package types (games.go).
type TeamOptions = types.TeamOptions

// AssignTeams splits players into o.Teams teams of o.PerTeam. Parties that fit
// stay together (each goes on the team with the lowest total rating that still
// has room); solos then fill the rest, ELO-balanced with probability
// 1 - randomness, otherwise randomly. Without parties it snake-drafts a
// noise-perturbed rating order (randomness 100 = a plain shuffle).
func AssignTeams(players []string, o TeamOptions) ([][]string, error) {
	if o.Teams < 2 || o.PerTeam < 1 || len(players) != o.Teams*o.PerTeam {
		return nil, ErrBadRoster
	}
	r := rngOr(o.Rand)
	rnd := float64(min(max(o.Randomness, 0), 100)) / 100
	rating := func(id string) int { return o.Ratings[id] }

	inRoster := make(map[string]bool, len(players))
	for _, id := range players {
		inRoster[id] = true
	}
	inGroup := map[string]bool{}
	var groups [][]string
	for _, pg := range o.Parties {
		var g []string
		for _, id := range pg.Members {
			if inRoster[id] && !inGroup[id] {
				g = append(g, id)
			}
		}
		if len(g) >= 2 {
			for _, id := range g {
				inGroup[id] = true
			}
			groups = append(groups, g)
		}
	}

	teams := make([][]string, o.Teams)
	if len(groups) == 0 {
		ordered := orderByNoise(players, rating, rnd, r)
		if rnd >= 1 {
			for i := range teams {
				teams[i] = slices.Clone(ordered[i*o.PerTeam : (i+1)*o.PerTeam])
			}
			return teams, nil
		}
		for i, id := range ordered { // snake: A B C C B A A B C ...
			round, pos := i/o.Teams, i%o.Teams
			t := pos
			if round%2 == 1 {
				t = o.Teams - 1 - pos
			}
			teams[t] = append(teams[t], id)
		}
		return teams, nil
	}

	teamElo := func(t int) int {
		sum := 0
		for _, id := range teams[t] {
			sum += rating(id)
		}
		return sum
	}
	// lowest picks the team with room for n that has the lowest total rating
	// (ties: fewer players, then earlier), or -1.
	lowest := func(n int) int {
		best := -1
		for t := range teams {
			if o.PerTeam-len(teams[t]) < n {
				continue
			}
			if best < 0 || teamElo(t) < teamElo(best) || (teamElo(t) == teamElo(best) && len(teams[t]) < len(teams[best])) {
				best = t
			}
		}
		return best
	}

	groupElo := func(g []string) float64 {
		elos := make([]int, len(g))
		for i, id := range g {
			elos[i] = rating(id)
		}
		return partymgr.AggregateElo(elos, o.Method, elos[0])
	}
	if o.BalanceParties {
		sort.SliceStable(groups, func(i, j int) bool { return groupElo(groups[i]) > groupElo(groups[j]) })
	} else {
		r.Shuffle(len(groups), func(i, j int) { groups[i], groups[j] = groups[j], groups[i] })
	}

	var solos []string
	for _, id := range players {
		if !inGroup[id] {
			solos = append(solos, id)
		}
	}
	for _, g := range groups {
		if t := lowest(len(g)); t >= 0 {
			teams[t] = append(teams[t], g...)
			continue
		}
		if !o.AllowSplit { // can't stay together: they fill in as solos
			solos = append(solos, g...)
			continue
		}
		for _, id := range g {
			if t := lowest(1); t >= 0 {
				teams[t] = append(teams[t], id)
			}
		}
	}

	if r.Float64() >= rnd { // balanced fill
		slices.SortStableFunc(solos, func(a, b string) int { return rating(b) - rating(a) })
		for _, id := range solos {
			if t := lowest(1); t >= 0 {
				teams[t] = append(teams[t], id)
			}
		}
	} else {
		r.Shuffle(len(solos), func(i, j int) { solos[i], solos[j] = solos[j], solos[i] })
		for _, id := range solos {
			var open []int
			for t := range teams {
				if len(teams[t]) < o.PerTeam {
					open = append(open, t)
				}
			}
			if len(open) > 0 {
				t := open[r.IntN(len(open))]
				teams[t] = append(teams[t], id)
			}
		}
	}
	return teams, nil
}

// orderByNoise sorts players by rating plus noise scaled by rnd (0 = exact
// rating order, 1 = a uniform shuffle).
func orderByNoise(players []string, rating func(string) int, rnd float64, r *rand.Rand) []string {
	out := slices.Clone(players)
	if rnd >= 1 {
		r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return out
	}
	score := make(map[string]float64, len(out))
	for _, id := range out {
		score[id] = float64(rating(id)) + (r.Float64()-0.5)*rnd*400
	}
	sort.SliceStable(out, func(i, j int) bool {
		if score[out[i]] != score[out[j]] {
			return score[out[i]] > score[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// ============================================================================
// Captain draft
