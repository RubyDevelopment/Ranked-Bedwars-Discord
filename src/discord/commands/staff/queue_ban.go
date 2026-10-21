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
	helpers.Register(QueueBanCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// QueueBanCommand bans a player from joining queues by recording a ban restriction.
var QueueBanCommand = &types.Command{
	Name:          "queueban",
	Requirements:  types.PunishmentRequirements("queue_ban_logs", "queue_banned_role"),
	Description:   "Bans a disruptive player from joining queues.",
	PrefixAliases: []string{"qban"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to queue ban",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the queue ban",
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
	Execute: executeQueueBanCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeQueueBanCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return helpers.ApplyPunishment(s, i, &helpers.ApplySpec{
		Command:    "queue_ban",
		RoleKey:    "queue_banned_role",
		Action:     "queue ban",
		Adjective:  "queue banned",
		Summary:    "the queue ban",
		Title:      "Queue Ban Applied",
		Type:       "ban",
		Duration:   true,
		Duplicates: true,
	})
}
