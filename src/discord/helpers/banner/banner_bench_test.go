package banner

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
)

// fakeImages replaces the network: every URL gets a deterministic picture (a skin-like figure for bodies, a face for heads).
func fakeImages(t testing.TB) {
	old := fetchImage
	fetchImage = func(url string) (image.Image, error) {
		h := fnv.New32a()
		h.Write([]byte(url))
		seed := h.Sum32()
		w, hgt := 64, 64
		if strings.Contains(url, "fullbody") || strings.Contains(url, "bust") {
			w, hgt = 180, 400
		}
		img := image.NewNRGBA(image.Rect(0, 0, w, hgt))
		base := color.NRGBA{uint8(seed), uint8(seed >> 8), uint8(seed >> 16), 255}
		for y := 0; y < hgt; y++ {
			for x := 0; x < w; x++ {
				c := base
				if (x/8+y/8)%2 == 0 {
					c.R, c.G, c.B = c.R/2, c.G/2, c.B/2
				}
				img.SetNRGBA(x, y, c)
			}
		}
		return img, nil
	}
	t.Cleanup(func() { fetchImage = old })
}

func sampleStats() Vars {
	st := &models.PlayerStatsModel{Rank: "Diamond", Elo: 1875, HighestElo: 1990, Wins: 412, Losses: 233, Kills: 5123, Deaths: 3011, FinalKills: 2210, BedsDestroyed: 640, BedsLost: 310, MvpCount: 88, WinStreak: 6, GamesPlayed: 645}
	v := Vars{
		"player.name": "Nexus720", "player.head": HeadURL("Nexus720"), "player.body": BodyURL("Nexus720"),
		"stats.wlr": Ratio(st.Wins, st.Losses), "stats.fkdr": Ratio(st.FinalKills, st.Deaths), "stats.bdr": Ratio(st.BedsDestroyed, st.BedsLost), "stats.last_played": "2026-11-03",
		"stats.rank": st.Rank, "stats.position": "14", "stats.elo": "1875", "stats.highest_elo": "1990", "stats.games_played": "645",
		"stats.wins": "412", "stats.losses": "233", "stats.win_rate": "64%", "stats.kills": "5123", "stats.final_kills": "2210", "stats.first_bloods": "190",
		"stats.deaths": "3011", "stats.assists": "900", "stats.kdr": "1.70", "stats.beds_destroyed": "640", "stats.beds_lost": "310", "stats.mvp_count": "88",
		"stats.win_streak": "6", "stats.highest_win_streak": "14", "stats.loss_streak": "0", "season.name": "Season 5", "season.id": "5",
	}
	return v
}

func sampleLeaderboard() Vars {
	v := Vars{"lb.title": "Season 5 Leaderboard", "lb.stat": "ELO", "lb.stat_short": "ELO", "page.number": "1", "page.total": "12"}
	names := []string{"Nexus720", "teebabi", "EBK", "Worst", "mynameisnotshort", "Steve", "Alex", "Herobrine", "Notch", "jeb_"}
	for n := 1; n <= 10; n++ {
		st := &models.PlayerStatsModel{Rank: []string{"Master", "Diamond", "Gold"}[n%3], Elo: 2400 - n*37, HighestElo: 2500 - n*20, Wins: 300 - n*11, Losses: 120 + n*7, Kills: 4000 - n*100, Deaths: 2500 + n*30, GamesPlayed: 420}
		EntryVars(v, fmt.Sprintf("lb.%d.", n), n, names[n-1], "", st)
		v[fmt.Sprintf("lb.%d.value", n)] = fmt.Sprint(st.Elo)
	}
	v["me.value"] = "1875"
	EntryVars(v, "me.", 14, "Nexus720", "", &models.PlayerStatsModel{Rank: "Diamond", Elo: 1875, HighestElo: 1990, Wins: 412, Losses: 233, Kills: 5123, Deaths: 3011})
	return v
}

func sampleResults(players int) (Vars, []ResultsTeam) {
	teams := []ResultsTeam{{Name: "Red", Win: true}, {Name: "Blue"}}
	for i := range teams {
		for p := 0; p < players; p++ {
			c := 24 - p*3
			if !teams[i].Win {
				c = -(20 - p*2)
			}
			teams[i].Players = append(teams[i].Players, ResultsPlayer{Name: fmt.Sprintf("Player%d_%d", i+1, p+1), Head: HeadURL(fmt.Sprintf("p%d%d", i, p)), RankBefore: "Gold", RankAfter: "Diamond", Before: 1500 + p*10, After: 1500 + p*10 + c, Change: c, MVP: p == 0 && i == 0})
		}
	}
	v := ResultsVars(1234, "Season 5", "4v4", "Lighthouse", teams)
	v["match.status"] = "Final"
	return v, teams
}

type sample struct {
	name string
	b    *Banner
	vars Vars
}

func samples() []sample {
	rv, rt := sampleResults(4)
	rv8, rt8 := sampleResults(8)
	return []sample{
		{"stats", DefaultBanner("stats"), sampleStats()},
		{"leaderboard", DefaultBanner("leaderboard"), sampleLeaderboard()},
		{"results4v4", DefaultResultsBannerFor(rt, true), rv},
		{"results8v8", DefaultResultsBannerFor(rt8, true), rv8},
	}
}

// TestDumpBanners writes every sample to $BANNER_OUT (when set) so two versions of the renderer can be compared by eye and by pixel.
func TestDumpBanners(t *testing.T) {
	dir := os.Getenv("BANNER_OUT")
	if dir == "" {
		t.Skip("BANNER_OUT not set")
	}
	fakeImages(t)
	_ = os.MkdirAll(dir, 0o755)
	for _, s := range samples() {
		png, err := RenderBanner(s.b, s.vars)
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, s.name+".png"), png, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %dx%d, %d KB", s.name, s.b.Width, s.b.Height, len(png)/1024)
	}
}

func BenchmarkRender(b *testing.B) {
	fakeImages(b)
	for _, s := range samples() {
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := RenderBanner(s.b, s.vars); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
