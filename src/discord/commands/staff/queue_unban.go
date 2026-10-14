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
	helpers.Register(QueueUnbanCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// QueueUnbanCommand removes a player's active queue ban.
var QueueUnbanCommand = &types.Command{
	Name:          "queueunban",
	Description:   "Removes a queue ban from a player.",
	Requirements:  []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}},
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
	Execute: executeQueueUnbanCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeQueueUnbanCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.RevokePunishment(s, i, &helpers.RevokeSpec{
		Command:   "queue_ban", // Use queue_ban for shared alerts
		RoleKey:   "queue_banned_role",
		Action:    "Queue Ban",
		Adjective: "queue banned",
		Type:      "ban",
	})
}
