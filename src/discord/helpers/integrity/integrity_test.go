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
	over, comps := Overview(rep, "", 1)
	if len(comps) < 2 || len(comps) > 5 {
		t.Errorf("%d component rows", len(comps))
	}
	for name, e := range map[string]*discordgo.MessageEmbed{
		"overview": over, "detail": Detail(rep, 1, rep.Findings[0], hits), "player": PlayerView(rep, "alpha", rep), "game": GameView(rep, 3),
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
	if e, comps := Overview(rep, "", 1); !strings.Contains(e.Description, "Nothing stands out") || comps != nil {
		t.Errorf("overview: %q %v", e.Description, comps)
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

func TestOverviewPagesAndFilters(t *testing.T) {
	rep := bigReport() // ten critical win-trading findings
	rep.Findings[3].Kind, rep.Findings[4].Kind = "boosting", "boosting"
	if Pages(rep, "") != 2 || Pages(rep, "boosting") != 1 || Pages(rep, "throwing") != 1 {
		t.Fatalf("pages: %d %d %d", Pages(rep, ""), Pages(rep, "boosting"), Pages(rep, "throwing"))
	}
	first, _ := Overview(rep, "", 1)
	second, comps := Overview(rep, "", 2)
	if len(first.Fields) != PerPage+1 || len(second.Fields) != PerPage { // the first page also lists the most flagged players
		t.Errorf("fields: %d on page 1, %d on page 2", len(first.Fields), len(second.Fields))
	}
	if !strings.Contains(second.Footer.Text, "Page 2/2") || !strings.Contains(second.Fields[0].Name, "#6") {
		t.Errorf("page 2: %q / %q", second.Footer.Text, second.Fields[0].Name)
	}
	if len(comps) != 3 { // page buttons, the menu that opens a finding, the kind filter
		t.Errorf("%d component rows on page 2", len(comps))
	}
	only, _ := Overview(rep, "boosting", 1)
	if len(only.Fields) != 2 || !strings.Contains(only.Footer.Text, "Boosting only") || !strings.Contains(only.Fields[0].Name, "#4") {
		t.Errorf("boosting only: %+v", only.Fields)
	}
	if g := gamesRange([]int{26966, 26464, 26469}); g != "3 games (#26464–#26966)" {
		t.Errorf("games range: %s", g)
	}
}
