package division

import "testing"

func TestSlots(t *testing.T) {
	if got := Pugs.Slot(SetSpec); got != "pugs_spec_role" {
		t.Errorf("slot = %q", got)
	}
	if Premium.Has(SetVotes) || !Pups.Has(SetVotes) || !Premium.Has(SetRoles) {
		t.Error("vote settings belong to the voting divisions only")
	}
}

func TestVerdict(t *testing.T) {
	for _, c := range []struct {
		yes, no int
		want    string
	}{{0, 0, "tied"}, {3, 1, "approved"}, {1, 3, "denied"}, {2, 2, "tied"}} {
		if got := verdict(c.yes, c.no); got != c.want {
			t.Errorf("verdict(%d, %d) = %q, want %q", c.yes, c.no, got, c.want)
		}
	}
}
