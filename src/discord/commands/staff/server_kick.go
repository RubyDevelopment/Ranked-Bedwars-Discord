package staff

import (
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ServerKickCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerKickCommand records a kick and removes the member from the Discord guild.
var ServerKickCommand = &types.Command{
	Name:         "kick",
	Description:  "Kicks a member from the Discord guild.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Requirements: append(types.ModerationRequirements, types.Requirement{Type: types.ReqRole, Key: "staff_role", Optional: true}),
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to kick",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the kick",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
		},
	},
	Execute: executeServerKickCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeServerKickCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.ApplyPunishment(s, i, &helpers.ApplySpec{
		Command:       "server_kick",
		Action:        "kick",
		Adjective:     "kicked",
		Summary:       "the kick",
		Title:         "Kick Applied",
		Type:          "kick",
		SkipHierarchy: true,
		Discord:       kickMember,
		AppliedNote:   "The member was removed from the Discord guild.",
		FailedNote:    "The kick was recorded, but the member could not be removed from Discord due to missing permissions or hierarchy.",
	})
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// kickMember removes the member from the guild when the bot is allowed to.
func kickMember(s *discordgo.Session, guildID, targetID, reason string) bool {
	member, _ := helpers.FetchGuildMember(s, guildID, targetID)
	botMember, _ := helpers.FetchGuildMember(s, guildID, s.State.User.ID)
	if member == nil || botMember == nil {
		return false
	}
	perms, err := helpers.MemberGuildPermissions(s, guildID, botMember)
	if err != nil || perms&discordgo.PermissionKickMembers == 0 {
		return false
	}
	return s.GuildMemberDeleteWithReason(guildID, targetID, reason) == nil
}
