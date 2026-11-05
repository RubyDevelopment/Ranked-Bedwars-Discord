package proofread

import "testing"

// A profile stores the server's "⚠" before a message garbled, and a roster name may carry the server nickname's tags ("[155] kingyamx").
// A screenshot reads that symbol however it likes, so the elimination alone, with no win line, must still tell the winner.
func TestGarbledSymbolAndTaggedNames(t *testing.T) {
	garbled := "â\u009a " // "⚠" as it sits in the stored profile
	track := func(id, pattern, kind string, fields map[string]string) Rule {
		return Rule{ID: id, On: "chat", Match: Matcher{Type: "regex", Value: pattern}, Actions: []Action{{Type: "track", Kind: kind, Fields: fields}}}
	}
	profile := Profile{
		Rules: []Rule{
			track("bed", `^(?:`+garbled+`\s+)?(?<team>[A-Za-z]+)\s+Team'?s?\s+Bed\b.*?\b(?:by|from)\s+(?<player>[A-Za-z0-9_]{3,16})\b`, "bed", map[string]string{"team": "{team}", "player": "{player}"}),
			track("kill", `^(?<victim>[A-Za-z0-9_]{3,16}) has been killed by (?<killer>[A-Za-z0-9_]{3,16})`, "kill", map[string]string{"victim": "{victim}", "killer": "{killer}"}),
			track("elim", `^`+garbled+`\s+Team\s+(?<team>[A-Za-z]+)\s+has been eliminated`, "eliminated", map[string]string{"team": "{team}"}),
		},
		FinalKill: &Matcher{Type: "includes", Value: "FINAL KILL"},
	}
	var roster []Member
	for _, m := range []Member{{"[155] kingyamx", "Team B", ""}, {"[265] LavaIl", "Team B", ""}, {"[595] Lennox__", "Team B", ""}, {"[390] vyncerie", "Team A", ""}, {"[315] HitNelson099", "Team A", ""}, {"HalfOfUs", "Team A", ""}} {
		roster = append(roster, Member{PlainName(m.Name), m.Team, ""})
	}
	lines := []string{
		"LavaIl has been killed by vyncerie",
		"Red Team's Bed has been destroyed by Lennox__",
		"vyncerie has been killed by kingyamx FINAL KILL",
		"©. Team Red has been eliminated from the game!",
	}
	res := Analyze(lines, profile, roster, "Team B", nil)
	if res.Winner != "Team B" {
		t.Fatalf("winner=%q (%s), want Team B", res.Winner, res.WinnerBy)
	}
	if res.MVP.Name != "kingyamx" && res.MVP.Name != "vyncerie" {
		t.Fatalf("mvp=%q", res.MVP.Name)
	}
}

// A player the chat names by another name they go by (their Minecraft name, not their nickname) is still that player, and counted once.
func TestPlayerFoundUnderAnyOfTheirNames(t *testing.T) {
	roster := []Member{
		{"kingyamx", "Team B", "u1"}, {"King Yam", "Team B", "u1"},
		{"vyncerie", "Team A", "u2"}, {"Vynce", "Team A", "u2"},
	}
	if m := resolve("King", roster, false); m != nil {
		t.Fatalf("a part of a name matched: %+v", m)
	}
	m := resolve("kingyamx", roster, false)
	if m == nil || m.ID != "u1" || m.Name != "kingyamx" {
		t.Fatalf("got %+v", m)
	}
	if m := resolve("Vynce", roster, false); m == nil || m.Name != "vyncerie" {
		t.Fatalf("alias not reported by the first name: %+v", m)
	}
}
