// Package integrity is the bot's side of the ladder-integrity check. The database works the findings out from every finished game of a season
// (win trading, boosting, throwing, dodging losses by voiding), each with its probability, evidence and caveats; this package asks for them,
// adds what the game chats say about the games involved, and writes them up for staff.
package integrity

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"ranked-bedwars/src/database"
)

// Evidence is one fact a finding rests on, with the game numbers it is about.
type Evidence struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Games []int  `json:"games"`
}

// Finding is one suspicion.
type Finding struct {
	Kind      string     `json:"kind"`
	Subtype   string     `json:"subtype"`
	Severity  string     `json:"severity"`
	Players   []uint     `json:"players"`
	P         float64    `json:"p"`
	Q         float64    `json:"q"`
	Summary   string     `json:"summary"`
	Reasoning string     `json:"reasoning"`
	Evidence  []Evidence `json:"evidence"`
	Caveats   []string   `json:"caveats"`
	Games     []int      `json:"games"`
}

// Risk adds up the findings that name a player.
type Risk struct {
	PlayerID uint   `json:"player_id"`
	Name     string `json:"name"`
	Score    int    `json:"score"`
	Findings int    `json:"findings"`
	Worst    string `json:"worst"`
	Strikes  int    `json:"strikes"`
}

// Report is what the database answers.
type Report struct {
	Season        string          `json:"season"`
	GamesAnalysed int             `json:"games_analysed"`
	GamesVoided   int             `json:"games_voided"`
	Players       int             `json:"players"`
	From          time.Time       `json:"from"`
	To            time.Time       `json:"to"`
	Findings      []Finding       `json:"findings"`
	Risk          []Risk          `json:"risk"`
	Names         map[uint]string `json:"names"`
	Notes         []string        `json:"notes"`
}

// Fetch asks the database for the season's report; player (a Discord id) narrows it to one player's findings. games is how many of the newest
// games to read (0 = the database's default).
func Fetch(ctx context.Context, guildID, player string, games int) (*Report, error) {
	path := "/v1/guilds/" + url.PathEscape(guildID) + "/integrity"
	if player != "" {
		path = "/v1/guilds/" + url.PathEscape(guildID) + "/players/" + url.PathEscape(player) + "/integrity"
	}
	if games > 0 {
		path += "?games=" + strconv.Itoa(games)
	}
	var rep Report
	if err := database.Must().Client().API.Do(ctx, http.MethodGet, path, nil, &rep); err != nil {
		return nil, err
	}
	return &rep, nil
}

var severityRank = map[string]int{"low": 1, "medium": 2, "high": 3, "critical": 4}

// AtLeast reports whether a finding is at least as severe as the named level.
func (f Finding) AtLeast(level string) bool { return severityRank[f.Severity] >= severityRank[level] }

// Number is the finding's place in the report, which is how staff name it (1 is the most serious).
func (r *Report) Number(f Finding) int {
	for i := range r.Findings {
		if r.Findings[i].Kind == f.Kind && r.Findings[i].Summary == f.Summary {
			return i + 1
		}
	}
	return 0
}
