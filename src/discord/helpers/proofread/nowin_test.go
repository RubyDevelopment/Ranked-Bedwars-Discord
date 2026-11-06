package proofread

import "testing"

func noWinProfile() Profile {
	track := func(id, pattern, kind string, fields map[string]string) Rule {
		return Rule{ID: id, On: "chat", Match: Matcher{Type: "regex", Value: pattern}, Actions: []Action{{Type: "track", Kind: kind, Fields: fields}}}
	}
	return Profile{
		Rules: []Rule{
			track("bed", `^(?<team>[A-Za-z]+)\s+Team'?s?\s+Bed\b.*?\b(?:by|from)\s+(?<player>[A-Za-z0-9_]{3,16})\b`, "bed", map[string]string{"team": "{team}", "player": "{player}"}),
			track("kill", `^(?<victim>[A-Za-z0-9_]{3,16}) has been killed by (?<killer>[A-Za-z0-9_]{3,16})`, "kill", map[string]string{"victim": "{victim}", "killer": "{killer}"}),
		},
		FinalKill:     &Matcher{Type: "includes", Value: "FINAL KILL"},
		InferKills:    true,
		InferKillSkip: []string{":", " > "},
	}
}

var noWinRoster = []Member{
	{"HITSELECTJESUS", "Team A", ""}, {"Tropinzi_", "Team A", ""}, {"Darkfiscend", "Team A", ""}, {"stoaart_", "Team A", ""},
	{"Lennox__", "Team B", ""}, {"Nyqzo", "Team B", ""}, {"_xsevilx_", "Team B", ""}, {"3no", "Team B", ""},
}

// A screenshot taken after the game with the win message scrolled away: only a bed break, a few kills and the last final kill are in view.
func TestWinnerFromTheLastFinalKillWhenNoWinMessageShows(t *testing.T) {
	lines := []string{
		"3no has been killed by Tropinzi_",
		"Red Team's Bed has been destroyed by Tropinzi_",
		"_xsevilx_ has been killed by Nyqzo",
		"Nyqzo could not escape the cold of Darkfiscend FINAL KILL",
		"TEAM Darkfiscend: ems nou",
	}
	res := Analyze(lines, noWinProfile(), noWinRoster, "Team A", nil)
	if res.Winner != "Team A" || !res.Weak {
		t.Fatalf("winner=%q weak=%v by=%q", res.Winner, res.Weak, res.WinnerBy)
	}
	if res.Mismatch != "" {
		t.Fatalf("mismatch %q", res.Mismatch)
	}
}

// When every player of a team was final-killed in view, that team lost: firm, not a guess.
func TestWinnerWhenAWholeTeamWasFinalKilled(t *testing.T) {
	lines := []string{
		"Lennox__ has been killed by Tropinzi_ FINAL KILL",
		"Nyqzo has been killed by Darkfiscend FINAL KILL",
		"_xsevilx_ has been killed by stoaart_ FINAL KILL",
		"3no has been killed by Tropinzi_ FINAL KILL",
	}
	res := Analyze(lines, noWinProfile(), noWinRoster, "Team A", nil)
	if res.Winner != "Team A" || res.Weak {
		t.Fatalf("winner=%q weak=%v by=%q", res.Winner, res.Weak, res.WinnerBy)
	}
}

// A winner guessed from one kill is marked weak, and autoScoreBlocker never lets a weak one score a game.
func TestAGuessedWinnerIsMarkedWeak(t *testing.T) {
	res := Analyze([]string{"Nyqzo could not escape the cold of Darkfiscend FINAL KILL"}, noWinProfile(), noWinRoster, "Team A", nil)
	if !res.Weak || res.Winner == "" {
		t.Fatalf("expected a weak winner: %+v", res)
	}
}
