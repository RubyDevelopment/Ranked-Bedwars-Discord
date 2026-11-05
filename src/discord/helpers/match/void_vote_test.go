package match

import (
	"testing"
	"time"
)

// A void vote counts as open from the first ballot until it is cleared, or for a quarter of an hour at most.
func TestVoidVoteOpenWindow(t *testing.T) {
	const id = 987654
	defer ClearVoidVotes(id)
	if voidVoteOpen(id) {
		t.Fatal("open before any ballot")
	}
	castVoidBallot(id, "u1", true, 8)
	if !voidVoteOpen(id) {
		t.Fatal("not open after a ballot")
	}
	voidVotes.mu.Lock()
	voidVotes.at[id] = time.Now().Add(-voidVoteLife - time.Minute)
	voidVotes.mu.Unlock()
	if voidVoteOpen(id) {
		t.Fatal("a forgotten vote stays open")
	}
	ClearVoidVotes(id)
	if voidVoteOpen(id) {
		t.Fatal("open after it was cleared")
	}
}
