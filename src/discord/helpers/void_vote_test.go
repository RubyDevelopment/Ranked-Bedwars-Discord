package helpers

import "testing"

func TestVoidVotesNeededIsThreeQuartersRoundedDown(t *testing.T) {
	for players, want := range map[int]int{1: 1, 2: 1, 3: 2, 4: 3, 6: 4, 8: 6, 10: 7} {
		if got := VoidVotesNeeded(players); got != want {
			t.Errorf("%d players: need %d votes, got %d", players, want, got)
		}
	}
}

func TestAPlayersVoteCountsOnce(t *testing.T) {
	defer ClearVoidVotes(7)
	if votes, needed, already := CastVoidVote(7, "a", 4); votes != 1 || needed != 3 || already {
		t.Fatalf("first vote: %d/%d already=%v", votes, needed, already)
	}
	if votes, _, already := CastVoidVote(7, "a", 4); votes != 1 || !already {
		t.Fatalf("the same player voting again: %d already=%v", votes, already)
	}
	CastVoidVote(7, "b", 4)
	if votes, needed, _ := CastVoidVote(7, "c", 4); votes != needed {
		t.Fatalf("three players of four void the game: %d/%d", votes, needed)
	}
}
