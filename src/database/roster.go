package database

import (
	"net/url"
	"strconv"

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
