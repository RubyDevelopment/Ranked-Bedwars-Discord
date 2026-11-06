package panels

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
)

// ============================================================================
// Giveaway rules
// ============================================================================

// A giveaway can ask more than a role of whoever enters. Any server may use the general rules (roles they must or must not hold, how long they
// have been in the server, boosters only); a ranked Bedwars server can add the ladder's own: ELO, rank, games, wins, recent play, a clean
// record, and whether earlier winners may win again. It can also give extra entries (a better chance) by rank or role, and hand the winners a
// role. Every rule is optional; a giveaway with none of them is open to everyone.

// entrant is what the rules look at: the member who presses Enter (or, at the draw, who has been picked).
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
	// JoinedDays is how long they have been in the server, in days (-1 when it is not known).
	JoinedDays int
	// Booster says they boost the server.
	Booster bool
	// IdleDays is how many days ago they last played a ranked game (-1 when never).
	IdleDays int
}

// asksStats says whether a rule needs the member's ladder record: a limit on ELO, rank, games, wins or recent play.
func (g giveawayDoc) asksStats() bool {
	return g.RequiredElo > 0 || g.MaxElo > 0 || g.MinRank != "" || g.MaxRank != "" || g.MinGames > 0 || g.MinWins > 0 || g.ActiveDays > 0
}

// needsStats says whether checking this giveaway means reading the member's stats this season (a rank bonus needs them too, though it never refuses anyone).
func (g giveawayDoc) needsStats() bool { return g.asksStats() || len(g.BonusRanks) > 0 }

// needsRegistration says whether the giveaway is for registered ranked players: it asks for it, or asks for something only they have.
func (g giveawayDoc) needsRegistration() bool { return g.RequireRegistered || g.asksStats() }

// cmd writes a command for a message: a clickable mention when the bot knows its id, else its name.
func (g giveawayDoc) cmd(name string) string {
	if g.ref != nil {
		return g.ref(name)
	}
	return "`/" + name + "`"
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

// check says why this member may not enter ("" when they may). order is the server's rank names, lowest first. Each refusal says what to do
// about it, naming the command that helps.
func (g giveawayDoc) check(e entrant, order []string) string {
	for _, id := range g.BlockedRoleIDs {
		if slices.Contains(e.Roles, id) {
			return fmt.Sprintf("Members with <@&%s> cannot enter this giveaway.", id)
		}
	}
	if len(g.RequiredRoleIDs) > 0 && !slices.ContainsFunc(g.RequiredRoleIDs, func(id string) bool { return slices.Contains(e.Roles, id) }) {
		return "You need " + roleList(g.RequiredRoleIDs, "or") + " to enter this giveaway."
	}
	if g.BoostersOnly && !e.Booster {
		return "This giveaway is for server boosters."
	}
	if g.MinJoinDays > 0 && e.JoinedDays >= 0 && e.JoinedDays < g.MinJoinDays {
		return fmt.Sprintf("You need to have been in this server for **%d days** to enter. You have been here **%d**.", g.MinJoinDays, e.JoinedDays)
	}
	if g.needsRegistration() && !e.Registered {
		return "You must be registered to enter this giveaway. Register with " + g.cmd("register") + "."
	}
	if g.RequiredElo > 0 && e.Elo < g.RequiredElo {
		return fmt.Sprintf("You need at least **%d ELO** to enter this giveaway. You have **%d** (see %s).", g.RequiredElo, e.Elo, g.cmd("stats"))
	}
	if g.MaxElo > 0 && e.Elo > g.MaxElo {
		return fmt.Sprintf("This giveaway is for players up to **%d ELO**. You have **%d** (see %s).", g.MaxElo, e.Elo, g.cmd("stats"))
	}
	if g.MinRank != "" {
		if want, have := rankIndex(order, g.MinRank), rankIndex(order, e.Rank); want >= 0 && have < want {
			return fmt.Sprintf("You need to be **%s** or higher to enter this giveaway (the ranks are on %s).", g.MinRank, g.cmd("ranks"))
		}
	}
	if g.MaxRank != "" {
		if limit, have := rankIndex(order, g.MaxRank), rankIndex(order, e.Rank); limit >= 0 && have > limit {
			return fmt.Sprintf("This giveaway is for **%s** and below.", g.MaxRank)
		}
	}
	if g.MinGames > 0 && e.Games < g.MinGames {
		return fmt.Sprintf("You need at least **%d games** this season to enter. You have **%d**. Join a queue from %s to play.", g.MinGames, e.Games, g.cmd("queues"))
	}
	if g.MinWins > 0 && e.Wins < g.MinWins {
		return fmt.Sprintf("You need at least **%d wins** this season to enter. You have **%d** (see %s).", g.MinWins, e.Wins, g.cmd("stats"))
	}
	if g.ActiveDays > 0 && (e.IdleDays < 0 || e.IdleDays > g.ActiveDays) {
		return fmt.Sprintf("You need to have played a ranked game in the last **%d days** to enter. Join a queue from %s.", g.ActiveDays, g.cmd("queues"))
	}
	if g.NoActivePunishment && e.Punished {
		return fmt.Sprintf("Players with an active punishment or strike cannot enter this giveaway (see %s and %s).", g.cmd("punishments"), g.cmd("strikes"))
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
	return pickWinnersChecked(entries, weights, n, rng, nil)
}

// pickWinnersChecked is pickWinners where each person drawn is first checked with valid: one who no longer qualifies (left the server, got a
// punishment since entering) is passed over and the draw goes on, so the winners are people who still meet the rules at the draw.
func pickWinnersChecked(entries []string, weights map[string]int, n int, rng *rand.Rand, valid func(userID string) bool) []string {
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
				if valid == nil || valid(id) {
					winners = append(winners, id)
				}
				pool = slices.Delete(pool, idx, idx+1)
				break
			}
		}
	}
	return winners
}

