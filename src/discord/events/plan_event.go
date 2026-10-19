package events

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(PlanReminderEvent)
}

// PlanReminderEvent tells each server's owner by DM when the plan is about to end, and once after it has.
var PlanReminderEvent = &types.Event{
	Name:        "Plan Reminders",
	System:      true,
	Description: "Messages a server's owner a few days before its plan ends, and once after it has, until it is renewed.",
	Type:        "ready",
	Execute:     helpers.HandlePlanReminders,
}
