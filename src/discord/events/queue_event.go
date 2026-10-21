package events

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(QueueEvent)
}

// QueueEvent tracks members joining and leaving the guild's queue voice
// channels (game config "queues"), screens joiners and forms lobbies.
var QueueEvent = &types.Event{
	Name:        helpers.QueueEventName,
	Description: "Forms games from the members waiting in the queue voice channels, and keeps out members who aren't allowed to queue.",
	Type:        "voice_state_update",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleQueue,
}
