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

// PriorSeason picks the season whose players are reset. With want != 0 that exact season (it must belong to the server and not be the
// active one); otherwise the newest season that has already started, is not active and has at least one player (the newest one when
// none has). It returns id 0 when there is no such season.
func PriorSeason(guildID string, want uint) (id uint, name string, err error) {
	var list []struct {
		ID       uint      `json:"id"`
		Name     string    `json:"name"`
		StartsAt time.Time `json:"starts_at"`
		IsActive bool      `json:"is_active"`
	}
	if _, err = apiCallHeaders("GET", "/v1/guilds/"+url.PathEscape(guildID)+"/seasons?limit=200&sort=number&order=desc", nil, &list); err != nil {
		return 0, "", err
	}
	var fallbackID uint
	var fallbackName string
	for _, s := range list { // newest first
		if want != 0 {
			if s.ID == want && !s.IsActive {
				return s.ID, s.Name, nil
			}
			continue
		}
		if s.IsActive || s.StartsAt.After(time.Now()) {
			continue
		}
		if fallbackID == 0 {
			fallbackID, fallbackName = s.ID, s.Name
		}
		if _, n, perr := SeasonPlayers(guildID, s.ID, 1, 0); perr == nil && n > 0 {
			return s.ID, s.Name, nil
		}
	}
	return fallbackID, fallbackName, nil
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
