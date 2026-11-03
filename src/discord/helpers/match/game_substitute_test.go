package match

import "testing"

// A majority of the OTHER players decides: the player who may be swapped out never votes.
func TestSubstituteVotesNeeded(t *testing.T) {
	for voters, want := range map[int]int{1: 1, 2: 2, 3: 2, 4: 3, 5: 3, 7: 4, 11: 6} {
		if got := substituteVotesNeeded(voters); got != want {
			t.Errorf("%d voters: needed %d, want %d", voters, got, want)
		}
	}
}

func TestSubstituteVoteCounts(t *testing.T) {
	v := &substituteVote{voters: []string{"a", "b", "c", "d", "e"}, ballots: map[string]bool{"a": true, "b": false, "c": true, "x": true}}
	yes, no := v.counts()
	if len(yes) != 2 || len(no) != 1 {
		t.Fatalf("yes=%v no=%v, want 2 and 1 (someone who may not vote is not counted)", yes, no)
	}
	if !(len(yes) >= substituteVotesNeeded(len(v.voters))) == false {
		t.Errorf("2 of 5 should not pass: %d needed", substituteVotesNeeded(len(v.voters)))
	}
}
