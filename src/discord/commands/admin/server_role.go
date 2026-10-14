package admin

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

var (
	discordUserMentionPattern = regexp.MustCompile(`<@!?(\d{17,20})>`)
	discordSnowflakePattern   = regexp.MustCompile(`^\d{17,20}$`)
)

func init() {
	helpers.Register(ServerRoleCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerRoleCommand Configures automated role assignments, permissions, and synchronization rules.
var ServerRoleCommand = &types.Command{
	Name:        "role",
	Description: "Configures automated role assignments, permissions, and synchronization rules.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.ManageRolesPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "add",
				Description: "Assign a specific role to an individual user",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "Target user", Type: discordgo.ApplicationCommandOptionUser, Required: true},
					{Name: "role", Description: "Role to assign", Type: discordgo.ApplicationCommandOptionRole, Required: true},
				},
			},
			{
				Name:        "remove",
				Description: "Remove a specific role from an individual user",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "Target user", Type: discordgo.ApplicationCommandOptionUser, Required: true},
					{Name: "role", Description: "Role to remove", Type: discordgo.ApplicationCommandOptionRole, Required: true},
				},
			},
			{
				Name:        "list",
				Description: "List roles assigned to a member",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "The member to inspect (defaults to self)", Type: discordgo.ApplicationCommandOptionUser, Required: false},
				},
			},
			{
				Name:        "mass_add",
				Description: "Assign a role to multiple users or a role group",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "role", Description: "Role to assign", Type: discordgo.ApplicationCommandOptionRole, Required: true},
					{Name: "members", Description: "Mentions or IDs separated by spaces", Type: discordgo.ApplicationCommandOptionString, Required: false},
					{Name: "target_role", Description: "Mass assign to all members currently holding this role", Type: discordgo.ApplicationCommandOptionRole, Required: false},
				},
			},
			{
				Name:        "mass_remove",
				Description: "Remove a role from multiple users or a role group",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "role", Description: "Role to strip", Type: discordgo.ApplicationCommandOptionRole, Required: true},
					{Name: "members", Description: "Mentions or IDs separated by spaces", Type: discordgo.ApplicationCommandOptionString, Required: false},
					{Name: "target_role", Description: "Mass remove from all members currently holding this role", Type: discordgo.ApplicationCommandOptionRole, Required: false},
				},
			},
			{
				Name:        "add_by_role",
				Description: "Conditionally add a secondary role to everyone who has a source role",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "source_role", Description: "Prerequisite role filter", Type: discordgo.ApplicationCommandOptionRole, Required: true},
					{Name: "role_to_add", Description: "Role to grant", Type: discordgo.ApplicationCommandOptionRole, Required: true},
				},
			},
			{
				Name:        "remove_by_role",
				Description: "Conditionally remove a role from everyone who has a source role",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "source_role", Description: "Prerequisite role filter", Type: discordgo.ApplicationCommandOptionRole, Required: true},
					{Name: "role_to_remove", Description: "Role to strip", Type: discordgo.ApplicationCommandOptionRole, Required: true},
				},
			},
			{
				Name:        "sync",
				Description: "Synchronize verified player roles across the server",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	},
	Execute: executeServerRoleCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeServerRoleCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	subcommand, options := types.SelectedSubcommand(i)

	switch subcommand {
	case "add":
		target := helpers.SubOptionUser(s, options, "user")
		roleID := helpers.SubOptionRoleID(s, options, "role")
		return mutateMemberRole(s, i, target, roleID, true)

	case "remove":
		target := helpers.SubOptionUser(s, options, "user")
		roleID := helpers.SubOptionRoleID(s, options, "role")
		return mutateMemberRole(s, i, target, roleID, false)

	case "list":
		target := helpers.SubOptionUser(s, options, "user")
		if target == nil {
			if i.Member != nil && i.Member.User != nil {
				target = i.Member.User
			} else if i.User != nil {
				target = i.User
			}
		}
		if target == nil {
			return helpers.EditInteractionEmbed(s, i, roleInvalidTargetEmbed())
		}

		member, err := helpers.FetchGuildMember(s, i.GuildID, target.ID)
		if err != nil || member == nil {
			return helpers.EditInteractionEmbed(s, i, roleMemberNotFoundEmbed())
		}

		roleNames := make([]string, 0, len(member.Roles))
		for _, id := range member.Roles {
			role, err := s.State.Role(i.GuildID, id)
			if err != nil || role == nil || role.Name == "@everyone" {
				continue
			}
			roleNames = append(roleNames, helpers.FormatRoleMention(role.ID))
		}

		if len(roleNames) == 0 {
			return helpers.EditInteractionEmbed(s, i, roleListEmptyEmbed(target))
		}
		return helpers.EditInteractionEmbed(s, i, roleListEmbed(target, strings.Join(roleNames, ", ")))

	case "mass_add":
		roleID := helpers.SubOptionRoleID(s, options, "role")
		targetRoleID := helpers.SubOptionRoleID(s, options, "target_role")
		membersInput := helpers.SubOptionString(options, "members")

		memberIDs := resolveMassMemberIDs(s, i.GuildID, targetRoleID, membersInput)
		if len(memberIDs) == 0 {
			return helpers.EditInteractionEmbed(s, i, roleNoMembersEmbed())
		}
		return executeRoleMassMutation(s, i, roleID, memberIDs, true)

	case "mass_remove":
		roleID := helpers.SubOptionRoleID(s, options, "role")
		targetRoleID := helpers.SubOptionRoleID(s, options, "target_role")
		membersInput := helpers.SubOptionString(options, "members")

		memberIDs := resolveMassMemberIDs(s, i.GuildID, targetRoleID, membersInput)
		if len(memberIDs) == 0 {
			return helpers.EditInteractionEmbed(s, i, roleNoMembersEmbed())
		}
		return executeRoleMassMutation(s, i, roleID, memberIDs, false)

	case "add_by_role":
		sourceRoleID := helpers.SubOptionRoleID(s, options, "source_role")
		roleToAddID := helpers.SubOptionRoleID(s, options, "role_to_add")
		memberIDs := resolveMassMemberIDs(s, i.GuildID, sourceRoleID, "")
		if len(memberIDs) == 0 {
			return helpers.EditInteractionEmbed(s, i, roleNoMembersEmbed())
		}
		return executeRoleMassMutation(s, i, roleToAddID, memberIDs, true)

	case "remove_by_role":
		sourceRoleID := helpers.SubOptionRoleID(s, options, "source_role")
		roleToRemoveID := helpers.SubOptionRoleID(s, options, "role_to_remove")
		memberIDs := resolveMassMemberIDs(s, i.GuildID, sourceRoleID, "")
		if len(memberIDs) == 0 {
			return helpers.EditInteractionEmbed(s, i, roleNoMembersEmbed())
		}
		return executeRoleMassMutation(s, i, roleToRemoveID, memberIDs, false)

	case "sync":
		return helpers.EditInteractionEmbed(s, i, roleSyncEmbed())
	}

	return nil
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

