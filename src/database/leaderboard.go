package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// Leaderboard reads one page of a season's leaderboard together with the number of players ranked in it.
//
// The SDK's own Stats.Leaderboard returns the rows but drops the API's X-Total-Count header and reports the number of rows on the page
// as the total, so every leaderboard looked like it had one page (no way to know there was a page 2). This asks the API directly, once,
// and reads both from the same answer. sort is a stat column ("elo", "kills", ...).
func Leaderboard(guildID string, seasonID uint, limit, offset int, sort string) (rows []sdk.LeaderboardEntry, total int, err error) {
	if apiConn.baseURL == "" {
		return nil, 0, errors.New("database API is not configured")
	}
	query := url.Values{"season_id": {strconv.FormatUint(uint64(seasonID), 10)}}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	if sort != "" {
		query.Set("sort", sort)
	}
	endpoint := apiConn.baseURL + "/v1/guilds/" + url.PathEscape(guildID) + "/leaderboard?" + query.Encode()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiConn.apiKey)
	res, err := (&http.Client{Timeout: max(apiConn.timeout, 10*time.Second)}).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("leaderboard request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("leaderboard answered %d", res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(&rows); err != nil {
		return nil, 0, fmt.Errorf("decode leaderboard: %w", err)
	}
	total = len(rows) + offset // what the page itself proves, if the API does not say
	if n, err := strconv.Atoi(res.Header.Get("X-Total-Count")); err == nil && n >= total {
		total = n
	}
	return rows, total, nil
}
