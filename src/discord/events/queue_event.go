package events

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/queue"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

func init() {
	helpers.RegisterEvent(QueueEvent)
	helpers.RegisterEvent(QueueVoiceStatusEvent)
}

// QueueEvent tracks members joining and leaving the guild's queue voice
// channels (game config "queues"), screens joiners and forms lobbies.
var QueueEvent = &types.Event{
	Name:        queue.QueueEventName,
	Description: "Forms games from the members waiting in the queue voice channels, and keeps out members who aren't allowed to queue.",
	Type:        "voice_state_update",
	Once:        false,
	Disabled:    false,
	Execute:     queue.HandleQueue,
}

// QueueVoiceStatusEvent sets the status under each queue channel's name (how many are queued) once the bot is connected; after that
// the queue event keeps it up to date as members join and leave.
var QueueVoiceStatusEvent = &types.Event{
	Name:        "Queue Voice Status",
	System:      true,
	Description: "Shows how many are queued, in a game or ineligible in the status under each queue voice channel's name.",
	Type:        "ready",
	Execute:     func(s *discordgo.Session, _ *discordgo.Ready) { queue.StartVoiceStatuses(s) },
}
