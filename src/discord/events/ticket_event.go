package events

import (
	"github.com/bwmarrin/discordgo"

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

func init() {
	helpers.RegisterEvent(TicketActivityEvent)
}

var TicketActivityEvent = &types.Event{
	Name:        "Ticket Activity",
	Description: "Resets ticket inactivity timers when a member sends a message in an open ticket.",
	Type:        "message_create",
	Once:        false,
	Disabled:    false,
	System:      true,
	Execute:     helpers.HandleTicketMessage,
}

func init() {
	helpers.RegisterEvent(TicketInactivitySweepEvent)
}

var TicketInactivitySweepEvent = &types.Event{
	Name:        "Ticket Inactivity Manager",
	Description: "Sends inactivity reminders and closes tickets after their configured idle period.",
	Type:        "ready",
	Once:        false,
	Disabled:    false,
	System:      true,
	Execute:     func(s *discordgo.Session, _ *discordgo.Ready) { helpers.StartTicketInactivitySweeper(s) },
}
