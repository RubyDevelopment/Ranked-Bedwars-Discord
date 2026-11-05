package factions

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
)

// From level 7 a faction can have a Discord role. The founder makes it with /faction role; from then on joining gives it, leaving or being
// kicked takes it, and a new colour (level 5) or name is applied to it.

const roleLevel = 7

// colorInt reads a faction colour (#RRGGBB) for a role; 0 means none.
func colorInt(color string) int {
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(color), "#"), 16, 32)
	if err != nil {
		return 0
	}
	return int(n)
}

// EnsureRole makes the faction's role (when it has none), brings its name and colour up to date, and gives it to every member. It
// returns the faction with its role id.
func EnsureRole(s *discordgo.Session, guildID string, f *database.Faction) (*database.Faction, error) {
	if f.Level < roleLevel {
		return f, fmt.Errorf("a Discord role unlocks at faction level %d", roleLevel)
	}
	color := 0
	if f.Level >= 5 {
		color = colorInt(f.Color)
	}
	mentionable := true
	if f.RoleID == "" {
		role, err := s.GuildRoleCreate(guildID, &discordgo.RoleParams{Name: f.Name, Color: &color, Mentionable: &mentionable})
		if err != nil {
			return f, fmt.Errorf("could not make the role (the bot needs Manage Roles): %w", err)
		}
		updated, err := database.UpdateFaction(guildID, f.ID, map[string]any{"role_id": role.ID})
		if err != nil {
			_ = s.GuildRoleDelete(guildID, role.ID)
			return f, err
		}
		f.RoleID = updated.RoleID
	} else if _, err := s.GuildRoleEdit(guildID, f.RoleID, &discordgo.RoleParams{Name: f.Name, Color: &color}); err != nil {
		return f, fmt.Errorf("could not update the role: %w", err)
	}
	for _, m := range f.MemberList {
		if m.SnowflakeID != "" {
			_ = s.GuildMemberRoleAdd(guildID, m.SnowflakeID, f.RoleID)
		}
	}
	return f, nil
}

// GiveRole gives a new member the faction's role, if it has one.
func GiveRole(s *discordgo.Session, guildID string, f *database.Faction, discordID string) {
	if f != nil && f.RoleID != "" {
		_ = s.GuildMemberRoleAdd(guildID, discordID, f.RoleID)
	}
}

// TakeRole takes the faction's role from a member who left or was kicked.
func TakeRole(s *discordgo.Session, guildID string, f *database.Faction, discordID string) {
	if f != nil && f.RoleID != "" {
		_ = s.GuildMemberRoleRemove(guildID, discordID, f.RoleID)
	}
}

// DeleteRole removes the role of a faction that was disbanded.
func DeleteRole(s *discordgo.Session, guildID string, f *database.Faction) {
	if f != nil && f.RoleID != "" {
		_ = s.GuildRoleDelete(guildID, f.RoleID)
	}
}
