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
	helpers.Register(ModerationQueueUnbanCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationQueueUnbanCommand removes a player's active queue ban.
var ModerationQueueUnbanCommand = &types.Command{
	Name:          "queueunban",
	Requirements:  types.PunishmentRequirements("queue_ban_logs", "queue_banned_role"),
	Description:   "Removes a queue ban from a player.",
	PrefixAliases: []string{"qunban"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to queue unban",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the queue unban",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeModerationQueueUnbanCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeModerationQueueUnbanCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.RevokePunishment(s, i, &helpers.RevokeSpec{
		Command:   "queueban",
		Ladder:    "queue_ban",
		RoleKey:   "queue_banned_role",
		Action:    "Queue Ban",
		Adjective: "queue banned",
		Type:      "ban",
	})
}
