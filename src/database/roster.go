package database

import (
	"net/url"
	"strconv"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// GuildPlayers reads one page (the API caps it at 200) of a server's registered players, with the total number registered.
func GuildPlayers(guildID string, limit, offset int) (rows []sdk.PlayerView, total int, err error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}, "sort": {"username_asc"}}
	h, err := apiCallHeaders("GET", "/v1/guilds/"+url.PathEscape(guildID)+"/players?"+q.Encode(), nil, &rows)
	if err != nil {
		return nil, 0, err
	}
	total = len(rows) + offset
	if n, convErr := strconv.Atoi(h.Get("X-Total-Count")); convErr == nil && n > total {
		total = n
	}
	return rows, total, nil
}

// PriorSeason is the server's most recent season that has already started and is not the active one (0 when there is none).
func PriorSeason(guildID string) (id uint, name string, err error) {
	var list []struct {
		ID       uint      `json:"id"`
		Number   int       `json:"number"`
		Name     string    `json:"name"`
		StartsAt time.Time `json:"starts_at"`
		IsActive bool      `json:"is_active"`
	}
	if _, err = apiCallHeaders("GET", "/v1/guilds/"+url.PathEscape(guildID)+"/seasons?limit=200&sort=number&order=desc", nil, &list); err != nil {
		return 0, "", err
	}
	for _, s := range list { // newest first
		if !s.IsActive && !s.StartsAt.After(time.Now()) {
			return s.ID, s.Name, nil
		}
	}
	return 0, "", nil
}

// SeasonPlayers reads one page of the players who have stats in a season (everyone who took part), with how many there are.
func SeasonPlayers(guildID string, seasonID uint, limit, offset int) (playerIDs []uint, total int, err error) {
	rows, total, err := Leaderboard(guildID, seasonID, limit, offset, "elo")
	if err != nil {
		return nil, 0, err
	}
	for _, r := range rows {
		playerIDs = append(playerIDs, r.PlayerID)
	}
	return playerIDs, total, nil
}
