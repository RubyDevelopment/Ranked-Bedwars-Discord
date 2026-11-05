// Package factions is the bot's side of the factions: groups of players (clans) that earn trophies together.
//
// The factions themselves live in the database (game_factions, game_faction_members, game_faction_invites...; see the Database
// repository). Trophies are added there, in the same transaction that scores a ranked game. This package holds what the bot decides:
// who may found a faction, what a level unlocks, and the faction details shown on a player's card.
package factions

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
)

// ============================================================================
// Settings
// ============================================================================

// Settings are a server's rules for founding factions, set on the dashboard (Extra → Factions). Unset values read as the defaults.
type Settings struct {
	Enabled  bool `json:"enabled"`
	MinDays  int  `json:"min_days"`  // days in the server before a player may found a faction
	MinGames int  `json:"min_games"` // ranked games played, all seasons
	MinWins  int  `json:"min_wins"`  // ranked wins, all seasons
}

// DefaultSettings are what a server that has not changed anything gets.
func DefaultSettings() Settings {
	return Settings{Enabled: true, MinDays: 30, MinGames: 50, MinWins: 25}
}

// ParseSettings reads the settings from the guild config's `panels` JSON.
func ParseSettings(panels []byte) Settings {
	st := DefaultSettings()
	var sections map[string]json.RawMessage
	if len(panels) == 0 || json.Unmarshal(panels, &sections) != nil {
		return st
	}
	if raw := sections["faction_settings"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &st) // fields that are not there keep their defaults
	}
	st.MinDays, st.MinGames, st.MinWins = max(st.MinDays, 0), max(st.MinGames, 0), max(st.MinWins, 0)
	return st
}

var (
	settingsMu    sync.Mutex
	settingsCache = map[string]struct {
		at time.Time
		st Settings
	}{}
)

// LoadSettings is a server's settings, read from its config at most every few seconds.
func LoadSettings(guildID string) (Settings, error) {
	settingsMu.Lock()
	if c, ok := settingsCache[guildID]; ok && time.Since(c.at) < 15*time.Second {
		settingsMu.Unlock()
		return c.st, nil
	}
	settingsMu.Unlock()
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		return Settings{}, err
	}
	st := ParseSettings(cfg.Panels)
	settingsMu.Lock()
	settingsCache[guildID] = struct {
		at time.Time
		st Settings
	}{time.Now(), st}
	settingsMu.Unlock()
	return st, nil
}

// ============================================================================
// Founding a faction
// ============================================================================

// Shortfalls lists what a player still lacks to found a faction (nothing when they may). joined is when they joined the server.
func Shortfalls(st Settings, joined time.Time, e database.FactionEligibility, now time.Time) []string {
	var out []string
	if st.MinDays > 0 {
		if joined.IsZero() {
			out = append(out, fmt.Sprintf("being in the server for %d days", st.MinDays))
		} else if days := int(now.Sub(joined).Hours() / 24); days < st.MinDays {
			out = append(out, fmt.Sprintf("**%d more day(s)** in the server (%d days needed)", st.MinDays-days, st.MinDays))
		}
	}
	if e.RankedGames < st.MinGames {
		out = append(out, fmt.Sprintf("**%d more ranked game(s)** (%d of %d played)", st.MinGames-e.RankedGames, e.RankedGames, st.MinGames))
	}
	if e.RankedWins < st.MinWins {
		out = append(out, fmt.Sprintf("**%d more ranked win(s)** (%d of %d)", st.MinWins-e.RankedWins, e.RankedWins, st.MinWins))
	}
	if e.ActiveBans > 0 {
		out = append(out, "no active ban")
	}
	return out
}

// CheckFounder says what the member lacks to found a faction.
func CheckFounder(guildID string, st Settings, member *discordgo.Member) ([]string, error) {
	e, err := database.FactionEligibilityOf(guildID, member.User.ID)
	if err != nil {
		if database.IsNotFound(err) {
			return []string{"being registered (use `/register`)"}, nil
		}
		return nil, err
	}
	return Shortfalls(st, member.JoinedAt, *e, time.Now()), nil
}

// ============================================================================
// Levels and what they unlock
// ============================================================================

// Unlock is a reward a level gives.
type Unlock struct {
	Level int
	Name  string
	Text  string
}

