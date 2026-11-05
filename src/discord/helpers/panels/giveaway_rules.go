package panels

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
)

// ============================================================================
// Giveaway rules made for ranked Bedwars
// ============================================================================

// A giveaway can ask more than a minimum ELO of whoever enters: a rank range, games and wins this season, roles they must or must not
// hold, a clean record, and whether earlier winners may win again. It can also give extra entries (a better chance) by rank or role, and
// hand the winners a role. Every rule is optional; a giveaway with none of them is open to everyone, as before.

// entrant is what the rules look at: the member who presses Enter.
type entrant struct {
	Registered bool
	Elo        int
	Games      int
	Wins       int
	Rank       string // their rank's name this season ("" when unranked)
	Roles      []string
	// Punished says they have an active punishment or strike.
	Punished bool
	// PastWinner says they won an earlier giveaway of this server.
	PastWinner bool
}

// needsStats says whether checking this giveaway means reading the member's stats this season.
func (g giveawayDoc) needsStats() bool {
	return g.RequiredElo > 0 || g.MaxElo > 0 || g.MinRank != "" || g.MaxRank != "" || g.MinGames > 0 || g.MinWins > 0 || len(g.BonusRanks) > 0
}

// rankIndex is a rank's place in the server's rank list, lowest first (-1 when the name is not in it).
func rankIndex(order []string, name string) int {
	for i, n := range order {
		if strings.EqualFold(n, name) {
			return i
		}
	}
	return -1
}

// check says why this member may not enter ("" when they may). order is the server's rank names, lowest first.
func (g giveawayDoc) check(e entrant, order []string) string {
	for _, id := range g.BlockedRoleIDs {
		if slices.Contains(e.Roles, id) {
			return fmt.Sprintf("Members with <@&%s> cannot enter this giveaway.", id)
		}
	}
	if len(g.RequiredRoleIDs) > 0 && !slices.ContainsFunc(g.RequiredRoleIDs, func(id string) bool { return slices.Contains(e.Roles, id) }) {
		return "You need " + roleList(g.RequiredRoleIDs, "or") + " to enter this giveaway."
	}
	if g.needsStats() && !e.Registered {
		return "You must be registered to enter this giveaway. Register with `/register`."
	}
	if g.RequiredElo > 0 && e.Elo < g.RequiredElo {
		return fmt.Sprintf("You need at least **%d ELO** to enter this giveaway. You have **%d**.", g.RequiredElo, e.Elo)
	}
	if g.MaxElo > 0 && e.Elo > g.MaxElo {
		return fmt.Sprintf("This giveaway is for players up to **%d ELO**. You have **%d**.", g.MaxElo, e.Elo)
	}
	if g.MinRank != "" {
		if want, have := rankIndex(order, g.MinRank), rankIndex(order, e.Rank); want >= 0 && have < want {
			return fmt.Sprintf("You need to be **%s** or higher to enter this giveaway.", g.MinRank)
		}
	}
	if g.MaxRank != "" {
		if limit, have := rankIndex(order, g.MaxRank), rankIndex(order, e.Rank); limit >= 0 && have > limit {
			return fmt.Sprintf("This giveaway is for **%s** and below.", g.MaxRank)
		}
	}
	if g.MinGames > 0 && e.Games < g.MinGames {
		return fmt.Sprintf("You need at least **%d games** this season to enter. You have **%d**.", g.MinGames, e.Games)
	}
	if g.MinWins > 0 && e.Wins < g.MinWins {
		return fmt.Sprintf("You need at least **%d wins** this season to enter. You have **%d**.", g.MinWins, e.Wins)
	}
	if g.NoActivePunishment && e.Punished {
		return "Players with an active punishment or strike cannot enter this giveaway."
	}
	if g.ExcludePastWinners && e.PastWinner {
		return "You won an earlier giveaway here, so you cannot enter this one."
	}
	return ""
}

// maxWeight caps the entries one member can hold, however many bonuses they qualify for.
const maxWeight = 50

// weight is how many entries this member holds: one, plus the bonus for their rank and for each bonus role they hold.
func (g giveawayDoc) weight(e entrant) int {
	w := 1
	for name, extra := range g.BonusRanks {
		if strings.EqualFold(name, e.Rank) {
			w += max(extra, 0)
		}
	}
	for id, extra := range g.BonusRoles {
		if slices.Contains(e.Roles, id) {
			w += max(extra, 0)
		}
	}
	return min(w, maxWeight)
}

// pickWinners draws up to n different entries, each with the chance its weight gives it (an entry without a recorded weight counts once).
func pickWinners(entries []string, weights map[string]int, n int, rng *rand.Rand) []string {
	pool := slices.Clone(entries)
	var winners []string
	for len(winners) < n && len(pool) > 0 {
		total := 0
		for _, id := range pool {
			total += max(weights[id], 1)
		}
		roll := rng.Intn(total)
		for idx, id := range pool {
			roll -= max(weights[id], 1)
			if roll < 0 {
				winners = append(winners, id)
				pool = slices.Delete(pool, idx, idx+1)
				break
			}
		}
	}
	return winners
}

// rulesText lists a giveaway's rules for its post, one per line.
func (g giveawayDoc) rulesText() string {
	var lines []string
	switch {
	case g.RequiredElo > 0 && g.MaxElo > 0:
		lines = append(lines, fmt.Sprintf("ELO %d to %d", g.RequiredElo, g.MaxElo))
	case g.RequiredElo > 0:
		lines = append(lines, fmt.Sprintf("At least %d ELO", g.RequiredElo))
	case g.MaxElo > 0:
		lines = append(lines, fmt.Sprintf("At most %d ELO", g.MaxElo))
	}
	switch {
	case g.MinRank != "" && g.MaxRank != "":
		lines = append(lines, fmt.Sprintf("Rank %s to %s", g.MinRank, g.MaxRank))
	case g.MinRank != "":
		lines = append(lines, fmt.Sprintf("Rank %s or higher", g.MinRank))
	case g.MaxRank != "":
		lines = append(lines, fmt.Sprintf("Rank %s or lower", g.MaxRank))
	}
	if g.MinGames > 0 {
		lines = append(lines, fmt.Sprintf("%d+ games this season", g.MinGames))
	}
	if g.MinWins > 0 {
		lines = append(lines, fmt.Sprintf("%d+ wins this season", g.MinWins))
	}
	if len(g.RequiredRoleIDs) > 0 {
		lines = append(lines, "Needs "+roleList(g.RequiredRoleIDs, "or"))
	}
	if len(g.BlockedRoleIDs) > 0 {
		lines = append(lines, "Not for "+roleList(g.BlockedRoleIDs, "or"))
	}
	if g.NoActivePunishment {
		lines = append(lines, "No active punishment or strike")
	}
	if g.ExcludePastWinners {
		lines = append(lines, "Earlier winners cannot enter")
	}
	if len(g.BonusRanks)+len(g.BonusRoles) > 0 {
		lines = append(lines, "Higher ranks and some roles get extra entries")
	}
	return strings.Join(lines, "\n")
}

func roleList(ids []string, joiner string) string {
	mentions := make([]string, len(ids))
	for i, id := range ids {
		mentions[i] = "<@&" + id + ">"
	}
	if len(mentions) <= 1 {
		return strings.Join(mentions, "")
	}
	return strings.Join(mentions[:len(mentions)-1], ", ") + " " + joiner + " " + mentions[len(mentions)-1]
}
