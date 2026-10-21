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
	helpers.Register(ServerVcUnmuteCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerVcUnmuteCommand removes a player's voice server-mute.
var ServerVcUnmuteCommand = &types.Command{
	Name:         "vcunmute",
	Requirements: types.PunishmentRequirements("vc_mute_logs", "vc_muted_role"),
	Description:  "Removes a voice channel server-mute from a player.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to voice unmute",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the voice unmute",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeServerVcUnmuteCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeServerVcUnmuteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.RevokePunishment(s, i, &helpers.RevokeSpec{
		Command:       "server_vc_mute", // Use server_vc_mute for shared alerts
		RoleKey:       "vc_muted_role",
		Action:        "Voice Mute",
		Adjective:     "voice muted",
		Type:          "vc_mute",
		ActiveTypes:   helpers.VoiceMutePunishmentTypes(),
		RecordType:    "vc_unmute",
		DiscordActive: isVoiceMuted,
		Discord:       voiceUnmuteMember,
	})
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// isVoiceMuted reports whether the member is currently server-muted in Discord.
func isVoiceMuted(s *discordgo.Session, guildID, targetID string) bool {
	member, _ := helpers.FetchGuildMember(s, guildID, targetID)
	return member != nil && member.Mute
}

// voiceUnmuteMember lifts the member's server mute when they are in the guild.
func voiceUnmuteMember(s *discordgo.Session, guildID, targetID, reason string) bool {
	member, _ := helpers.FetchGuildMember(s, guildID, targetID)
	if member == nil {
		return false
	}
	return s.GuildMemberMute(guildID, targetID, false) == nil
}