// Unlocks are the rewards, in level order.
var Unlocks = []Unlock{
	{3, "Faction tag", "the tag shows on members' stats cards"},
	{5, "Faction colour", "a colour for the tag and the role"},
	{7, "Discord role", "a role for the faction, made with `/faction role`"},
	{10, "Faction icon", "a symbol for the tag and the role"},
	{15, "Advanced customization", "icons around the tag, image icons, and a second officer"},
	{20, "Elite Faction", "the Elite Faction badge"},
	{30, "Legendary Faction", "Legendary status, the top of the ladder"},
}

// NextUnlock is the next reward above this level, or nil at the top.
func NextUnlock(level int) *Unlock {
	for i := range Unlocks {
		if Unlocks[i].Level > level {
			return &Unlocks[i]
		}
	}
	return nil
}

// UnlockedAt lists the rewards a faction of this level has.
func UnlockedAt(level int) []Unlock {
	var out []Unlock
	for _, u := range Unlocks {
		if u.Level <= level {
			out = append(out, u)
		}
	}
	return out
}

// Bar draws progress as a bar of ten blocks.
func Bar(done, total int) string {
	if total <= 0 {
		return strings.Repeat("▰", 10)
	}
	filled := min(max(done*10/total, 0), 10)
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled)
}

// Level progress: where a faction stands between its level and the next.
func LevelProgress(f *database.Faction) string {
	if f.Level >= 30 || f.NextLevelTrophies == 0 {
		return fmt.Sprintf("Level **%d** (max) · %s trophies", f.Level, Number(f.LifetimeTrophies))
	}
	from := levelStart(f.Level)
	return fmt.Sprintf("Level **%d**\n%s %s / %s trophies", f.Level, Bar(f.LifetimeTrophies-from, f.NextLevelTrophies-from), Number(f.LifetimeTrophies), Number(f.NextLevelTrophies))
}

// levelStart is the lifetime trophies a level starts at (12 × (level − 1)², the Database's curve).
func levelStart(level int) int {
	if level <= 1 {
		return 0
	}
	return 12 * (level - 1) * (level - 1)
}

// Number writes a number with thousands separators.
func Number(n int) string {
	s := fmt.Sprint(n)
	if n < 1000 && n > -1000 {
		return s
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// ============================================================================
// On a player's card
// ============================================================================

type cached struct {
	at time.Time
	f  *database.Faction // nil: in no faction
}

var (
	cardMu    sync.Mutex
	cardCache = map[string]cached{}
)

const cardFor = time.Minute

// CardVars are the faction details for a player's stats card (the variables `faction.tag`, `faction.name`, `faction.level`...), looked up
// once a minute per player. All of them are empty for a player in no faction, and the tag is empty until the faction has unlocked it.
func CardVars(guildID, discordID string) map[string]string {
	key := guildID + "/" + discordID
	cardMu.Lock()
	c, ok := cardCache[key]
	cardMu.Unlock()
	if !ok || time.Since(c.at) > cardFor {
		f, err := database.FactionOf(guildID, discordID)
		if err != nil {
			if ok {
				return varsOf(c.f) // a failed lookup keeps what was known
			}
			return varsOf(nil)
		}
		c = cached{at: time.Now(), f: f}
		cardMu.Lock()
		if len(cardCache) > 5000 {
			clear(cardCache)
		}
		cardCache[key] = c
		cardMu.Unlock()
	}
	return varsOf(c.f)
}

// Forget drops what is remembered of a player's faction (they joined, left or were moved).
func Forget(guildID, discordID string) {
	cardMu.Lock()
	delete(cardCache, guildID+"/"+discordID)
	cardMu.Unlock()
}

func varsOf(f *database.Faction) map[string]string {
	v := map[string]string{"faction.name": "", "faction.tag": "", "faction.level": "", "faction.icon": "", "faction.color": "", "faction.prestige": "", "faction.trophies": "", "faction.role": ""}
	if f == nil {
		return v
	}
	v["faction.name"], v["faction.level"], v["faction.prestige"] = f.Name, fmt.Sprint(f.Level), f.Prestige
	v["faction.tag"] = f.DisplayTag
	v["faction.trophies"] = Number(f.LifetimeTrophies)
	if f.Level >= 10 {
		v["faction.icon"] = f.Icon
	}
	if f.Level >= 5 {
		v["faction.color"] = f.Color
	}
	if f.RoleID != "" {
		v["faction.role"] = f.RoleID
	}
	return v
}
