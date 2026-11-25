package match

import (
	"testing"

	"ranked-bedwars/src/types"
)

func TestModeForParty(t *testing.T) {
	f := FlowSettings{Formations: []Formation{{ID: "cap", Kind: FormationCaptains}, {ID: "rnd", Kind: FormationRandom}}}
	m := types.Mode{FormationID: "cap", PartyFormationID: "rnd"}

	if !f.IsCaptain(f.ModeFor(m, false)) {
		t.Error("no party: want the mode's own captain formation")
	}
	if f.IsCaptain(f.ModeFor(m, true)) {
		t.Error("party: want the party formation (random), not captains")
	}
	m.PartyFormationID = "gone"
	if !f.IsCaptain(f.ModeFor(m, true)) {
		t.Error("unknown party formation: want the mode's own")
	}
}
