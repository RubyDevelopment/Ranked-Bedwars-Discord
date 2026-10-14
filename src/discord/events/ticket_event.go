package events

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(TicketsEvent)
}

// TicketsEvent listens for every ticket button, select menu and modal
// submission (panel builder, open, claim, close). Ticket panels themselves are
// created and sent from `/ticket panel`; nothing is published in the background.
var TicketsEvent = &types.Event{
	Name:        "Tickets",
	Description: "Handles the ticket panel's buttons, select menus and forms: opening, claiming and closing tickets.",
	Type:        "interaction_create",
	Once:        false,
	Disabled:    false,
	Execute:     helpers.HandleTickets,
}
