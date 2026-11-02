package helpers

import "testing"

func TestAPlayerOutOfTheMatchIsFreeToQueueAgain(t *testing.T) {
	m := NewGameManager(GameDeps{})
	lg := &liveGame{g: Game{ID: 9, GuildID: "g", State: GameInGame, Players: []GamePlayer{{UserID: "alive"}, {UserID: "dead"}}}}
	m.mu.Lock()
	m.games[9] = lg
	m.mu.Unlock()
	m.indexLocked(lg)
	if !m.InGame("g", "dead") || !m.InGame("g", "alive") {
		t.Fatal("both players are in the game before anyone dies")
	}

	m.ReleasePlayers(9, "dead")
	if m.InGame("g", "dead") {
		t.Fatal("a player who died for good must be free to queue")
	}
	if !m.InGame("g", "alive") {
		t.Fatal("the other player is still in the game")
	}

	m.indexLocked(lg) // a later state change indexes the game again: the dead player must stay free
	if m.InGame("g", "dead") {
		t.Fatal("re-indexing the game must not put the dead player back")
	}
}
