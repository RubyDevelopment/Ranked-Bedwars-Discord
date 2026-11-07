package database

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The faction calls below talk to the database API directly (see links.go). Players are named by Discord id; the API resolves them.

// FactionUnlocks are the levels the cosmetic rewards open at.
type FactionUnlocks struct {
	Tag       int `json:"tag"`
	Color     int `json:"color"`
	Role      int `json:"role"`
	Icon      int `json:"icon"`
	Advanced  int `json:"advanced"`
	Prestige  int `json:"prestige"`
	Legendary int `json:"legendary"`
}

// FactionConfig is the part of a guild's faction ruleset the bot reads.
type FactionConfig struct {
	Enabled  bool `json:"enabled"`
	Creation struct {
		MinServerDays  int  `json:"min_server_days"`
		MinRankedGames int  `json:"min_ranked_games"`
		MinRankedWins  int  `json:"min_ranked_wins"`
		BlockPunished  bool `json:"block_punished"`
	} `json:"creation"`
	NameMin            int            `json:"name_min"`
	NameMax            int            `json:"name_max"`
	TagMin             int            `json:"tag_min"`
	TagMax             int            `json:"tag_max"`
	MaxLevel           int            `json:"max_level"`
	Unlocks            FactionUnlocks `json:"unlocks"`
	LeaderboardPerPage int            `json:"leaderboard_per_page"`
	Trophies           struct {
		PerWin   int `json:"per_win"`
		MvpBonus int `json:"mvp_bonus"`
	} `json:"trophies"`
	Slots []struct {
		Level    int `json:"level"`
		Slots    int `json:"slots"`
		Officers int `json:"officers"`
	} `json:"slots"`
}

// Faction is a faction row.
type Faction struct {
	ID               uint      `json:"id"`
	Name             string    `json:"name"`
	Tag              string    `json:"tag"`
	Color            string    `json:"color"`
	Icon             string    `json:"icon"`
	Symbol           string    `json:"symbol"`
	RoleID           string    `json:"role_id"`
	LifetimeTrophies int       `json:"lifetime_trophies"`
	Level            int       `json:"level"`
	CreatedAt        time.Time `json:"created_at"`
}

// FactionMember is one seat of a faction (Role is founder, officer or member).
type FactionMember struct {
	PlayerID    uint      `json:"player_id"`
	SnowflakeID string    `json:"snowflake_id"`
	Role        string    `json:"role"`
	Trophies    int       `json:"trophies"`
	JoinedAt    time.Time `json:"joined_at"`
}

// FactionBadge is a seasonal title.
type FactionBadge struct {
	SeasonID  uint   `json:"season_id"`
	Placement int    `json:"placement"`
	Title     string `json:"title"`
}

// FactionProfile is a faction with its level progress, ranks, seats, members and what it has unlocked.
type FactionProfile struct {
	Faction          Faction         `json:"faction"`
	DisplayTag       string          `json:"display_tag"`
	Color            string          `json:"color"`
	Prestige         string          `json:"prestige"`
	Legendary        bool            `json:"legendary"`
	Level            int             `json:"level"`
	MaxLevel         int             `json:"max_level"`
	LifetimeTrophies int             `json:"lifetime_trophies"`
	NextLevelAt      int             `json:"next_level_at"`
	LevelStartAt     int             `json:"level_start_at"`
	SeasonTrophies   int             `json:"season_trophies"`
	LifetimeRank     int             `json:"lifetime_rank"`
	SeasonRank       int             `json:"season_rank"`
	Slots            int             `json:"slots"`
	OfficerSlots     int             `json:"officer_slots"`
	Members          []FactionMember `json:"members"`
	Founder          string          `json:"founder"`
	Contributors     []FactionMember `json:"contributors"`
	Badges           []FactionBadge  `json:"badges"`
	Unlocked         struct {
		Tag, Color, Role, Icon, Advanced, Prestige, Legendary bool
	} `json:"unlocked"`
	Season *struct {
		ID     uint   `json:"id"`
		Number int    `json:"number"`
		Name   string `json:"name"`
	} `json:"season,omitempty"`
}

// FactionBrief is a player's faction in brief: what their stats card and nickname show.
type FactionBrief struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	Tag        string `json:"tag"`
	DisplayTag string `json:"display_tag"`
	Color      string `json:"color"`
	Icon       string `json:"icon"`
	RoleID     string `json:"role_id"`
	Level      int    `json:"level"`
	Role       string `json:"role"`
	Prestige   string `json:"prestige"`
}

// FactionRow is one line of a faction leaderboard.
type FactionRow struct {
	Faction
	Rank           int    `json:"rank"`
	SeasonTrophies int    `json:"season_trophies"`
	MemberCount    int    `json:"member_count"`
	DisplayTag     string `json:"display_tag"`
	Prestige       string `json:"prestige"`
}

