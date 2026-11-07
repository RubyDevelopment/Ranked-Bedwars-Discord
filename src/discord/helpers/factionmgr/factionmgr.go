// Package factionmgr is what the bot does in Discord for factions: the faction role and the tags in nicknames that follow a faction's level
// and membership, and the trophy announcement after a ranked game.
package factionmgr

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/types"
)

const (
	// CmdName is the command whose alerts channel the faction announcements go to.
	CmdName   = "faction"
	AlertsKey = "faction_alerts"
)

// ColorInt reads "#RRGGBB" as a Discord colour, or 0 when it is not one.
func ColorInt(hex string) int {
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(hex), "#"), 16, 32)
	if err != nil || len(strings.TrimPrefix(hex, "#")) != 6 {
		return 0
	}
	return int(n)
}

// Trophies writes a trophy count the way faction embeds show it: 1,284 🏆.
func Trophies(n int) string { return Thousands(n) + " 🏆" }

// Thousands writes n with commas.
func Thousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// SyncFaction brings Discord in line with a faction: its role (made, renamed and coloured once the role reward is unlocked, then given to
// every member) and its members' nicknames (which carry the tag). It is slow (a call or two per member), so callers run it in a goroutine.
func SyncFaction(guildID string, p *database.FactionProfile) {
	s := base.Session.Load()
	if s == nil || p == nil {
		return
	}
	roleID := p.Faction.RoleID
	if p.Unlocked.Role {
		roleID = ensureRole(s, guildID, p)
	}
	for _, m := range p.Members {
		if roleID != "" {
			if err := helpers.AddMemberRole(s, guildID, m.SnowflakeID, roleID); err != nil {
				log.Printf("[Faction] could not give role %s to %s in %s: %v", roleID, m.SnowflakeID, guildID, err)
			}
		}
		RefreshMember(guildID, m.SnowflakeID)
		time.Sleep(250 * time.Millisecond) // nickname and role edits are rate limited: a big faction must not stall the bot
	}
}

// ensureRole makes the faction's Discord role when it has none (or it was deleted), and keeps its name and colour current.
func ensureRole(s *discordgo.Session, guildID string, p *database.FactionProfile) string {
	name := roleName(p)
	color := ColorInt(p.Color)
	params := &discordgo.RoleParams{Name: name, Color: &color}
	if id := p.Faction.RoleID; id != "" {
		if _, err := s.GuildRoleEdit(guildID, id, params); err == nil {
			return id
		}
		// The role is gone (deleted by hand): make a new one.
	}
	role, err := s.GuildRoleCreate(guildID, params)
	if err != nil {
		log.Printf("[Faction] could not create the role of %s in %s: %v", p.Faction.Name, guildID, err)
		return ""
	}
	if _, err := database.FactionCustomize(guildID, p.Faction.ID, "", true, map[string]any{"role_id": role.ID}); err != nil {
		log.Printf("[Faction] could not store the role of %s in %s: %v", p.Faction.Name, guildID, err)
	}
	return role.ID
}

func roleName(p *database.FactionProfile) string {
	name := p.Faction.Name
	if p.DisplayTag != "" {
		name = p.DisplayTag + " " + name
	}
	if r := []rune(name); len(r) > 100 {
		name = string(r[:100])
	}
	return name
}

// RemoveFrom takes a faction's role and tag away from a member who left, was kicked or whose faction was disbanded.
func RemoveFrom(guildID, userID, roleID string) {
	s := base.Session.Load()
	if s == nil {
		return
	}
	if roleID != "" {
		_ = helpers.RemoveMemberRole(s, guildID, userID, roleID)
	}
	RefreshMember(guildID, userID)
}

// DeleteRole deletes a disbanded faction's role.
func DeleteRole(guildID, roleID string) {
	if s := base.Session.Load(); s != nil && roleID != "" {
		_ = s.GuildRoleDelete(guildID, roleID)
	}
}

