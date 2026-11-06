package integrity

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestSuspiciousWords(t *testing.T) {
	for _, s := range []string{"i'll lose this one for you", "let you win", "im throwing this game", "its your turn to win", "dont fight just sit out", "ill carry you", "this is boosting lol", "wintrade?", "my alt account"} {
		if !suspicious.MatchString(s) {
			t.Errorf("%q should match", s)
		}
	}
	for _, s := range []string{"gg", "nice bed", "boosters are expensive", "throw a pearl", "he is a carrot"} {
		if suspicious.MatchString(s) {
			t.Errorf("%q should not match", s)
		}
	}
}

func bigReport() *Report {
	f := Finding{Kind: "win_trading", Subtype: "alternating", Severity: "critical", Players: []uint{1, 2}, P: 1e-9, Q: 1e-7,
		Summary: strings.Repeat("a very long summary ", 40), Reasoning: strings.Repeat("because the winner changed every time. ", 80),
		Caveats: []string{strings.Repeat("it could be luck ", 100), "or friends"}, Games: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}}
	for range 30 {
		f.Evidence = append(f.Evidence, Evidence{Label: strings.Repeat("label ", 60), Value: strings.Repeat("value ", 300), Games: f.Games})
	}
	rep := &Report{GamesAnalysed: 100, Names: map[uint]string{1: "alpha", 2: "beta"}, Findings: []Finding{f, f, f, f, f, f, f, f, f, f}}
	for i := range 20 {
		rep.Risk = append(rep.Risk, Risk{PlayerID: uint(i), Name: strings.Repeat("n", 16), Score: 8, Findings: 2, Worst: "critical", Strikes: 1})
	}
	return rep
}

// Discord refuses an embed over its limits, so a report with long findings must still fit them.
func TestEmbedsStayWithinDiscordLimits(t *testing.T) {
	rep := bigReport()
	hits := []Hit{{Game: 1, Source: "discord", Who: "alpha", Text: strings.Repeat("let you win ", 50)}, {Game: 2, Source: "minecraft", Who: "beta", Text: "i'll lose"}}
	for name, e := range map[string]*discordgo.MessageEmbed{
		"overview": Overview(rep, ""), "detail": Detail(rep, 1, rep.Findings[0], hits), "player": PlayerView(rep, "alpha", rep), "game": GameView(rep, 3),
	} {
		total := len([]rune(e.Title)) + len([]rune(e.Description))
		if len([]rune(e.Title)) > 256 || len([]rune(e.Description)) > 4096 || len(e.Fields) > 25 {
			t.Errorf("%s: title %d, description %d, %d fields", name, len([]rune(e.Title)), len([]rune(e.Description)), len(e.Fields))
		}
		for _, f := range e.Fields {
			total += len([]rune(f.Name)) + len([]rune(f.Value))
			if len([]rune(f.Name)) > 256 || len([]rune(f.Value)) > 1024 || f.Name == "" || f.Value == "" {
				t.Errorf("%s: a field is %d / %d characters", name, len([]rune(f.Name)), len([]rune(f.Value)))
			}
		}
		if total > 6000 {
			t.Errorf("%s: %d characters in all, over 6000", name, total)
		}
	}
}

func TestEmptyReportsReadClearly(t *testing.T) {
	rep := &Report{GamesAnalysed: 50}
	if e := Overview(rep, ""); !strings.Contains(e.Description, "Nothing stands out") {
		t.Errorf("overview: %q", e.Description)
	}
	if e := PlayerView(rep, "alpha", rep); !strings.Contains(e.Description, "Nothing stands out") {
		t.Errorf("player: %q", e.Description)
	}
	if e := GameView(rep, 5); !strings.Contains(e.Description, "not part of any finding") {
		t.Errorf("game: %q", e.Description)
	}
}

func TestFindingNumbersMatchTheReport(t *testing.T) {
	rep := &Report{Findings: []Finding{{Kind: "boosting", Summary: "a"}, {Kind: "throwing", Summary: "b"}}}
	if rep.Number(rep.Findings[1]) != 2 || rep.Number(Finding{Kind: "x", Summary: "y"}) != 0 {
		t.Error("numbers do not follow the report's order")
	}
	if !(Finding{Severity: "critical"}).AtLeast("high") || (Finding{Severity: "medium"}).AtLeast("high") {
		t.Error("severity comparison")
	}
}
