package tickets

import (
	"testing"

	"ranked-bedwars/src/types"
)

// A panel assigned to a command is never published, and the command's managers count as its staff.
func TestCommandPanels(t *testing.T) {
	assigned := &types.InteractivePanel{ID: "ss", Type: types.PanelTypeTicket, Ticket: &types.TicketPanelConfig{
		Command:     "screenshare",
		Settings:    types.TicketSettings{StaffRoleIDs: []string{"staff"}},
		Screenshare: &types.ScreensharePanelOptions{ManagerRoleIDs: []string{"manager"}},
	}}
	plain := &types.InteractivePanel{ID: "own", Type: types.PanelTypeTicket, Ticket: &types.TicketPanelConfig{}}
	ip := &types.InteractivePanels{Panels: map[string]*types.InteractivePanel{"ss": assigned, "own": plain}}

	if listed := SortedTicketPanels(ip); len(listed) != 1 || listed[0].ID != "own" {
		t.Fatalf("publishable panels = %v, want only the unassigned one", listed)
	}
	if !isCommandPanel(assigned) || isCommandPanel(plain) {
		t.Fatal("isCommandPanel must tell an assigned panel from a plain one")
	}
	if roles := staffRoleIDs(assigned, nil); len(roles) != 2 || roles[1] != "manager" {
		t.Fatalf("staff roles = %v, want the staff role and the manager", roles)
	}
}
