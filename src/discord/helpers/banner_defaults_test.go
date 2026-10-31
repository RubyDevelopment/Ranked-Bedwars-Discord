package helpers

import (
	"bytes"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"image/png"
	"os"
	"testing"
)

func decoded(t *testing.T, b *Banner, v Vars) (w, h int) {
	t.Helper()
	data, err := RenderBanner(b, v)
	if err != nil {
		t.Fatalf("render %s: %v", b.ID, err)
	}
	if dir := os.Getenv("BANNER_OUT"); dir != "" {
		_ = os.WriteFile(dir+"/"+b.ID+".png", data, 0o644)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode %s: %v", b.ID, err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func TestDefaultBannersRender(t *testing.T) {
	stats := Vars{"player.name": "Alice", "stats.rank": "Gold", "stats.position": "7", "stats.elo": "1432", "stats.highest_elo": "1500", "stats.wins": "20", "stats.losses": "10",
		"stats.wlr": "2.00", "stats.win_streak": "3", "stats.kills": "300", "stats.final_kills": "80", "stats.beds_destroyed": "40", "stats.mvp_count": "6",
		"season.name": "Season 4", "stats.games_played": "30", "stats.kdr": "1.50", "stats.last_played": "2026-10-30"}
	if w, h := decoded(t, DefaultBanner("stats"), stats); w != 1200 || h != 500 {
		t.Errorf("stats is %dx%d", w, h)
	}
	lb := Vars{"lb.title": "Season 4 Leaderboard", "page.number": "1", "page.total": "2"}
	for n, name := range []string{"Alice", "Bob", "Cara"} {
		EntryVars(lb, "lb."+string(rune('1'+n))+".", n+1, name, "", &statsRow)
	}
	decoded(t, DefaultBanner("leaderboard"), lb)
	if DefaultBanner("profile") != nil {
		t.Error("only stats and leaderboard have built-in cards")
	}
}

func TestDefaultResultsBannerFitsTheGame(t *testing.T) {
	team := func(name string, win bool, n int) ResultsTeam {
		t := ResultsTeam{Name: name, Win: win}
		for i := 0; i < n; i++ {
			t.Players = append(t.Players, ResultsPlayer{Name: name + string(rune('A'+i)), RankBefore: "Silver", RankAfter: "Gold", Before: 1000, After: 1025, Change: 25, MVP: i == 0 && win})
			if !win {
				t.Players[i].Change, t.Players[i].After = -18, 982
			}
		}
		return t
	}
	small := DefaultResultsBanner([]ResultsTeam{team("Team A", true, 2), team("Team B", false, 2)})
	big := DefaultResultsBanner([]ResultsTeam{team("Team A", true, 4), team("Team B", false, 4), team("Team C", false, 4), team("Team D", false, 4)})
	vars := ResultsVars(12, "Season 4", "4v4", "Aquarium", []ResultsTeam{team("Team A", true, 2), team("Team B", false, 2)})
	if vars["t1.result"] != "VICTORY" || vars["t2.p1.change"] != "-18" || vars["t1.p1.mvp"] == "" {
		t.Errorf("vars: %v", vars)
	}
	decoded(t, small, vars)
	_, hSmall := decoded(t, small, vars)
	_, hBig := decoded(t, big, ResultsVars(12, "Season 4", "4v4", "Aquarium", []ResultsTeam{team("Team A", true, 4), team("Team B", false, 4), team("Team C", false, 4), team("Team D", false, 4)}))
	if hBig <= hSmall {
		t.Errorf("a bigger game needs a taller card: %d vs %d", hBig, hSmall)
	}
}

var statsRow = models.PlayerStatsModel{Elo: 1400, Wins: 10, Losses: 5, Rank: "Gold"}
