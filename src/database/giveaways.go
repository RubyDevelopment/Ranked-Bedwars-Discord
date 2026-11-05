package database

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The giveaway calls below talk to the database API directly (see links.go).

// Giveaway is a giveaway row; Rules is the JSON of its entry rules.
type Giveaway struct {
	ID          uint            `json:"id"`
	Prize       string          `json:"prize"`
	Description string          `json:"description"`
	Winners     int             `json:"winners"`
	ChannelID   string          `json:"channel_id"`
	EndsAt      time.Time       `json:"ends_at"`
	Status      string          `json:"status"`
	MessageID   string          `json:"message_id"`
	Rules       json.RawMessage `json:"rules"`
	CreatedBy   string          `json:"created_by"`
}

// GiveawayEntry is one entry of a giveaway.
type GiveawayEntry struct {
	UserID   string `json:"user_id"`
	Weight   int    `json:"weight"`
	IsWinner bool   `json:"is_winner"`
}

func giveawaysPath(guildID string, parts ...string) string {
	p := "/v1/guilds/" + url.PathEscape(guildID) + "/giveaways"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func idPart(id uint) string { return strconv.FormatUint(uint64(id), 10) }

// Giveaways lists the guild's giveaways in these statuses (a comma list, "" for all), up to 200.
func Giveaways(guildID, statuses string) ([]Giveaway, error) {
	q := url.Values{"limit": {"200"}}
	if statuses != "" {
		q.Set("status", statuses)
	}
	var list []Giveaway
	err := apiCall(http.MethodGet, giveawaysPath(guildID)+"?"+q.Encode(), nil, &list)
	return list, err
}

// GiveawayByID is one giveaway, or nil when it is gone.
func GiveawayByID(guildID string, id uint) (*Giveaway, error) {
	var g Giveaway
	if err := apiCall(http.MethodGet, giveawaysPath(guildID, idPart(id)), nil, &g); err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &g, nil
}

// UpdateGiveaway changes fields of a giveaway (status, message_id...).
func UpdateGiveaway(guildID string, id uint, fields map[string]any) error {
	return apiCall(http.MethodPatch, giveawaysPath(guildID, idPart(id)), fields, nil)
}

// EnterGiveaway records an entry. entered is false when the member had entered already; count is how many entries there are.
func EnterGiveaway(guildID string, id uint, userID string, weight int) (entered bool, count int64, err error) {
	var out struct {
		Entered bool  `json:"entered"`
		Count   int64 `json:"count"`
	}
	err = apiCall(http.MethodPost, giveawaysPath(guildID, idPart(id), "entries"), map[string]any{"user_id": userID, "weight": weight}, &out)
	return out.Entered, out.Count, err
}

// GiveawayEntries lists a giveaway's entries (up to 5000).
func GiveawayEntries(guildID string, id uint) ([]GiveawayEntry, error) {
	var list []GiveawayEntry
	err := apiCall(http.MethodGet, giveawaysPath(guildID, idPart(id), "entries")+"?limit=5000", nil, &list)
	return list, err
}

// FinishGiveaway ends a giveaway with its winners. It works once: a second call is an error, so winners are not announced twice.
func FinishGiveaway(guildID string, id uint, winners []string) error {
	if winners == nil {
		winners = []string{}
	}
	return apiCall(http.MethodPost, giveawaysPath(guildID, idPart(id), "finish"), map[string]any{"winners": winners}, nil)
}

// HasWonGiveaway says whether a member won an earlier giveaway of the guild (other than excludeID).
func HasWonGiveaway(guildID, userID string, excludeID uint) (bool, error) {
	var out struct {
		Won bool `json:"won"`
	}
	q := url.Values{"user_id": {userID}, "exclude": {idPart(excludeID)}}
	if err := apiCall(http.MethodGet, giveawaysPath(guildID, "winner")+"?"+q.Encode(), nil, &out); err != nil {
		return false, fmt.Errorf("checking earlier giveaway wins: %w", err)
	}
	return out.Won, nil
}