// RefreshMember rewrites a member's server nickname, which carries their faction's tag next to their ELO and name.
func RefreshMember(guildID, userID string) {
	s := base.Session.Load()
	if s == nil {
		return
	}
	view, err := database.Must().Player.Get(guildID, userID)
	if err != nil || view == nil || view.Player == nil {
		return
	}
	if _, err := helpers.SyncRegisteredPlayerProfile(s, guildID, view.Player.ID, userID); err != nil {
		log.Printf("[Faction] could not refresh %s in %s: %v", userID, guildID, err)
	}
}

// AfterGame announces what a finished ranked game gave the factions of its players, and brings Discord up to date for any faction that
// levelled up (a new level can unlock the tag, the role and more). It is best effort: the game is already saved.
func AfterGame(guildID string, gameID uint) {
	s := base.Session.Load()
	if s == nil {
		return
	}
	earned, err := database.FactionGameTrophiesFor(guildID, gameID)
	if err != nil {
		log.Printf("[Faction] trophies of game %d in %s: %v", gameID, guildID, err)
		return
	}
	for _, f := range earned {
		leveled := f.LevelAfter > f.LevelBefore
		if leveled && len(f.Players) > 0 {
			if p, err := database.FactionFor(guildID, f.Players[0].SnowflakeID); err == nil && p != nil {
				go SyncFaction(guildID, p)
			}
		}
		msg := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{TrophiesEmbed(f)}, AllowedMentions: &discordgo.MessageAllowedMentions{}}
		if leveled {
			msg.Embeds = append(msg.Embeds, LevelUpEmbed(f))
		}
		if err := helpers.SendCommandChannelMessage(s, guildID, CmdName, AlertsKey, msg); err != nil && err != helpers.ErrRequirementNotConfigured {
			log.Printf("[Faction] could not announce trophies in %s: %v", guildID, err)
		}
	}
}

// TrophiesEmbed lists who earned a faction its trophies from one game.
func TrophiesEmbed(f database.FactionGameTrophies) *discordgo.MessageEmbed {
	var b strings.Builder
	for _, p := range f.Players {
		fmt.Fprintf(&b, "<@%s> — %s", p.SnowflakeID, Trophies(p.Trophies))
		if p.Mvp {
			b.WriteString(" · MVP")
		}
		b.WriteString("\n")
	}
	name := f.Name
	if f.DisplayTag != "" {
		name = f.DisplayTag + " " + name
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Faction Trophies Earned",
		Description: fmt.Sprintf("**%s** earned %s from a ranked win.\n\n%s\n> **Lifetime Trophies:** %s\n> **Level:** %d", name, Trophies(f.Trophies), b.String(), Trophies(f.Lifetime), f.LevelAfter),
		Color:       ColorInt(f.Color),
		Thumbnail:   helpers.ThumbTada,
	})
}

// LevelUpEmbed announces a faction's new level.
func LevelUpEmbed(f database.FactionGameTrophies) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Faction Level Up",
		Description: fmt.Sprintf("**%s** reached level **%d**! Use `/faction info` to see what has unlocked.", f.Name, f.LevelAfter),
		Color:       ColorInt(f.Color),
		Thumbnail:   helpers.ThumbTada,
	})
}

// BannerVars are the {faction.*} variables of a player's stats banner: empty for a player with no faction, so a banner that shows the tag
// simply shows nothing for them. faction.tag is the tag as written beside the player (empty until the tag reward is unlocked).
func BannerVars(guildID, userID string) map[string]string {
	vars := map[string]string{"faction.name": "", "faction.tag": "", "faction.color": "", "faction.icon": "", "faction.level": "", "faction.role": "", "faction.prestige": ""}
	b, err := database.FactionBriefFor(guildID, userID)
	if err != nil || b == nil {
		return vars
	}
	vars["faction.name"], vars["faction.tag"], vars["faction.color"], vars["faction.icon"] = b.Name, b.DisplayTag, b.Color, b.Icon
	vars["faction.level"], vars["faction.role"], vars["faction.prestige"] = strconv.Itoa(b.Level), b.Role, b.Prestige
	return vars
}
