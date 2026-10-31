package helpers

import (
	"testing"
	"time"

	"ranked-bedwars/src/types"
)

func TestMapVoteRulesComeFromTheProfile(t *testing.T) {
	lasts, perks, weighted, enabled := parseGuildFlow([]byte(`{"profile":{"hosting":{"mapVote":{"durationSec":30,"usePerks":false,"winner":"weighted","enabled":false}}}}`)).mapVoteRules()
	if lasts != 30*time.Second || perks || !weighted || enabled {
		t.Errorf("got %v %v %v %v", lasts, perks, weighted, enabled)
	}
	lasts, perks, weighted, enabled = guildFlow{}.mapVoteRules()
	if lasts != mapVoteDuration || !perks || weighted || !enabled {
		t.Errorf("the defaults are 45s, perks on, most votes, voting on: got %v %v %v %v", lasts, perks, weighted, enabled)
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

func TestManualHostingStepsAreKeptApartFromTheBotsOwn(t *testing.T) {
	f := parseGuildFlow([]byte(`{"profile":{"pipeline":[
		{"type":"announce","embed":"introduction"},
		{"type":"announce","embed":"teams","hosting":"manual"},
		{"type":"announce","embed":"scored","hosting":"manual","phase":"post"},
		{"type":"announce","embed":"noMaps","hosting":"manual","enabled":false}]}}`))
	if !f.Announced["introduction"] || f.Announced["teams"] {
		t.Errorf("only the bot's own announce steps keep the default posting away: %v", f.Announced)
	}
	if len(f.Manual["pre"]) != 1 || f.Manual["pre"][0] != "teams" || len(f.Manual["post"]) != 1 || f.Manual["post"][0] != "scored" {
		t.Errorf("manual steps by phase, switched-off ones left out: %v", f.Manual)
	}
}
