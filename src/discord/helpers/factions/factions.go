// Package factions is the server's factions: groups members join (Red, Blue, a clan, a city...) that compete over a season.
//
// A faction is a Discord role the server picks on the dashboard (Community → Factions). Being in a faction is holding its role, so there is
// nothing to keep in step: join gives the role, leave takes it, and staff can move people. A faction's score is worked out from its members'
// stats of the active season: points for every win and every game played.
package factions

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"ranked-bedwars/src/database"
)

// Faction is one of the server's factions, as the dashboard saves it.
type Faction struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Emoji       string `json:"emoji"`
	Color       string `json:"color"` // "#RRGGBB"
	Description string `json:"description"`
	RoleID      string `json:"role_id"`
}

// Settings are the server-wide rules of its factions. Unset values read as the defaults.
type Settings struct {
	Enabled           bool `json:"enabled"`
	AllowLeave        bool `json:"allow_leave"`        // may members leave a faction themselves (otherwise only staff move them)
	RequireRegistered bool `json:"require_registered"` // only registered players may join
	WinPoints         int  `json:"win_points"`
	GamePoints        int  `json:"game_points"`
}

// DefaultSettings are what a server that has not changed anything gets.
func DefaultSettings() Settings {
	return Settings{Enabled: true, AllowLeave: false, RequireRegistered: true, WinPoints: 3, GamePoints: 1}
}

// Usable says whether the faction can be joined: it has a name and a role.
func (f Faction) Usable() bool {
	return strings.TrimSpace(f.Name) != "" && f.RoleID != "" && f.ID != ""
}

// Label is the faction's name with its emoji.
func (f Faction) Label() string {
	if e := strings.TrimSpace(f.Emoji); e != "" {
		return e + " " + f.Name
	}
	return f.Name
}

// ColorInt is the faction's colour for an embed (a neutral grey when it has none).
func (f Faction) ColorInt() int {
	if n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(f.Color), "#"), 16, 32); err == nil {
		return int(n)
	}
	return 0x99AAB5
}

// ParseConfig reads the factions and their settings from the guild config's `panels` JSON. Factions that cannot be joined (no name or
// role yet) are left out.
func ParseConfig(panels []byte) (Settings, []Faction) {
	settings := DefaultSettings()
	var sections map[string]json.RawMessage
	if len(panels) == 0 || json.Unmarshal(panels, &sections) != nil {
		return settings, nil
	}
	if raw := sections["faction_settings"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &settings) // fields that are not there keep their defaults
	}
	var all []Faction
	if raw := sections["factions"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &all)
	}
	usable := all[:0]
	for _, f := range all {
		if f.Usable() {
			usable = append(usable, f)
		}
	}
	return settings, usable
}

var (
	configMu    sync.Mutex
	configCache = map[string]struct {
		at       time.Time
		settings Settings
		list     []Faction
	}{}
)

const configFor = 15 * time.Second

// Load is a server's factions and settings, read from its config at most every few seconds.
func Load(guildID string) (Settings, []Faction, error) {
	configMu.Lock()
	if c, ok := configCache[guildID]; ok && time.Since(c.at) < configFor {
		configMu.Unlock()
		return c.settings, c.list, nil
	}
	configMu.Unlock()
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		return Settings{}, nil, err
	}
	settings, list := ParseConfig(cfg.Panels)
	configMu.Lock()
	configCache[guildID] = struct {
		at       time.Time
		settings Settings
		list     []Faction
	}{time.Now(), settings, list}
	configMu.Unlock()
	return settings, list, nil
}

// Find is the faction called name (or with that id), ignoring case.
func Find(list []Faction, nameOrID string) *Faction {
	nameOrID = strings.TrimSpace(nameOrID)
	for i := range list {
		if strings.EqualFold(list[i].ID, nameOrID) || strings.EqualFold(list[i].Name, nameOrID) {
			return &list[i]
		}
	}
	return nil
}

