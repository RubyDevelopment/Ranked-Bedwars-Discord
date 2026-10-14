package events

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(AccountAgeEvent)
}

// AccountAgeEvent checks a new member's Discord account against the server's minimum account age the moment they
// join, and queue-bans accounts that are too new (for the number of days the server set, or for good). The same check
// runs when a member joins a queue, so members who were already here are covered too. The rule is the dashboard's
// Account Age page.
var AccountAgeEvent = &types.Event{
	Name:        helpers.AccountAgeEventName,
	Description: "Queue-bans new members whose Discord account is younger than the server's minimum age.",
	Type:        "guild_member_add",
	Execute:     helpers.HandleAccountAge,
}

func init() {
	helpers.RegisterEvent(AppearanceSyncEvent)
}

// AppearanceSyncEvent keeps the bot's per-server nickname, avatar, banner and bio in line with the dashboard.
var AppearanceSyncEvent = &types.Event{
	Name:        "Appearance Sync",
	System:      true,
	Description: "Re-applies each server's configured bot nickname, avatar, banner and bio on startup and every 30 minutes.",
	Type:        "ready",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleAppearanceSync,
}
