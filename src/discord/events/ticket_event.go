package events

import (
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.RegisterEvent(TicketsEvent)
}

// TicketsEvent handles ticket opening and management buttons, plus the
// dashboard-panel publish menus.
var TicketsEvent = &types.Event{
	Name:        "Tickets",
	Description: "Handles ticket buttons and the panel selection/channel menus used to publish dashboard-created panels.",
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
	Description: "Resets inactivity timers and auto-claims open tickets when staff reply.",
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
	Description: "Reminds ticket owners when a ticket has been idle for the configured time. Never closes tickets.",
	Type:        "ready",
	Once:        false,
	Disabled:    false,
	System:      true,
	Execute:     func(s *discordgo.Session, _ *discordgo.Ready) { helpers.StartTicketInactivitySweeper(s) },
}