// Of is the faction whose role the member holds (the first, if they somehow hold several).
func Of(roles []string, list []Faction) *Faction {
	for i := range list {
		if slices.Contains(roles, list[i].RoleID) {
			return &list[i]
		}
	}
	return nil
}

// ============================================================================
// Scores
// ============================================================================

// MemberStats are one member's numbers of the active season.
type MemberStats struct {
	Elo   int
	Wins  int
	Games int
}

// Standing is a faction's place in the season.
type Standing struct {
	Faction  Faction
	Members  int // everyone who holds the role
	Active   int // members who have played this season
	Wins     int
	Games    int
	AvgElo   int // of the members who have played
	Points   int
	Position int
}

// Tally scores the factions from their members' stats. members maps a faction's id to the Discord ids that hold its role.
func Tally(list []Faction, members map[string][]string, stats map[string]MemberStats, st Settings) []Standing {
	out := make([]Standing, 0, len(list))
	for _, f := range list {
		s := Standing{Faction: f, Members: len(members[f.ID])}
		eloSum := 0
		for _, id := range members[f.ID] {
			m, ok := stats[id]
			if !ok || m.Games == 0 {
				continue
			}
			s.Active++
			s.Wins += m.Wins
			s.Games += m.Games
			eloSum += m.Elo
		}
		if s.Active > 0 {
			s.AvgElo = eloSum / s.Active
		}
		s.Points = s.Wins*st.WinPoints + s.Games*st.GamePoints
		out = append(out, s)
	}
	slices.SortStableFunc(out, func(a, b Standing) int {
		return cmp.Or(cmp.Compare(b.Points, a.Points), cmp.Compare(b.Wins, a.Wins), strings.Compare(strings.ToLower(a.Faction.Name), strings.ToLower(b.Faction.Name)))
	})
	for i := range out {
		out[i].Position = i + 1
	}
	return out
}

// ============================================================================
// Reading the numbers
// ============================================================================

const (
	standingsFor   = 10 * time.Minute
	statsPageSize  = 200
	statsMaxPages  = 25 // the top 5000 players
	maxStatsErrors = 2
)

var (
	standingsMu    sync.Mutex
	standingsCache = map[string]struct {
		at    time.Time
		stats map[string]MemberStats
	}{}
)

// seasonStats is every registered player's stats of the active season, by Discord id. The numbers are kept for ten minutes; a server
// has thousands of players but only a few factions, so they are read in bulk (200 players per request) rather than per member.
func seasonStats(guildID string) (map[string]MemberStats, error) {
	standingsMu.Lock()
	if c, ok := standingsCache[guildID]; ok && time.Since(c.at) < standingsFor {
		standingsMu.Unlock()
		return c.stats, nil
	}
	standingsMu.Unlock()

	stats := map[string]MemberStats{}
	failures := 0
	for page := 0; page < statsMaxPages; page++ {
		rows, err := database.PlayersWithStats(guildID, statsPageSize, page*statsPageSize)
		if err != nil {
			if failures++; failures > maxStatsErrors || len(stats) == 0 {
				return nil, fmt.Errorf("reading players: %w", err)
			}
			continue
		}
		for _, r := range rows {
			stats[r.Snowflake] = MemberStats{Elo: r.Elo, Wins: r.Wins, Games: r.Games}
		}
		if len(rows) < statsPageSize {
			break
		}
	}
	standingsMu.Lock()
	standingsCache[guildID] = struct {
		at    time.Time
		stats map[string]MemberStats
	}{time.Now(), stats}
	standingsMu.Unlock()
	return stats, nil
}

// Standings scores every faction of the server. holders maps a faction's role id to the Discord ids that hold it (from the member list).
func Standings(guildID string, list []Faction, st Settings, holders func(roleID string) []string) ([]Standing, error) {
	stats, err := seasonStats(guildID)
	if err != nil {
		return nil, err
	}
	members := make(map[string][]string, len(list))
	for _, f := range list {
		members[f.ID] = holders(f.RoleID)
	}
	return Tally(list, members, stats, st), nil
}
