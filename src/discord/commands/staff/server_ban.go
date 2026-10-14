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
	helpers.Register(ServerBanCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerBanCommand bans a player from the Discord guild and records the punishment.
var ServerBanCommand = &types.Command{
	Name:         "ban",
	Description:  "Bans a player from the Discord guild.",
	Requirements: []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}},
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to ban",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the ban",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
			{
				Name:        "duration",
				Description: "How long the ban lasts (e.g. 1h, 1d, perm)",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeServerBanCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeServerBanCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.ApplyPunishment(s, i, &helpers.ApplySpec{
		Command:     "server_ban",
		Action:      "server ban",
		Adjective:   "server banned",
		Summary:     "the server ban",
		Title:       "Server Ban Applied",
		Type:        "server_ban",
		Duration:    true,
		Duplicates:  true,
		Discord:     banMember,
		AppliedNote: "The member was banned from the Discord guild.",
		FailedNote:  "The punishment was recorded, but the Discord ban could not be applied due to missing permissions or hierarchy.",
	})
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// banMember bans the user from the guild when the bot is allowed to.
func banMember(s *discordgo.Session, guildID, targetID, reason string) bool {
	botMember, _ := helpers.FetchGuildMember(s, guildID, s.State.User.ID)
	if botMember == nil {
		return false
	}
	perms, err := helpers.MemberGuildPermissions(s, guildID, botMember)
	if err != nil || perms&discordgo.PermissionBanMembers == 0 {
		return false
	}
	return s.GuildBanCreateWithReason(guildID, targetID, reason, 0) == nil
}