func mutateMemberRole(s *discordgo.Session, i *discordgo.InteractionCreate, target *discordgo.User, roleID string, add bool) error {
	if target == nil || target.Bot {
		return helpers.EditInteractionEmbed(s, i, roleInvalidTargetEmbed())
	}

	role, issue, err := validateRoleMutation(s, i, roleID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorRoleEmbed())
	}
	if issue != helpers.RoleHierarchyOK {
		return helpers.EditInteractionEmbed(s, i, roleHierarchyEmbed(issue))
	}

	member, err := helpers.FetchGuildMember(s, i.GuildID, target.ID)
	if err != nil || member == nil {
		return helpers.EditInteractionEmbed(s, i, roleMemberNotFoundEmbed())
	}

	botMember, _ := helpers.FetchGuildMember(s, i.GuildID, s.State.User.ID)
	if memberIssue := helpers.MemberManageabilityIssue(s, i.GuildID, member, botMember); memberIssue != helpers.RoleHierarchyOK {
		return helpers.EditInteractionEmbed(s, i, helpers.PunishmentHierarchyEmbed())
	}

	hasRole := helpers.MemberHasRole(member, role.ID)
	if add {
		if hasRole {
			return helpers.EditInteractionEmbed(s, i, roleAlreadyAssignedEmbed(target, role.Name))
		}
		if err := helpers.AddMemberRole(s, i.GuildID, target.ID, role.ID); err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorRoleEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, roleUpdatedEmbed("added", target, role.Name))
	}

	if !hasRole {
		return helpers.EditInteractionEmbed(s, i, roleNotAssignedEmbed(target, role.Name))
	}
	if err := helpers.RemoveMemberRole(s, i.GuildID, target.ID, role.ID); err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorRoleEmbed())
	}
	return helpers.EditInteractionEmbed(s, i, roleUpdatedEmbed("removed", target, role.Name))
}

