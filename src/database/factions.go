package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The faction calls below talk to the database API directly (see links.go).

// FactionMember is a member of a faction, with who they are.
type FactionMember struct {
	PlayerID    uint      `json:"player_id"`
	SnowflakeID string    `json:"snowflake_id"`
	Username    string    `json:"username"`
	Nickname    string    `json:"nickname"`
	Role        string    `json:"role"`
	JoinedAt    time.Time `json:"joined_at"`
	Trophies    int       `json:"trophies"`
}

// Name is how a member is shown: their nickname, else their username.
func (m FactionMember) Name() string {
	if m.Nickname != "" {
		return m.Nickname
	}
	if m.Username != "" {
		return m.Username
	}
	return m.SnowflakeID
}

// FactionTitle is a season a faction placed in.
type FactionTitle struct {
	SeasonID  uint   `json:"season_id"`
	Trophies  int    `json:"trophies"`
	Placement int    `json:"placement"`
	Title     string `json:"title"`
}

// Faction is a faction as the API gives it: the summary (what a list shows) and, from a lookup of one, the profile (members and titles).
type Faction struct {
	ID                uint      `json:"id"`
	GuildID           uint      `json:"guild_id"`
	Name              string    `json:"name"`
	Tag               string    `json:"tag"`
	FounderID         uint      `json:"founder_id"`
	Description       string    `json:"description"`
	Color             string    `json:"color"`
	Icon              string    `json:"icon"`
	IconURL           string    `json:"icon_url"`
	TagStyle          string    `json:"tag_style"`
	RoleID            string    `json:"role_id"`
	LifetimeTrophies  int       `json:"lifetime_trophies"`
	Level             int       `json:"level"`
	CreatedAt         time.Time `json:"created_at"`
	DisplayTag        string    `json:"display_tag"`
	Prestige          string    `json:"prestige"`
	Members           int       `json:"members"`
	Slots             int       `json:"slots"`
	OfficerSlots      int       `json:"officer_slots"`
	NextLevelTrophies int       `json:"next_level_trophies"`
	SeasonTrophies    int       `json:"season_trophies"`
	LifetimeRank      int       `json:"lifetime_rank"`
	SeasonRank        int       `json:"season_rank"`

	Founder         *FactionMember  `json:"founder"`
	Officers        []FactionMember `json:"officers"`
	MemberList      []FactionMember `json:"member_list"`
	TopContributors []FactionMember `json:"top_contributors"`
	Titles          []FactionTitle  `json:"titles"`
}

