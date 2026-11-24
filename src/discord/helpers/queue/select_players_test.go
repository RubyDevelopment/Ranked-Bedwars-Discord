package queue

import (
	"fmt"
	"testing"

	"ranked-bedwars/src/discord/helpers/partymgr"
)

func members(ids ...string) []QueueMember {
	out := make([]QueueMember, len(ids))
	for i, id := range ids {
		out[i] = QueueMember{UserID: id}
	}
	return out
}

func party(ids ...string) partymgr.PartyGroup { return partymgr.PartyGroup{Leader: ids[0], Members: ids} }

// Three parties of two in a 3v3: only two parties get in; the third waits for solos.
func TestSelectPlayersThirdPartyWaits(t *testing.T) {
	players := members("a1", "a2", "b1", "b2", "c1", "c2")
	parties := []partymgr.PartyGroup{party("a1", "a2"), party("b1", "b2"), party("c1", "c2")}
	// 6 slots, 2 teams of 3: parties of 2 cannot all sit whole (2+2 > 3), so three parties never form a lobby.
	if got := SelectPlayers(players, parties, 6, 2); got != nil {
		t.Fatalf("three parties of 2 must not fill a 3v3, got %v", got)
	}
	// Two parties plus two solos do.
	players = append(members("a1", "a2", "b1", "b2"), members("s1", "s2")...)
	got := SelectPlayers(players, []partymgr.PartyGroup{party("a1", "a2"), party("b1", "b2")}, 6, 2)
	if len(got) != 6 {
		t.Fatalf("two parties + two solos should fill the lobby, got %v", got)
	}
}

// A party is never split by the selection: a lobby needing half a party is skipped.
func TestSelectPlayersNeverHalfParty(t *testing.T) {
	players := members("a1", "a2", "a3", "s1", "s2")
	got := SelectPlayers(players, []partymgr.PartyGroup{party("a1", "a2", "a3")}, 4, 2)
	for _, m := range got {
		if m.UserID == "a1" || m.UserID == "a2" || m.UserID == "a3" {
			n := 0
			for _, x := range got {
				if x.UserID[0] == 'a' {
					n++
				}
			}
			if n != 3 {
				t.Fatalf("party split: %s", fmt.Sprint(got))
			}
		}
	}
}
