package proofread

import (
	"encoding/json"
	"strings"
	"testing"
)

// The rules are the Pika profile's own (Engine defaults.js), so the test reads chat the way a game is tracked.
const pikaServer = `{"profile":{"tracking":{"teamAliases":{"aqua":["cyan"]},"finalKill":{"type":"includes","value":"FINAL KILL"}},"rules":[
 {"id":"bed","on":"chat","match":{"type":"regex","value":"^(?:⚠\\s+)?(?<team>[A-Za-z]+)\\s+Team'?s?\\s+Bed\\b.*?\\b(?:by|from|of|under|at|than)\\s+(?<player>[A-Za-z0-9_]{3,16})\\b"},"actions":[{"type":"track","kind":"bed","fields":{"team":"{team}","player":"{player}"}}]},
 {"id":"kill","on":"chat","match":{"type":"regex","value":"^(?<victim>[A-Za-z0-9_]{3,16}) has been killed by (?<killer>[A-Za-z0-9_]{3,16})"},"actions":[{"type":"track","kind":"kill","fields":{"victim":"{victim}","killer":"{killer}"},"final":{"type":"includes","value":"FINAL KILL"}}]},
 {"id":"elim","on":"chat","match":{"type":"regex","value":"^⚠\\s+Team\\s+(?<team>[A-Za-z]+)\\s+has been eliminated"},"actions":[{"type":"track","kind":"eliminated","fields":{"team":"{team}"}}]},
 {"id":"win","on":"chat","match":{"type":"regex","value":"^(?<team>\\w+)\\s+won\\s+the\\s+game!"},"actions":[{"type":"track","kind":"win","fields":{"team":"{team}"}}]}]}}`

var roster = []Member{
	{"bummyyyyy", "Team A"}, {"EGIRLMEI", "Team A"}, {"Raahim_Playz", "Team A"}, {"EnderBlast313", "Team A"},
	{"deloid_AK", "Team B"}, {"HowYouFeeling", "Team B"}, {"HT206", "Team B"}, {"cat1lol", "Team B"},
}

// The chat of the second screenshot, as the reader gives it (the chat mod's [COPY] buttons included, one letter misread).
var chat = []string{
	"TEAM EGIRLMEI: gap [COPY]",
	"deloid_AK has been killed by bummyyyyy [COPY]",
	"HowYouFeeling has been killed by EGIRLMEI [COPY]",
	"Blue Team's Bed has been destroyed by EGIRLMEI [COPY]",
	"HT2O6 has been killed by bummyyyyy FINAL KILL [COPY]",
	"deloid_AK has been killed by EGIRLMEI FINAL KILL [COPY]",
	"HowYouFeeling has been killed by EGIRLMEI FINAL KILL [COPY]",
	"⚠ Team Blue has been eliminated from the game! [COPY]",
	"How would you rate this map? [COPY]",
}

func profile(t *testing.T) Profile {
	t.Helper()
	return LoadProfile(json.RawMessage(pikaServer))
}

func TestAnalyzeReadsTheChatAgainstTheRoster(t *testing.T) {
	res := Analyze(chat, profile(t), roster, "Team A")
	if res.Winner != "Team A" {
		t.Fatalf("winner %q (%s)", res.Winner, res.WinnerBy)
	}
	if res.Mismatch != "" {
		t.Fatalf("mismatch %q", res.Mismatch)
	}
	if res.MVP.Name != "EGIRLMEI" || !strings.Contains(res.MVPBy, "most kills") {
		t.Fatalf("mvp %+v by %q", res.MVP, res.MVPBy)
	}
	if len(res.BedBreaks) != 1 || res.BedBreaks[0].Name != "EGIRLMEI" {
		t.Fatalf("beds %+v", res.BedBreaks)
	}
	names := map[string]string{}
	for _, m := range res.Seen {
		names[m.Name] = m.Team
	}
	if names["HT206"] != "Team B" || names["bummyyyyy"] != "Team A" || len(names) < 5 {
		t.Fatalf("seen %v", names)
	}
}

func TestMismatchWhenAnotherTeamClaimsTheWin(t *testing.T) {
	res := Analyze(chat, profile(t), roster, "Team B")
	if res.Winner != "Team A" || res.Mismatch == "" {
		t.Fatalf("winner %q mismatch %q", res.Winner, res.Mismatch)
	}
}

func TestWinMessageNamesTheWinner(t *testing.T) {
	lines := append(append([]string{}, chat...), "Red won the game!")
	// Red cannot be Blue, the eliminated team, so Red is Team A and the win message agrees.
	res := Analyze(lines, profile(t), roster, "")
	if res.Winner != "Team A" || !strings.Contains(res.WinnerBy, "win message") {
		t.Fatalf("winner %q by %q", res.Winner, res.WinnerBy)
	}
}

func TestNoMVPWithoutKillsOrBeds(t *testing.T) {
	res := Analyze([]string{"⚠ Team Blue has been eliminated from the game!"}, profile(t), roster, "")
	if res.MVP.Name != "" {
		t.Fatalf("mvp %+v with nothing to judge by", res.MVP)
	}
}

func TestMVPFallsBackToBedsThenNone(t *testing.T) {
	res := Analyze([]string{"Blue Team's Bed has been destroyed by bummyyyyy"}, profile(t), roster, "")
	if res.MVP.Name != "bummyyyyy" || !strings.Contains(res.MVPBy, "bed") {
		t.Fatalf("mvp %+v by %q", res.MVP, res.MVPBy)
	}
	tied := Analyze([]string{"deloid_AK has been killed by bummyyyyy", "HT206 has been killed by EGIRLMEI"}, profile(t), roster, "")
	if tied.MVP.Name != "" {
		t.Fatalf("a tie gave %+v", tied.MVP)
	}
}

func TestNoChatMeansNoEvidence(t *testing.T) {
	res := Analyze([]string{"Create or join a guild and", "BedWars Thu, Nov 05", "play.pika-network.net"}, profile(t), roster, "Team A")
	if res.HasEvidence() {
		t.Fatalf("evidence from a lobby: %+v", res)
	}
}

func TestResolveToleratesAMisreadLetterButNotAmbiguity(t *testing.T) {
	if m := resolve("HT2O6", roster, true); m == nil || m.Name != "HT206" {
		t.Fatalf("misread name -> %v", m)
	}
	if m := resolve("HT2O6", roster, false); m != nil {
		t.Fatal("a stray word was matched loosely")
	}
	twins := []Member{{"Player1", "Team A"}, {"Player2", "Team B"}}
	if m := resolve("Player3", twins, true); m != nil {
		t.Fatalf("ambiguous name -> %v", m)
	}
}
