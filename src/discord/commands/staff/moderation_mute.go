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
	helpers.Register(ModerationMuteCommand)
	helpers.RegisterLadderAction("chat_mute", muteSpec)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationMuteCommand mutes a player in text chat by recording a mute restriction.
var ModerationMuteCommand = &types.Command{
	Name:         "mute",
	Requirements: types.PunishmentRequirements("chat_mute_logs", "chat_muted_role"),
	Description:  "Mutes a player in text chat across the guild.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: helpers.PunishmentOptions("Discord user to chat mute", "Reason for the chat mute", true, true, true),
	},
	Autocomplete: helpers.PunishmentLevelAutocomplete("chat_mute"),
	Execute:      executeModerationMuteCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

var muteSpec = &helpers.ApplySpec{
	Command:     "mute",
	RoleKey:     "chat_muted_role",
	Action:      "chat mute",
	Adjective:   "chat muted",
	Summary:     "the chat mute",
	Title:       "Chat Mute Applied",
	Type:        "mute",
	Duration:    true,
	Duplicates:  true,
	AppliedNote: "The chat muted role was given to the member.",
	FailedNote:  "The punishment was recorded, but no chat muted role is set (or it could not be given). Choose one in the dashboard under Moderation > Punishment ladders.",
	Ladder:      "chat_mute",
}

func executeModerationMuteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.ApplyPunishment(s, i, muteSpec)
}
