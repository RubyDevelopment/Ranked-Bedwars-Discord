package helpers

import (
	"testing"
	"time"

	"ranked-bedwars/src/types"
)

func TestMapVoteRulesComeFromTheProfile(t *testing.T) {
	lasts, perks, weighted := parseGuildFlow([]byte(`{"profile":{"hosting":{"mapVote":{"durationSec":30,"usePerks":false,"winner":"weighted"}}}}`)).mapVoteRules()
	if lasts != 30*time.Second || perks || !weighted {
		t.Errorf("got %v %v %v", lasts, perks, weighted)
	}
	lasts, perks, weighted = guildFlow{}.mapVoteRules()
	if lasts != mapVoteDuration || !perks || weighted {
		t.Errorf("the defaults are 45s, perks on, most votes: got %v %v %v", lasts, perks, weighted)
	}
}

func TestWeightedVoteNeverPicksAMapNobodyVotedFor(t *testing.T) {
	v := &mapVote{weighted: true, maps: []types.Map{{Name: "A"}, {Name: "B"}}, votes: map[string]int{"u": 1}, weights: map[string]float64{"u": 1}}
	for i := 0; i < 50; i++ {
		if v.winner().Name != "B" {
			t.Fatal("only B has votes")
		}
	}
}
