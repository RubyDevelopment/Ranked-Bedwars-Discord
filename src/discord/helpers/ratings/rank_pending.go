package ratings

import (
	"sync"
	"time"
)

// A finished game saves ELO first; the stored rank and Discord roles follow later (ApplyRanks, one slow call per player). Until then
// they are stale, so anything that gates on rank or rank roles (the queue) waits on WaitRankSync.
// The marker expires on its own so a game that never reaches ApplyRanks cannot block a player.
const rankPendingTTL = 2 * time.Minute

var (
	rankPendingMu sync.Mutex
	rankPending   = map[string]time.Time{}
)

func markRankPending(guildID, userID string) {
	rankPendingMu.Lock()
	rankPending[guildID+":"+userID] = time.Now().Add(rankPendingTTL)
	rankPendingMu.Unlock()
}

func clearRankPending(guildID, userID string) {
	rankPendingMu.Lock()
	delete(rankPending, guildID+":"+userID)
	rankPendingMu.Unlock()
}

// WaitRankSync blocks until the player's post-game rank/role update has finished, for at most max.
func WaitRankSync(guildID, userID string, max time.Duration) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		rankPendingMu.Lock()
		until, ok := rankPending[guildID+":"+userID]
		rankPendingMu.Unlock()
		if !ok || time.Now().After(until) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}
