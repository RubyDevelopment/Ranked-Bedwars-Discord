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
	helpers.Register(ServerVcMuteCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerVcMuteCommand server-mutes a player in voice channels.
var ServerVcMuteCommand = &types.Command{
	Name:         "vcmute",
	Description:  "Server-mutes a player in voice channels.",
	Requirements: []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}, {Type: types.ReqRole, Key: "vc_muted_role"}},
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to voice mute",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the voice mute",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
			{
				Name:        "duration",
				Description: "How long the mute lasts (e.g. 30m, 1d, perm)",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeServerVcMuteCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeServerVcMuteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.ApplyPunishment(s, i, &helpers.ApplySpec{
		Command:       "server_vc_mute",
		RoleKey:       "vc_muted_role",
		Action:        "voice mute",
		Adjective:     "voice muted",
		Summary:       "the voice mute",
		Title:         "Voice Mute Applied",
		Type:          "vc_mute",
		ActiveTypes:   helpers.VoiceMutePunishmentTypes(),
		Duration:      true,
		Duplicates:    true,
		RequireMember: true,
		MemberActive:  func(member *discordgo.Member) bool { return member.Mute },
		Discord:       voiceMuteMember,
		AppliedNote:   "The member is now server-muted in voice channels.",
		FailedNote:    "The punishment was recorded, but the Discord voice mute could not be applied due to missing permissions.",
	})
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// voiceMuteMember server-mutes the member in voice channels.
func voiceMuteMember(s *discordgo.Session, guildID, targetID, reason string) bool {
	return s.GuildMemberMute(guildID, targetID, true) == nil
}
