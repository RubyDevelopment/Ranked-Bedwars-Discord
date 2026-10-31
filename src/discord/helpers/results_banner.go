package helpers

import (
	"bytes"
	"log"

	"github.com/bwmarrin/discordgo"
)

// resultsTeams lays a scored game out for the results card: its teams in order, the players of each with their rank
// and ELO change. Teams without scored players (a game that was not rated) are left out.
func resultsTeams(g Game) []ResultsTeam {
	byUser := map[string]EloResult{}
	for _, r := range g.Results {
		byUser[r.UserID] = r
	}
	var out []ResultsTeam
	for _, name := range g.Teams {
		team := ResultsTeam{Name: name}
		for _, p := range g.TeamPlayers(name) {
			r, ok := byUser[p.UserID]
			if !ok {
				continue
			}
			team.Win = r.Win
			team.Players = append(team.Players, ResultsPlayer{
				Name: p.Username, Head: HeadURL(p.Username), RankBefore: r.RankBefore, RankAfter: r.RankAfter,
				Before: r.Before, After: r.After, Change: r.Change, MVP: r.MVP,
			})
		}
		if len(team.Players) > 0 {
			out = append(out, team)
		}
	}
	return out
}

// postResultsBanner draws the match results card for a scored game and posts it in the game's chat. A server's own
// "results" banner (Banner Builder) is used when it has one, the built-in card otherwise. The "scored" embed can switch it
// off with "banner": false. A card that cannot be drawn is logged and skipped: the scored embed already told the result.
func (d *gameDiscord) postResultsBanner(s *discordgo.Session, g Game) {
	if t, ok := flowOf(g.GuildID).embedFor("scored"); ok && t.Banner != nil && !*t.Banner {
		return
	}
	teams := resultsTeams(g)
	if len(teams) == 0 || g.Channels.Text == "" {
		return
	}
	b := LoadBanners(g.GuildID).Choose("results", Viewer{}, "")
	if b == nil {
		b = DefaultResultsBanner(teams)
	}
	season := "Season"
	if key, _, err := ActiveSeasonKey(g.GuildID); err == nil && key != "" {
		season = key
	}
	png, err := RenderBanner(b, ResultsVars(g.Number, season, g.Mode.Name, g.Map, teams))
	if err != nil {
		log.Printf("results banner for game %d: %v", g.ID, err)
		return
	}
	if _, err := s.ChannelMessageSendComplex(g.Channels.Text, &discordgo.MessageSend{Files: []*discordgo.File{{Name: "results.png", ContentType: "image/png", Reader: bytes.NewReader(png)}}}); err != nil {
		log.Printf("post results banner for game %d: %v", g.ID, err)
	}
}
