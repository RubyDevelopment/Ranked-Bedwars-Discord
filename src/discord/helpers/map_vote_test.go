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
	// A hand-hosted game runs the shared blocks (no hosting set) and its own, by phase; a switched-off block is left out.
	if len(f.Manual["pre"]) != 2 || f.Manual["pre"][0].Embed != "introduction" || f.Manual["pre"][1].Embed != "teams" || len(f.Manual["post"]) != 1 || f.Manual["post"][0].Embed != "scored" {
		t.Errorf("manual blocks by phase, switched-off ones left out: %v", f.Manual)
	}
}

func TestAnnounceStepWithItsOwnMessageIsPostedFromTheStep(t *testing.T) {
	f := parseGuildFlow([]byte(`{"profile":{"messages":[
		{"id":"m1","reply":{"type":"embed","embeds":[{"title":"Game {match.number}","description":"On {game.map} with {match.teams}"}]}},
		{"id":"m2","reply":{"type":"message","content":"gg {game.mode}"}}],"pipeline":[
		{"id":"hello","type":"announce","message":"m1"},
		{"id":"after","type":"announce","hosting":"manual","phase":"post","message":"m2"}]}}`))
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
	if m := f.Manual["post"]; len(m) != 1 || m[0].ID != "after" || f.Replies["after"] == nil {
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

func TestVoteEndsWhenTheLeaderCannotBeCaught(t *testing.T) {
	players := []GamePlayer{{UserID: "1"}, {UserID: "2"}, {UserID: "3"}, {UserID: "4"}, {UserID: "5"}}
	v := &mapVote{maps: make([]types.Map, 2), votes: map[string]int{}, weights: map[string]float64{"1": 1, "2": 1, "3": 1, "4": 1, "5": 1}, game: Game{Players: players}}
	v.votes["1"], v.votes["2"] = 0, 0
	if v.settled() {
		t.Fatal("two votes of five can still be caught by the other three")
	}
	v.votes["3"] = 0
	if !v.settled() {
		t.Fatal("three of five votes cannot be caught by two")
	}
	w := &mapVote{weighted: true, maps: make([]types.Map, 2), votes: map[string]int{"1": 0, "2": 0, "3": 0}, weights: v.weights, game: Game{Players: players}}
	if w.settled() {
		t.Fatal("a weighted vote is a lottery by share: it only ends when everybody voted")
	}
}

func TestLiveConsoleFramesAreNotQueuedBehindGameFrames(t *testing.T) {
	for _, kind := range []string{"chat.line", "chat.bots", "bot.event", "bot.result"} {
		if !liveFrame(kind) {
			t.Errorf("%s is live console traffic and must be handled at once", kind)
		}
	}
	for _, kind := range []string{"game.event", "game.started", "game.ended", "host.maps", "host.failed", "host.map.vote", "players.sync"} {
		if liveFrame(kind) {
			t.Errorf("%s is about a game and keeps its order on the worker", kind)
		}
	}
}

func TestHandHostedFlowKeepsSharedBlocksGroupsAndWaits(t *testing.T) {
	f := parseGuildFlow([]byte(`{"profile":{"pipeline":[
		{"id":"intro","type":"announce","embed":"introduction"},
		{"id":"bot","type":"group","hosting":"automatic","children":[{"id":"p","type":"party"}]},
		{"id":"shared","type":"group","mode":"parallel","children":[{"id":"w","type":"wait","waitSec":2},{"id":"a","type":"announce","embed":"teamSetup","hosting":"manual"}]},
		{"id":"after","type":"announce","embed":"matchStartedManual","hosting":"manual","phase":"in"}]}}`))
	pre := f.Manual["pre"]
	if len(pre) != 2 || pre[0].ID != "intro" || pre[1].ID != "shared" {
		t.Fatalf("shared blocks run by hand, the bot's own do not: %v", pre)
	}
	if pre[1].Mode != "parallel" || len(pre[1].Children) != 2 || pre[1].Children[0].WaitSec != 2 {
		t.Errorf("a group keeps its mode and its blocks, a wait its seconds: %+v", pre[1])
	}
	if in := f.Manual["in"]; len(in) != 1 || in[0].ID != "after" {
		t.Errorf("blocks by phase: %v", f.Manual)
	}
	if !f.Announced["introduction"] || f.Announced["teamSetup"] {
		t.Errorf("the shared welcome is the bot's too; a hand-only block is not: %v", f.Announced)
	}
}
