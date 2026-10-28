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
	helpers.Register(ModerationVcMuteCommand)
	helpers.RegisterLadderAction("vc_mute", vcMuteSpec)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationVcMuteCommand server-mutes a player in voice channels.
var ModerationVcMuteCommand = &types.Command{
	Name:         "vcmute",
	Requirements: types.PunishmentRequirements("vc_mute_logs", "vc_muted_role"),
	Description:  "Server-mutes a player in voice channels.",
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
			helpers.PunishmentLevelOption(),
		},
	},
	Autocomplete: helpers.PunishmentLevelAutocomplete("vc_mute"),
	Execute: executeModerationVcMuteCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

var vcMuteSpec = &helpers.ApplySpec{
	Command:       "vcmute",
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
	FailedNote:    "The punishment was recorded, but the Discord voice mute could not be applied (the member may not be in a voice channel, and no voice muted role may be set). Choose one in the dashboard under Moderation > Punishment ladders.",
	Ladder:        "vc_mute",
}

func executeModerationVcMuteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.ApplyPunishment(s, i, vcMuteSpec)
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// voiceMuteMember server-mutes the member in voice channels.
func voiceMuteMember(s *discordgo.Session, guildID, targetID, reason string) bool {
	return s.GuildMemberMute(guildID, targetID, true) == nil
}