func executeRoleMassMutation(s *discordgo.Session, i *discordgo.InteractionCreate, roleID string, memberIDs []string, add bool) error {
	role, issue, err := validateRoleMutation(s, i, roleID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorRoleEmbed())
	}
	if issue != helpers.RoleHierarchyOK {
		return helpers.EditInteractionEmbed(s, i, roleHierarchyEmbed(issue))
	}

	botMember, _ := helpers.FetchGuildMember(s, i.GuildID, s.State.User.ID)
	var success, skipped int

	for _, userID := range memberIDs {
		member, err := helpers.FetchGuildMember(s, i.GuildID, userID)
		if err != nil || member == nil {
			skipped++
			continue
		}
		if helpers.MemberManageabilityIssue(s, i.GuildID, member, botMember) != helpers.RoleHierarchyOK {
			skipped++
			continue
		}

		var actionErr error
		if add {
			if helpers.MemberHasRole(member, role.ID) {
				skipped++
				continue
			}
			actionErr = helpers.AddMemberRole(s, i.GuildID, userID, role.ID)
		} else {
			if !helpers.MemberHasRole(member, role.ID) {
				skipped++
				continue
			}
			actionErr = helpers.RemoveMemberRole(s, i.GuildID, userID, role.ID)
		}

		if actionErr != nil {
			skipped++
		} else {
			success++
		}
	}

	action := "added to"
	if !add {
		action = "removed from"
	}
	return helpers.EditInteractionEmbed(s, i, roleMassResultEmbed(role.Name, action, success, skipped))
}

func resolveMassMemberIDs(s *discordgo.Session, guildID string, targetRoleID string, membersInput string) []string {
	seen := make(map[string]struct{})
	var ids []string

	if targetRoleID != "" {
		members, err := s.GuildMembers(guildID, "", 1000)
		if err == nil {
			for _, m := range members {
				if helpers.MemberHasRole(m, targetRoleID) {
					if _, exists := seen[m.User.ID]; !exists {
						seen[m.User.ID] = struct{}{}
						ids = append(ids, m.User.ID)
					}
				}
			}
		}
	}

	if membersInput != "" {
		parsedIDs := parseDiscordUserIDs(membersInput)
		for _, id := range parsedIDs {
			if _, exists := seen[id]; !exists {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}

	return ids
}

func validateRoleMutation(s *discordgo.Session, i *discordgo.InteractionCreate, roleID string) (*discordgo.Role, helpers.RoleHierarchyIssue, error) {
	if roleID == "" {
		return nil, helpers.RoleHierarchyBot, nil
	}
	role, err := s.State.Role(i.GuildID, roleID)
	if err != nil || role == nil {
		return nil, helpers.RoleHierarchyBot, fmt.Errorf("role lookup failed: %w", err)
	}

	executor, _ := helpers.FetchGuildMember(s, i.GuildID, helpers.InteractionUserID(i))
	botMember, _ := helpers.FetchGuildMember(s, i.GuildID, s.State.User.ID)
	issue := helpers.GetRoleManageabilityIssue(s, i.GuildID, role, executor, botMember)
	return role, issue, nil
}

func parseDiscordUserIDs(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	seen := make(map[string]struct{})
	ids := make([]string, 0)

	for _, match := range discordUserMentionPattern.FindAllStringSubmatch(input, -1) {
		if len(match) < 2 {
			continue
		}
		if _, ok := seen[match[1]]; !ok {
			seen[match[1]] = struct{}{}
			ids = append(ids, match[1])
		}
	}

	for _, token := range strings.Fields(input) {
		token = strings.Trim(token, ",;")
		if !discordSnowflakePattern.MatchString(token) {
			continue
		}
		if _, ok := seen[token]; !ok {
			seen[token] = struct{}{}
			ids = append(ids, token)
		}
	}

	return ids
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorRoleEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while modifying member roles. Please try again later or contact support.",
	})
}

func roleInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "That member could not be updated. Verify the user is in this server and try again.",
	})
}

func roleMemberNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: "That member is not in this server. They may have left or been removed.",
	})
}

func roleNoMembersEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Members Found",
		Description: "You must provide at least one valid user mention, snowflake ID, or target role filter.",
	})
}

func roleAlreadyAssignedEmbed(user *discordgo.User, roleName string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Role Already Assigned",
		Description: fmt.Sprintf("<@%s> already has the **%s** role. No further action is needed.", user.ID, roleName),
	})
}

func roleNotAssignedEmbed(user *discordgo.User, roleName string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Role Not Assigned",
		Description: fmt.Sprintf("<@%s> does not currently have the **%s** role. Use `/server_role list` to view their current roles.", user.ID, roleName),
	})
}

func roleUpdatedEmbed(action string, user *discordgo.User, roleName string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Role Updated",
		Description: fmt.Sprintf(
			"Successfully updated the member's roles. Changes take effect immediately.\n\n> **Role:** `%s`\n> **Action:** %s\n> **Member:** <@%s>",
			roleName, action, user.ID,
		),
	})
}

func roleMassResultEmbed(roleName, action string, success, skipped int) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Bulk Role Update",
		Description: fmt.Sprintf(
			"Completed the bulk role operation. Skipped members may lack permissions or have hierarchy conflicts.\n\n> **Role:** `%s`\n> **Action:** %s\n> **Updated:** `%d` member(s)\n> **Skipped:** `%d`",
			roleName, action, success, skipped,
		),
	})
}

func roleListEmbed(user *discordgo.User, roles string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("Roles — %s", user.Username),
		Description: roles + "\n\nUse `/server_role add` or `/server_role remove` to manage roles.",
	})
}

func roleListEmptyEmbed(user *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       fmt.Sprintf("Roles — %s", user.Username),
		Description: "It looks like that member has no assignable roles. Roles may be managed by integrations or higher hierarchy.",
	})
}

func roleSyncEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Role Synchronization Started",
		Description: "Queued background role synchronization for verified server members.",
	})
}

func roleHierarchyEmbed(issue helpers.RoleHierarchyIssue) *discordgo.MessageEmbed {
	description := "That role cannot be managed due to hierarchy or permissions."
	switch issue {
	case helpers.RoleHierarchyManaged:
		description = "That role is managed by an integration and cannot be assigned manually."
	case helpers.RoleHierarchyDangerous:
		description = "Administrator roles cannot be assigned through this command."
	case helpers.RoleHierarchyExecutor:
		description = "Your highest role must be above the target role."
	case helpers.RoleHierarchyBot:
		description = "My highest role must be above the target role."
	}
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Role Not Manageable",
		Description: description + " Adjust the role hierarchy to resolve this.",
	})
}
