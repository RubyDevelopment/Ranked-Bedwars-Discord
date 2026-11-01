package helpers

import "sync"

// Void votes: any player of a game may vote to void it, and the game is voided once three quarters of its players (rounded down, at least one)
// have voted. A player's vote counts once, however often they use the command.

var voidVotes = struct {
	mu sync.Mutex
	by map[uint]map[string]bool // game id -> user ids that voted
}{by: map[uint]map[string]bool{}}

// VoidVotesNeeded is how many votes void a game of this many players: three quarters, rounded down, and never fewer than one.
func VoidVotesNeeded(players int) int {
	return max(1, players*3/4)
}

// CastVoidVote records a player's vote and returns the votes so far, the votes needed, and whether this player had already voted.
func CastVoidVote(gameID uint, userID string, players int) (votes, needed int, already bool) {
	voidVotes.mu.Lock()
	defer voidVotes.mu.Unlock()
	if voidVotes.by[gameID] == nil {
		voidVotes.by[gameID] = map[string]bool{}
	}
	already = voidVotes.by[gameID][userID]
	voidVotes.by[gameID][userID] = true
	return len(voidVotes.by[gameID]), VoidVotesNeeded(players), already
}

// ClearVoidVotes forgets the votes of a game (it ended or was voided).
func ClearVoidVotes(gameID uint) {
	voidVotes.mu.Lock()
	delete(voidVotes.by, gameID)
	voidVotes.mu.Unlock()
}