// requirements lists a giveaway's rules for its post, one per line, each saying where to look or what to do (ref writes a command).
func (g giveawayDoc) requirements() []string {
	var lines []string
	if g.needsRegistration() {
		lines = append(lines, "Registered ranked player: "+g.cmd("register"))
	}
	switch {
	case g.RequiredElo > 0 && g.MaxElo > 0:
		lines = append(lines, fmt.Sprintf("ELO %d to %d (%s)", g.RequiredElo, g.MaxElo, g.cmd("stats")))
	case g.RequiredElo > 0:
		lines = append(lines, fmt.Sprintf("At least %d ELO (%s)", g.RequiredElo, g.cmd("stats")))
	case g.MaxElo > 0:
		lines = append(lines, fmt.Sprintf("At most %d ELO (%s)", g.MaxElo, g.cmd("stats")))
	}
	switch {
	case g.MinRank != "" && g.MaxRank != "":
		lines = append(lines, fmt.Sprintf("Rank %s to %s (%s)", g.MinRank, g.MaxRank, g.cmd("ranks")))
	case g.MinRank != "":
		lines = append(lines, fmt.Sprintf("Rank %s or higher (%s)", g.MinRank, g.cmd("ranks")))
	case g.MaxRank != "":
		lines = append(lines, fmt.Sprintf("Rank %s or lower (%s)", g.MaxRank, g.cmd("ranks")))
	}
	if g.MinGames > 0 {
		lines = append(lines, fmt.Sprintf("%d+ games this season (%s)", g.MinGames, g.cmd("queues")))
	}
	if g.MinWins > 0 {
		lines = append(lines, fmt.Sprintf("%d+ wins this season (%s)", g.MinWins, g.cmd("stats")))
	}
	if g.ActiveDays > 0 {
		lines = append(lines, fmt.Sprintf("Played a ranked game in the last %d days", g.ActiveDays))
	}
	if len(g.RequiredRoleIDs) > 0 {
		lines = append(lines, "Needs "+roleList(g.RequiredRoleIDs, "or"))
	}
	if len(g.BlockedRoleIDs) > 0 {
		lines = append(lines, "Not for "+roleList(g.BlockedRoleIDs, "or"))
	}
	if g.MinJoinDays > 0 {
		lines = append(lines, fmt.Sprintf("In this server for %d+ days", g.MinJoinDays))
	}
	if g.BoostersOnly {
		lines = append(lines, "Server boosters only")
	}
	if g.NoActivePunishment {
		lines = append(lines, fmt.Sprintf("No active punishment or strike (%s, %s)", g.cmd("punishments"), g.cmd("strikes")))
	}
	if g.ExcludePastWinners {
		lines = append(lines, "Earlier winners cannot enter")
	}
	return lines
}

// bonusText says who gets extra entries, "" when nobody does.
func (g giveawayDoc) bonusText() string {
	var parts []string
	for _, name := range sortedKeys(g.BonusRanks) {
		parts = append(parts, fmt.Sprintf("%s +%d", name, g.BonusRanks[name]))
	}
	for _, id := range sortedKeys(g.BonusRoles) {
		parts = append(parts, fmt.Sprintf("<@&%s> +%d", id, g.BonusRoles[id]))
	}
	return strings.Join(parts, " · ")
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v > 0 {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// rulesText lists a giveaway's rules, one per line, as plain text.
func (g giveawayDoc) rulesText() string {
	lines := g.requirements()
	if g.bonusText() != "" {
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
