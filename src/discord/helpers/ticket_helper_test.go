package helpers

import (
	"testing"
	"time"

	"ranked-bedwars/src/types"
)

func TestTicketBlacklistEntryFor(t *testing.T) {
	now := time.Now().Unix()
	panels := &types.InteractivePanels{
		Tickets: types.TicketState{
			Blacklist: []*types.TicketBlacklistEntry{
				nil,
				{UserID: "active", ExpiresAt: now + 60},
				{UserID: "permanent", ExpiresAt: 0},
				{UserID: "expired", ExpiresAt: now - 1},
			},
		},
	}

	tests := []struct {
		userID string
		want   bool
	}{
		{userID: "active", want: true},
		{userID: "permanent", want: true},
		{userID: "expired", want: false},
		{userID: "unknown", want: false},
	}
	for _, test := range tests {
		t.Run(test.userID, func(t *testing.T) {
			got := TicketBlacklistEntryFor(panels, test.userID) != nil
			if got != test.want {
				t.Fatalf("TicketBlacklistEntryFor(%q) = %t, want %t", test.userID, got, test.want)
			}
		})
	}
}
