package interactions

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(PrefixCommandEvent)
}

// PrefixCommandEvent routes chat messages that start with the server's prefix to the matching command.
var PrefixCommandEvent = &types.Event{
	Name:        "Prefix Command Router",
	System:      true,
	Description: "Runs prefix commands (like =stats) typed in chat through the same checks as slash commands.",
	Type:        "message_create",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandlePrefixCommand,
}
