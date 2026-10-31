package helpers

import (
	"encoding/json"
	"testing"

	"ranked-bedwars/src/types"
)

func TestModeGUISlotPreservesZeroAndOmittedSlots(t *testing.T) {
	tests := []struct {
		name string
		json string
		want *int
	}{
		{name: "slot zero", json: `{"gui_slot":0}`, want: intPointer(0)},
		{name: "positive slot", json: `{"gui_slot":12}`, want: intPointer(12)},
		{name: "omitted slot", json: `{}`},
		{name: "negative slot", json: `{"gui_slot":-1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mode types.Mode
			if err := json.Unmarshal([]byte(tt.json), &mode); err != nil {
				t.Fatal(err)
			}
			got := modeGUISlot(mode)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("modeGUISlot() = %d, want nil", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("modeGUISlot() = %v, want %d", got, *tt.want)
			}
		})
	}
}

func intPointer(value int) *int { return &value }