// FactionInvite is an invitation to join a faction.
type FactionInvite struct {
	ID        uint      `json:"id"`
	FactionID uint      `json:"faction_id"`
	PlayerID  uint      `json:"player_id"`
	InvitedBy uint      `json:"invited_by"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
	Faction   Faction   `json:"faction"`
}

// FactionEligibility is what a player has done, for deciding whether they may start a faction.
type FactionEligibility struct {
	RankedGames int `json:"ranked_games"`
	RankedWins  int `json:"ranked_wins"`
	ActiveBans  int `json:"active_bans"`
}

func factionsPath(guildID string, parts ...string) string {
	p := "/v1/guilds/" + url.PathEscape(guildID) + "/factions"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

// IsNotFound says whether err is the API answering 404 (nothing there).
func IsNotFound(err error) bool {
	var api *APIStatusError
	return errors.As(err, &api) && api.Status == http.StatusNotFound
}

// Factions pages the guild's factions: sort is "lifetime" (default) or "season". total is how many there are.
func Factions(guildID, sort, search string, limit, offset int) (list []Faction, total int, err error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	if sort != "" {
		q.Set("sort", sort)
	}
	if search != "" {
		q.Set("search", search)
	}
	h, err := apiCallHeaders(http.MethodGet, factionsPath(guildID)+"?"+q.Encode(), nil, &list)
	if err != nil {
		return nil, 0, err
	}
	total, _ = strconv.Atoi(h.Get("X-Total-Count"))
	return list, max(total, len(list)), nil
}

// FactionByName is the faction with this name or tag, or nil when there is none.
func FactionByName(guildID, nameOrTag string) (*Faction, error) {
	return findFaction(guildID, url.Values{"name": {nameOrTag}})
}

// FactionOf is the faction a player (Discord id) is in, or nil when they are in none (or are not registered).
func FactionOf(guildID, discordID string) (*Faction, error) {
	return findFaction(guildID, url.Values{"player": {discordID}})
}

func findFaction(guildID string, q url.Values) (*Faction, error) {
	var f Faction
	if err := apiCall(http.MethodGet, factionsPath(guildID, "find")+"?"+q.Encode(), nil, &f); err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

// FactionByID is a faction's profile.
func FactionByID(guildID string, id uint) (*Faction, error) {
	var f Faction
	if err := apiCall(http.MethodGet, factionsPath(guildID, strconv.FormatUint(uint64(id), 10)), nil, &f); err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

// CreateFaction makes a faction with the player (Discord id) as founder.
func CreateFaction(guildID, name, tag, founderDiscordID string) (*Faction, error) {
	var f Faction
	err := apiCall(http.MethodPost, factionsPath(guildID), map[string]string{"name": name, "tag": tag, "founder": founderDiscordID}, &f)
	return &f, err
}

// UpdateFaction changes a faction: the keys of fields are the API's (name, tag, description, color, icon, icon_url, tag_style, role_id).
func UpdateFaction(guildID string, id uint, fields map[string]any) (*Faction, error) {
	var f Faction
	err := apiCall(http.MethodPatch, factionsPath(guildID, strconv.FormatUint(uint64(id), 10)), fields, &f)
	return &f, err
}

// DisbandFaction deletes a faction.
func DisbandFaction(guildID string, id uint) error {
	return apiCall(http.MethodDelete, factionsPath(guildID, strconv.FormatUint(uint64(id), 10)), nil, nil)
}

// SetFactionRole changes a member's role ("officer", "member" or "founder": the last hands the faction over).
func SetFactionRole(guildID string, id, playerID uint, role string) error {
	return apiCall(http.MethodPatch, factionsPath(guildID, strconv.FormatUint(uint64(id), 10), "members", strconv.FormatUint(uint64(playerID), 10)), map[string]string{"role": role}, nil)
}

// RemoveFactionMember takes a member out (they left, or were kicked).
func RemoveFactionMember(guildID string, id, playerID uint) error {
	return apiCall(http.MethodDelete, factionsPath(guildID, strconv.FormatUint(uint64(id), 10), "members", strconv.FormatUint(uint64(playerID), 10)), nil, nil)
}

// InviteToFaction invites a player (Discord id); invitedBy is the member who does (Discord id).
func InviteToFaction(guildID string, id uint, playerDiscordID, invitedByDiscordID string) (*FactionInvite, error) {
	var inv FactionInvite
	err := apiCall(http.MethodPost, factionsPath(guildID, strconv.FormatUint(uint64(id), 10), "invites"), map[string]string{"player": playerDiscordID, "invited_by": invitedByDiscordID}, &inv)
	return &inv, err
}

// FactionInvites lists a player's waiting invitations.
func FactionInvites(guildID, discordID string) ([]FactionInvite, error) {
	var list []FactionInvite
	err := apiCall(http.MethodGet, factionsPath(guildID, "invites")+"?"+url.Values{"player": {discordID}}.Encode(), nil, &list)
	return list, err
}

// RespondFactionInvite accepts, declines or cancels an invitation (action: "accept", "decline", "cancel").
func RespondFactionInvite(guildID string, inviteID uint, discordID, action string) (*FactionInvite, error) {
	var inv FactionInvite
	err := apiCall(http.MethodPost, factionsPath(guildID, "invites", strconv.FormatUint(uint64(inviteID), 10), "respond"), map[string]string{"player": discordID, "action": action}, &inv)
	return &inv, err
}

// FactionEligibilityOf is what a player has done: ranked games and wins over every season, and active bans.
func FactionEligibilityOf(guildID, discordID string) (*FactionEligibility, error) {
	var e FactionEligibility
	err := apiCall(http.MethodGet, factionsPath(guildID, "eligibility")+"?"+url.Values{"player": {discordID}}.Encode(), nil, &e)
	return &e, err
}

// ============================================================================
// Giveaways
// ============================================================================

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
