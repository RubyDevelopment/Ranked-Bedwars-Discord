package general

import (
	"fmt"
	"strings"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers/factionmgr"
)

// Small helpers the faction embeds share.

// factionColor is the colour of an embed about a faction: its own, or 0 (the preset's) when it has none.
func factionColor(p *database.FactionProfile) int { return factionmgr.ColorInt(p.Color) }

// factionHeading is a faction's name with its tag in front, as members see it.
func factionHeading(name, tag string) string {
	if tag == "" {
		return name
	}
	return tag + " " + name
}

func progressBar(have, from, to int) string {
	if to <= from {
		return "▰▰▰▰▰▰▰▰▰▰"
	}
	filled := min(max((have-from)*10/(to-from), 0), 10)
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled)
}

// factionNextUnlock names the next cosmetic reward the faction has not reached yet, or "".
func factionNextUnlock(p *database.FactionProfile, cfg *database.FactionConfig) string {
	if cfg == nil {
		return ""
	}
	next, nextName := 0, ""
	for _, r := range []struct {
		at   int
		name string
	}{{cfg.Unlocks.Tag, "Faction Tag"}, {cfg.Unlocks.Color, "Faction Colour"}, {cfg.Unlocks.Role, "Discord Faction Role"}, {cfg.Unlocks.Icon, "Faction Icon"},
		{cfg.Unlocks.Advanced, "Advanced Customization"}, {cfg.Unlocks.Prestige, "Prestige Badge"}, {cfg.Unlocks.Legendary, "Legendary Status"}} {
		if r.at > p.Level && (next == 0 || r.at < next) {
			next, nextName = r.at, r.name
		}
	}
	if next == 0 {
		return ""
	}
	return fmt.Sprintf("**%s** at level %d", nextName, next)
}