// FactionInvite is an invitation a player holds.
type FactionInvite struct {
	FactionID   uint      `json:"faction_id"`
	FactionName string    `json:"faction_name"`
	FactionTag  string    `json:"faction_tag"`
	InvitedBy   string    `json:"invited_by"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// FactionCheck is one creation requirement and how a player stands against it.
type FactionCheck struct {
	Label string `json:"label"`
	Need  int    `json:"need"`
	Have  int    `json:"have"`
	Met   bool   `json:"met"`
}

// FactionGameTrophies is what a finished game gave one faction.
type FactionGameTrophies struct {
	FactionID   uint   `json:"faction_id"`
	Name        string `json:"name"`
	DisplayTag  string `json:"display_tag"`
	Color       string `json:"color"`
	Trophies    int    `json:"trophies"`
	LevelBefore int    `json:"level_before"`
	LevelAfter  int    `json:"level_after"`
	Lifetime    int    `json:"lifetime_trophies"`
	Players     []struct {
		SnowflakeID string `json:"snowflake_id"`
		Trophies    int    `json:"trophies"`
		Mvp         bool   `json:"mvp"`
	} `json:"players"`
}

// FactionDisbanded is what a disbanded faction leaves to clean up: its row (with the Discord role) and its members' Discord ids.
type FactionDisbanded struct {
	Faction Faction  `json:"faction"`
	Members []string `json:"members"`
}

func factionsPath(guildID string, parts ...string) string {
	p := "/v1/guilds/" + url.PathEscape(guildID) + "/factions"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

// FactionMessage is the database's own explanation of a refused faction request ("Only the Founder can ..."), or "" when err is
// something else (a network failure, a missing resource).
func FactionMessage(err error) string {
	var api *APIStatusError
	if errors.As(err, &api) && api.Status == http.StatusBadRequest {
		return api.Message
	}
	return ""
}

func FactionConfigGet(guildID string) (*FactionConfig, error) {
	var out struct {
		Config FactionConfig `json:"config"`
	}
	if err := apiCall(http.MethodGet, factionsPath(guildID, "config"), nil, &out); err != nil {
		return nil, err
	}
	return &out.Config, nil
}

// FactionFor is a player's faction profile, or nil when they have none.
func FactionFor(guildID, userID string) (*FactionProfile, error) {
	return factionLookup(guildID, url.Values{"player": {userID}})
}

// FactionNamed is the faction with this name or tag, or nil.
func FactionNamed(guildID, query string) (*FactionProfile, error) {
	return factionLookup(guildID, url.Values{"q": {query}})
}

func factionLookup(guildID string, q url.Values) (*FactionProfile, error) {
	var p FactionProfile
	if err := apiCall(http.MethodGet, factionsPath(guildID, "lookup")+"?"+q.Encode(), nil, &p); err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// FactionBriefFor is a player's faction in brief, or nil when they have none.
func FactionBriefFor(guildID, userID string) (*FactionBrief, error) {
	var b FactionBrief
	if err := apiCall(http.MethodGet, factionsPath(guildID, "brief")+"?"+url.Values{"player": {userID}}.Encode(), nil, &b); err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

func FactionCreate(guildID, userID, name, tag string, serverDays int) (*FactionProfile, error) {
	var p FactionProfile
	err := apiCall(http.MethodPost, factionsPath(guildID), map[string]any{"actor": userID, "name": name, "tag": tag, "server_days": serverDays}, &p)
	return &p, err
}

func FactionEligibility(guildID, userID string, serverDays int) (bool, []FactionCheck, error) {
	var out struct {
		Eligible bool           `json:"eligible"`
		Checks   []FactionCheck `json:"checks"`
	}
	q := url.Values{"player": {userID}, "server_days": {strconv.Itoa(serverDays)}}
	err := apiCall(http.MethodGet, factionsPath(guildID, "eligibility")+"?"+q.Encode(), nil, &out)
	return out.Eligible, out.Checks, err
}

// FactionLeaderboard lists a page of factions: sort is "lifetime" or "seasonal". total is how many factions exist.
func FactionLeaderboard(guildID, sort string, limit, offset int) (rows []FactionRow, total int64, err error) {
	q := url.Values{"sort": {sort}, "limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	h, err := apiCallHeaders(http.MethodGet, factionsPath(guildID)+"?"+q.Encode(), nil, &rows)
	if err == nil {
		total, _ = strconv.ParseInt(h.Get("X-Total-Count"), 10, 64)
	}
	return rows, total, err
}

// FactionInvitePlayer offers a seat and answers when the invitation expires.
func FactionInvitePlayer(guildID string, factionID uint, actor, target string) (time.Time, error) {
	var out struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	err := apiCall(http.MethodPost, factionsPath(guildID, idPart(factionID), "invites"), map[string]any{"actor": actor, "target": target}, &out)
	return out.ExpiresAt, err
}

func FactionInvites(guildID, userID string) ([]FactionInvite, error) {
	var list []FactionInvite
	err := apiCall(http.MethodGet, factionsPath(guildID, "invites")+"?"+url.Values{"player": {userID}}.Encode(), nil, &list)
	return list, err
}

// FactionAnswer accepts or declines an invitation (factionID 0 means the newest). Accepting answers the faction joined.
func FactionAnswer(guildID, userID string, factionID uint, accept bool) (*FactionProfile, error) {
	var p FactionProfile
	err := apiCall(http.MethodPost, factionsPath(guildID, "invites", "answer"), map[string]any{"player": userID, "faction_id": factionID, "accept": accept}, &p)
	if !accept || err != nil {
		return nil, err
	}
	return &p, nil
}

func FactionLeave(guildID, userID string) (*Faction, error) {
	var f Faction
	err := apiCall(http.MethodPost, factionsPath(guildID, "leave"), map[string]any{"player": userID}, &f)
	return &f, err
}

func FactionKick(guildID string, factionID uint, actor, target string) error {
	return apiCall(http.MethodPost, factionsPath(guildID, idPart(factionID), "kick"), map[string]any{"actor": actor, "target": target}, nil)
}

func FactionSetRole(guildID string, factionID uint, actor, target, role string) error {
	return apiCall(http.MethodPost, factionsPath(guildID, idPart(factionID), "role"), map[string]any{"actor": actor, "target": target, "role": role}, nil)
}

func FactionTransfer(guildID string, factionID uint, actor, target string) error {
	return apiCall(http.MethodPost, factionsPath(guildID, idPart(factionID), "transfer"), map[string]any{"actor": actor, "target": target}, nil)
}

func FactionDisband(guildID string, factionID uint, actor string, force bool) (*FactionDisbanded, error) {
	var out FactionDisbanded
	err := apiCall(http.MethodPost, factionsPath(guildID, idPart(factionID), "disband"), map[string]any{"actor": actor, "force": force}, &out)
	return &out, err
}

// FactionCustomize changes the named fields (name, tag, color, icon, symbol, role_id) of a faction on behalf of actor. With force
// (the bot's own bookkeeping, such as storing the Discord role it made) no Founder or unlock check applies.
func FactionCustomize(guildID string, factionID uint, actor string, force bool, fields map[string]any) (*FactionProfile, error) {
	body := map[string]any{"actor": actor, "force": force}
	for k, v := range fields {
		body[k] = v
	}
	var p FactionProfile
	err := apiCall(http.MethodPatch, factionsPath(guildID, idPart(factionID)), body, &p)
	return &p, err
}

// The calls below are the staff versions: they skip the Founder, level and creation checks.

// FactionAdminCreate founds a faction with the player as its Founder, whatever the creation requirements say.
func FactionAdminCreate(guildID, founder, name, tag string) (*FactionProfile, error) {
	var p FactionProfile
	err := apiCall(http.MethodPost, factionsPath(guildID), map[string]any{"actor": founder, "name": name, "tag": tag, "force": true}, &p)
	return &p, err
}

// FactionAdminAdd seats a player in a faction without an invitation.
func FactionAdminAdd(guildID string, factionID uint, target string) (*FactionProfile, error) {
	var p FactionProfile
	err := apiCall(http.MethodPost, factionsPath(guildID, idPart(factionID), "members"), map[string]any{"target": target}, &p)
	return &p, err
}

// FactionAdminKick removes any member but the Founder.
func FactionAdminKick(guildID string, factionID uint, target string) error {
	return apiCall(http.MethodPost, factionsPath(guildID, idPart(factionID), "kick"), map[string]any{"target": target, "force": true}, nil)
}

// FactionAdminRole makes a member an officer or a plain member.
func FactionAdminRole(guildID string, factionID uint, target, role string) error {
	return apiCall(http.MethodPost, factionsPath(guildID, idPart(factionID), "role"), map[string]any{"target": target, "role": role, "force": true}, nil)
}

// FactionAdminTransfer makes the target the Founder.
func FactionAdminTransfer(guildID string, factionID uint, target string) error {
	return apiCall(http.MethodPost, factionsPath(guildID, idPart(factionID), "transfer"), map[string]any{"target": target, "force": true}, nil)
}

// FactionAdminTrophies changes a faction's trophies: board is "lifetime" or "seasonal", mode "add" or "set".
func FactionAdminTrophies(guildID string, factionID uint, board, mode string, amount int) (*FactionProfile, error) {
	var p FactionProfile
	err := apiCall(http.MethodPost, factionsPath(guildID, idPart(factionID), "trophies"), map[string]any{"board": board, "mode": mode, "amount": amount}, &p)
	return &p, err
}

// FactionGameTrophiesFor is what a finished game gave each faction.
func FactionGameTrophiesFor(guildID string, gameID uint) ([]FactionGameTrophies, error) {
	var list []FactionGameTrophies
	err := apiCall(http.MethodGet, factionsPath(guildID, "games", idPart(gameID), "trophies"), nil, &list)
	return list, err
}
