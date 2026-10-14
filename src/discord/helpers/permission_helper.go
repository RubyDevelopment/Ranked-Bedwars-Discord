package helpers

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

// MemberIsAdministrator reports whether the interaction member can manage guild configuration.
func MemberIsAdministrator(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i == nil || i.Member == nil {
		return false
	}
	perms, err := MemberGuildPermissions(s, i.GuildID, i.Member)
	if err != nil {
		return false
	}
	return perms&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild) != 0
}

// MemberGuildPermissions returns the guild permissions for a member.
func MemberGuildPermissions(s *discordgo.Session, guildID string, member *discordgo.Member) (int64, error) {
	if s == nil || member == nil || member.User == nil || guildID == "" {
		return 0, nil
	}

	// Get guild to check owner
	guild, err := s.Guild(guildID)
	if err != nil || guild == nil {
		return 0, err
	}

	// Owner has all permissions
	if guild.OwnerID == member.User.ID {
		return discordgo.PermissionAll, nil
	}

	// Calculate permissions from roles
	permissions := int64(0)
	for _, roleID := range member.Roles {
		role, err := s.State.Role(guildID, roleID)
		if err != nil {
			// Try to fetch role if not in state
			roles, err := s.GuildRoles(guildID)
			if err != nil {
				continue
			}
			for _, r := range roles {
				if r.ID == roleID {
					role = r
					break
				}
			}
			if role == nil {
				continue
			}
		}
		permissions |= role.Permissions
	}

	// @everyone role permissions
	if everyoneRole, err := s.State.Role(guildID, guildID); err == nil && everyoneRole != nil {
		permissions |= everyoneRole.Permissions
	}

	return permissions, nil
}

// DashboardStaff is defined in package types (permissions.go).
type DashboardStaff = types.DashboardStaff

// GuildStaff lists the members whose Discord permissions give them the full
// dashboard (owner, Administrator, Manage Server). roles is the guild's role
// list; the member list is paged, so a very large guild costs one request per
// 1000 members.
func GuildStaff(s *discordgo.Session, guildID string, roles []*discordgo.Role) ([]DashboardStaff, error) {
	guild, err := s.Guild(guildID)
	if err != nil {
		return nil, err
	}
	perms := make(map[string]int64, len(roles))
	for _, r := range roles {
		if r != nil {
			perms[r.ID] = r.Permissions
		}
	}

	staff := []DashboardStaff{}
	after := ""
	for {
		page, err := s.GuildMembers(guildID, after, 1000)
		if err != nil {
			return nil, err
		}
		for _, m := range page {
			if m == nil || m.User == nil || m.User.Bot {
				continue
			}
			via := ""
			if m.User.ID == guild.OwnerID {
				via = "Owner"
			} else {
				p := perms[guildID] // @everyone
				for _, id := range m.Roles {
					p |= perms[id]
				}
				if p&discordgo.PermissionAdministrator != 0 {
					via = "Administrator"
				} else if p&discordgo.PermissionManageGuild != 0 {
					via = "Manage Server"
				}
			}
			if via == "" {
				continue
			}
			name := m.Nick
			if name == "" {
				name = m.User.GlobalName
			}
			if name == "" {
				name = m.User.Username
			}
			staff = append(staff, DashboardStaff{ID: m.User.ID, Name: name, AvatarURL: m.User.AvatarURL("64"), Via: via})
		}
		if len(page) < 1000 {
			return staff, nil
		}
		after = page[len(page)-1].User.ID
	}
}
