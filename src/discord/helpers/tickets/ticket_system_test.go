package tickets

import (
	"testing"

	"ranked-bedwars/src/types"
)

// A system panel is part of the loaded panels but never of what is saved or published.
func TestSystemPanelsAreNeverStoredOrPublished(t *testing.T) {
	RegisterSystemPanel("test-system", SystemPanel{Build: func(string) *types.InteractivePanel {
		return &types.InteractivePanel{Type: types.PanelTypeTicket, Ticket: &types.TicketPanelConfig{}}
	}})
	defer delete(systemPanels, "test-system")

	ip := &types.InteractivePanels{Panels: map[string]*types.InteractivePanel{
		"own": {ID: "own", Type: types.PanelTypeTicket, Ticket: &types.TicketPanelConfig{}},
	}}
	addSystemPanels("guild", ip)
	if ip.Panels["test-system"] == nil || ip.Panels["test-system"].ID != "test-system" {
		t.Fatal("the system panel was not added with its id")
	}
	if stored := storedPanels(ip); len(stored.Panels) != 1 || stored.Panels["own"] == nil {
		t.Fatalf("stored panels = %v, want only the guild's own", stored.Panels)
	}
	if listed := SortedTicketPanels(ip); len(listed) != 1 || listed[0].ID != "own" {
		t.Fatalf("publishable panels = %v, want only the guild's own", listed)
	}
}
