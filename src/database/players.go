package database

import (
	"net/http"
	"net/url"
	"strconv"
)

// PlayerStats is one registered player with their stats of the active season, as the player list gives them.
type PlayerStats struct {
	Snowflake string
	Elo       int
	Wins      int
	Games     int
}

// PlayersWithStats reads one page (up to 200) of a guild's registered players, best ELO first, each with the Discord ID and the active
// season's stats, in a single request. A page shorter than the limit is the last one.
func PlayersWithStats(guildID string, limit, offset int) ([]PlayerStats, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}, "sort": {"elo_desc"}, "include": {"stats"}}
	var rows []struct {
		Player struct {
			Snowflake string `json:"snowflake_id"`
		} `json:"player"`
		Stats *struct {
			Elo   int `json:"elo"`
			Wins  int `json:"wins"`
			Games int `json:"games_played"`
		} `json:"stats"`
	}
	if err := apiCall(http.MethodGet, "/v1/guilds/"+url.PathEscape(guildID)+"/players?"+q.Encode(), nil, &rows); err != nil {
		return nil, err
	}
	out := make([]PlayerStats, 0, len(rows))
	for _, r := range rows {
		if r.Player.Snowflake == "" {
			continue
		}
		p := PlayerStats{Snowflake: r.Player.Snowflake}
		if r.Stats != nil {
			p.Elo, p.Wins, p.Games = r.Stats.Elo, r.Stats.Wins, r.Stats.Games
		}
		out = append(out, p)
	}
	return out, nil
}
