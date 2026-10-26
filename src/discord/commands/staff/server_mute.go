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
	helpers.Register(ServerMuteCommand)
	helpers.RegisterLadderAction("chat_mute", muteSpec)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerMuteCommand mutes a player in text chat by recording a mute restriction.
var ServerMuteCommand = &types.Command{
	Name:         "mute",
	Requirements: types.PunishmentRequirements("chat_mute_logs", "chat_muted_role"),
	Description:  "Mutes a player in text chat across the guild.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to chat mute",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the chat mute",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
		},
	},
	Execute: executeServerMuteCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

var muteSpec = &helpers.ApplySpec{
	Command:    "server_mute",
	RoleKey:    "chat_muted_role",
	Action:     "chat mute",
	Adjective:  "chat muted",
	Summary:    "the chat mute",
	Title:      "Chat Mute Applied",
	Type:       "mute",
	Duration:   true,
	Duplicates: true,
	Ladder:     "chat_mute",
}

func executeServerMuteCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.ApplyPunishment(s, i, muteSpec)
}
