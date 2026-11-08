package tickets

import (
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// A system panel is a ticket type the bot provides itself (screenshare): it has no panel message and is not stored in the guild's
// panels. It is built from another feature's own settings every time the panels are loaded, so its tickets use the whole ticket
// system (claim, close, transcript, log channel, /ticket commands, history) without an admin setting up a panel.

// SystemPanel is one such ticket type. Claimed and Closed run after a ticket of it is claimed or closed, whichever way that happened.
type SystemPanel struct {
	Build   func(guildID string) *types.InteractivePanel
	Claimed func(s *discordgo.Session, guildID string, ticket *types.Ticket)
	Closed  func(s *discordgo.Session, guildID string, ticket *types.Ticket)
}

var systemPanels = map[string]SystemPanel{}

// RegisterSystemPanel adds a system ticket type. Call it from init.
func RegisterSystemPanel(id string, panel SystemPanel) { systemPanels[id] = panel }

func isSystemPanel(id string) bool {
	_, ok := systemPanels[id]
	return ok
}

// PlaceholderVars adds feature-specific placeholders ({name} -> value) to the text of a ticket's welcome message.
var PlaceholderVars func(ticket *types.Ticket) map[string]string

// addSystemPanels puts the system panels into a freshly loaded set.
func addSystemPanels(guildID string, ip *types.InteractivePanels) {
	for id, system := range systemPanels {
		if panel := system.Build(guildID); panel != nil {
			panel.ID = id
			ip.Panels[id] = panel
		}
	}
}

// storedPanels is ip without its system panels: what belongs in the guild config.
func storedPanels(ip *types.InteractivePanels) *types.InteractivePanels {
	if len(systemPanels) == 0 {
		return ip
	}
	stored := &types.InteractivePanels{Panels: make(map[string]*types.InteractivePanel, len(ip.Panels))}
	for id, panel := range ip.Panels {
		if !isSystemPanel(id) {
			stored.Panels[id] = panel
		}
	}
	return stored
}

func runClaimed(s *discordgo.Session, guildID string, ticket *types.Ticket) {
	if system, ok := systemPanels[ticket.PanelID]; ok && system.Claimed != nil {
		system.Claimed(s, guildID, ticket)
	}
}

// runClosed runs the closed hook; s is nil when the ticket was closed without a session at hand.
func runClosed(s *discordgo.Session, guildID string, ticket *types.Ticket) {
	if system, ok := systemPanels[ticket.PanelID]; ok && system.Closed != nil {
		system.Closed(s, guildID, ticket)
	}
}

// OpenOptions are the extras a feature can ask for when it opens a ticket.
type OpenOptions struct {
	ExtraMembers []string // people besides the owner who can see and write in it until removed
}

// RefreshWelcome re-renders a ticket's welcome message (after a feature changed what its placeholders show).
func RefreshWelcome(s *discordgo.Session, guildID string, ticket *types.Ticket) {
	if ip, err := Tickets.LoadOpen(guildID); err == nil {
		RefreshTicketWelcome(s, guildID, ticket, ip.Panels[ticket.PanelID])
	}
}
