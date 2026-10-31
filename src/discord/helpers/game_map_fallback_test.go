package helpers

import (
	"reflect"
	"testing"

	"ranked-bedwars/src/types"
)

func TestMapSelectionFallbacksExcludeFailedAndIneligibleMaps(t *testing.T) {
	maps := []types.Map{
		{Name: "Chosen", GUISlot: 4, MapHeight: 88, ModeSettings: map[string]types.MapModeSettings{"Doubles": {Teams: []string{"Red", "Blue"}}}},
		{Name: "Too Few Teams", GUISlot: 5, ModeSettings: map[string]types.MapModeSettings{"Doubles": {Teams: []string{"Red"}}}},
		{Name: "Fallback", GUISlot: 6, MapHeight: 96, ModeSettings: map[string]types.MapModeSettings{"Doubles": {Teams: []string{"Green", "Yellow"}}}},
		{Name: "Chosen", GUISlot: 7, ModeSettings: map[string]types.MapModeSettings{"Doubles": {Teams: []string{"Red", "Blue"}}}},
		{Name: "Duplicate Slot", GUISlot: 6, ModeSettings: map[string]types.MapModeSettings{"Doubles": {Teams: []string{"Black", "White"}}}},
	}

	eligible := mapsWithEnoughTeams(maps, "Doubles", 2)
	if len(eligible) != 4 || eligible[1].Name != "Fallback" {
		t.Fatalf("eligible maps = %#v, want only maps with enough teams", eligible)
	}
	selected := mapSelectionWithFallbacks(eligible, eligible[0], "Doubles")
	if selected.Name != "Chosen" || selected.GUISlot != 4 {
		t.Fatalf("selected = %#v, want Chosen", selected)
	}
	if len(selected.Fallbacks) != 1 {
		t.Fatalf("fallbacks = %#v, want only the distinct eligible fallback", selected.Fallbacks)
	}
	fallback := selected.Fallbacks[0]
	if fallback.Name != "Fallback" || fallback.GUISlot != 6 || fallback.MapHeight != 96 || !reflect.DeepEqual(fallback.Teams, []string{"Green", "Yellow"}) {
		t.Fatalf("fallback = %#v, want eligible map metadata", fallback)
	}
}

func TestMapSelectionRetainsLegacyPoolWhenNoMapMeetsTeamThreshold(t *testing.T) {
	maps := []types.Map{{Name: "Legacy A", Teams: []string{"Red"}}, {Name: "Legacy B"}}
	if got := mapsWithEnoughTeams(maps, "Doubles", 2); !reflect.DeepEqual(got, maps) {
		t.Fatalf("eligible = %#v, want the legacy map pool %#v", got, maps)
	}
}
