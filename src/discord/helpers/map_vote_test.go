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
	if len(f.Manual["pre"]) != 1 || f.Manual["pre"][0].Embed != "teams" || len(f.Manual["post"]) != 1 || f.Manual["post"][0].Embed != "scored" {
		t.Errorf("manual steps by phase, switched-off ones left out: %v", f.Manual)
	}
}

func TestAnnounceStepWithItsOwnMessageIsPostedFromTheStep(t *testing.T) {
	f := parseGuildFlow([]byte(`{"profile":{"pipeline":[
		{"id":"hello","type":"announce","reply":{"type":"embed","embeds":[{"title":"Game {match.number}","description":"On {game.map} with {match.teams}"}]}},
		{"id":"after","type":"announce","hosting":"manual","phase":"post","reply":{"type":"message","content":"gg {game.mode}"}}]}}`))
	rep := f.Replies["hello"]
	if rep == nil || len(f.Announced) != 0 {
		t.Fatalf("a step's own message is kept by its id and is no built-in embed: %v %v", f.Replies, f.Announced)
	}
	g := Game{Number: 7, Map: "Lighthouse", Teams: []string{"Red"}, Players: []GamePlayer{{UserID: "1", Team: "Red"}, {UserID: "2", Team: "Red"}}}
	payload := rep.doc().Payload(VarFiller(stepVars(g, "queue-chat")))
	embeds, _ := payload["embeds"].([]map[string]any)
	if len(embeds) != 1 || embeds[0]["title"] != "Game 7" || embeds[0]["description"] != "On Lighthouse with **Red**: <@1> <@2>" {
		t.Errorf("variables are filled in: %v", payload["embeds"])
	}
	if m := f.Manual["post"]; len(m) != 1 || m[0].StepID != "after" || f.Replies["after"] == nil {
		t.Errorf("a manual step carries its message too: %v", f.Manual)
	}
}

func TestAStepUsesACustomEmbedOfTheProfile(t *testing.T) {
	f := parseGuildFlow([]byte(`{"profile":{
		"messages":[{"id":"embed-1","name":"Welcome","reply":{"type":"message","content":"Welcome to game {game.number}"}}],
		"pipeline":[{"id":"a","type":"announce","message":"embed-1"},{"id":"b","type":"announce","message":"gone"}]}}`))
	if f.Replies["a"] == nil || f.Replies["b"] != nil {
		t.Errorf("a step gets the custom embed it names, and nothing when it is gone: %v", f.Replies)
	}
}
