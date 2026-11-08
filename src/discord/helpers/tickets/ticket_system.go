package tickets

import (
	"encoding/json"
	"sort"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"
)

// A command panel is a ticket panel that is assigned to a command (screenshare) in the dashboard. The command opens its tickets
// itself, so the panel is never published; everything else about it is an ordinary ticket panel: staff roles, log channel, claiming,
// the welcome message, inactivity, transcripts, and the /ticket commands inside its tickets.

// CommandHooks run after a ticket of a command's panel is claimed or closed, whichever way that happened.
type CommandHooks struct {
	Claimed func(s *discordgo.Session, guildID string, ticket *types.Ticket)
	Closed  func(s *discordgo.Session, guildID string, ticket *types.Ticket) // s is nil when no session is at hand
}

var commandHooks = map[string]CommandHooks{}

// RegisterCommandHooks sets what happens to tickets of the panel assigned to command. Call it from init.
func RegisterCommandHooks(command string, hooks CommandHooks) { commandHooks[command] = hooks }

// PlaceholderVars adds feature-specific placeholders ({name} -> value) to the text of a ticket's welcome message. panel is the
// ticket's panel, so a feature can tell its own tickets.
var PlaceholderVars func(ticket *types.Ticket, panel *types.InteractivePanel) map[string]string

func isCommandPanel(panel *types.InteractivePanel) bool {
	return panel != nil && panel.Ticket != nil && panel.Ticket.Command != ""
}

// CommandPanel is the guild's ticket panel assigned to the command (the first by id when there are several), or nil. It reads
// only the panel designs, so it is cheap enough for a hot path.
func CommandPanel(guildID, command string) *types.InteractivePanel {
	raw, err := database.Must().GuildConfig.Field(guildID, interactivePanelsField)
	if err != nil && !sdk.IsNotFound(err) || len(raw) == 0 {
		return nil
	}
	ip := &types.InteractivePanels{}
	if json.Unmarshal(raw, ip) != nil {
		return nil
	}
	normalizeInteractivePanels(ip)
	var found []*types.InteractivePanel
	for _, panel := range ip.Panels {
		if panel != nil && panel.Ticket != nil && panel.Ticket.Command == command {
			found = append(found, panel)
		}
	}
	if len(found) == 0 {
		return nil
	}
	sort.Slice(found, func(a, b int) bool { return found[a].ID < found[b].ID })
	return found[0]
}

func runClaimed(s *discordgo.Session, guildID string, ticket *types.Ticket, panel *types.InteractivePanel) {
	if isCommandPanel(panel) && commandHooks[panel.Ticket.Command].Claimed != nil {
		commandHooks[panel.Ticket.Command].Claimed(s, guildID, ticket)
	}
}

func runClosed(s *discordgo.Session, guildID string, ticket *types.Ticket, panel *types.InteractivePanel) {
	if isCommandPanel(panel) && commandHooks[panel.Ticket.Command].Closed != nil {
		commandHooks[panel.Ticket.Command].Closed(s, guildID, ticket)
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
