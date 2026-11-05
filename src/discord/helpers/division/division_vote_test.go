package division

import "testing"

func TestVoteMath(t *testing.T) {
	for eligible, want := range map[int]int{0: 1, 1: 1, 2: 1, 3: 2, 4: 2, 5: 3} {
		if got := votesNeeded(eligible); got != want {
			t.Errorf("votesNeeded(%d) = %d, want %d", eligible, got, want)
		}
	}
	// 5 eligible, 3 needed: passes at 3 approvals, fails once 3 deny (only 2 could still approve).
	for _, c := range []struct {
		yes, no int
		want    string
	}{{2, 2, ""}, {3, 0, "passed"}, {0, 2, ""}, {1, 3, "failed"}} {
		if got := outcome(c.yes, c.no, 5, 3); got != c.want {
			t.Errorf("outcome(%d, %d) = %q, want %q", c.yes, c.no, got, c.want)
		}
	}
}

func TestSlots(t *testing.T) {
	if got := Pugs.Slot(SetSpec); got != "pugs_spec_role" {
		t.Errorf("slot = %q", got)
	}
	if Premium.Has(SetVotes) || !Pups.Has(SetVotes) || !Premium.Has(SetRoles) {
		t.Error("vote settings belong to the voting divisions only")
	}
}
