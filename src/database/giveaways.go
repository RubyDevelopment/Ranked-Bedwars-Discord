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

// NewGiveaway is what a giveaway is made from.
type NewGiveaway struct {
	Prize       string
	Description string
	Winners     int
	ChannelID   string
	EndsAt      time.Time
	Rules       json.RawMessage
	CreatedBy   string
}

// CreateGiveaway saves a giveaway (scheduled: the bot posts it within moments) and answers the saved row.
func CreateGiveaway(guildID string, g NewGiveaway) (*Giveaway, error) {
	rules := g.Rules
	if len(rules) == 0 {
		rules = json.RawMessage("{}")
	}
	body := map[string]any{
		"prize": g.Prize, "description": g.Description, "winners": g.Winners, "channel_id": g.ChannelID,
		"ends_at": g.EndsAt.UTC(), "rules": rules, "created_by": g.CreatedBy, "status": "scheduled",
	}
	var out Giveaway
	if err := apiCall(http.MethodPost, giveawaysPath(guildID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetGiveawayWinners names new winners of an ended giveaway. With replace the earlier winners lose the mark. It answers all the winners
// the giveaway has afterwards.
func SetGiveawayWinners(guildID string, id uint, winners []string, replace bool) ([]string, error) {
	var out struct {
		Winners []string `json:"winners"`
	}
	err := apiCall(http.MethodPost, giveawaysPath(guildID, idPart(id), "winners"), map[string]any{"winners": winners, "replace": replace}, &out)
	return out.Winners, err
}

// RecentGiveaways lists the guild's giveaways, newest first (up to 25), for staff to pick from.
func RecentGiveaways(guildID string) ([]Giveaway, error) {
	var list []Giveaway
	err := apiCall(http.MethodGet, giveawaysPath(guildID)+"?limit=25&order=desc", nil, &list)
	return list, err
}

// GiveawayEntryCount is how many entries a giveaway holds (0 when it cannot be read).
func GiveawayEntryCount(guildID string, id uint) int64 {
	headers, err := apiCallHeaders(http.MethodGet, giveawaysPath(guildID, idPart(id), "entries")+"?limit=1", nil, nil)
	if err != nil || headers == nil {
		return 0
	}
	n, _ := strconv.ParseInt(headers.Get("X-Total-Count"), 10, 64)
	return n
}
