package staff

import (
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/punish"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ModerationQueueBanCommand)
	punish.RegisterLadderAction("queue_ban", queueBanSpec)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationQueueBanCommand bans a player from joining queues by recording a ban restriction.
var ModerationQueueBanCommand = &types.Command{
	Name:          "ban",
	Requirements:  types.PunishmentRequirements("queue_ban_logs", "queue_banned_role"),
	Description:   "Bans a disruptive player from joining queues.",
	PrefixAliases: []string{"qban"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: punish.PunishmentOptions("Discord user to queue ban", "Reason for the queue ban", true, true, true),
	},
	Autocomplete: punish.PunishmentLevelAutocomplete("queue_ban"),
	Execute:      executeModerationQueueBanCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

var queueBanSpec = &punish.ApplySpec{
	Command:     "ban",
	RoleKey:     "queue_banned_role",
	Action:      "queue ban",
	Adjective:   "queue banned",
	Summary:     "the queue ban",
	Title:       "Queue Ban Applied",
	Type:        "ban",
	Duration:    true,
	Duplicates:  true,
	AppliedNote: "The queue banned role was given to the member.",
	FailedNote:  "The punishment was recorded, but no queue banned role is set (or it could not be given). Choose one in the dashboard under Moderation > Punishment ladders.",
	Ladder:      "queue_ban",
}

func executeModerationQueueBanCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return punish.ApplyPunishment(s, i, queueBanSpec)
}
