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

// resultsPNG draws the match results card of a scored game, or returns nil when there is none to post: the server turned it off, the
// scored embed switched it off with "banner": false, or no player was rated. A server's own "results" banner (Banner Builder) is used
// when it has one and can be drawn, the built-in card otherwise.
func (d *gameDiscord) resultsPNG(g Game) []byte {
	flow := flowOf(g.GuildID)
	if flow.Delivery.ResultsMode == "off" {
		return nil
	}
	if t, ok := flow.embedFor("scored"); ok && t.Banner != nil && !*t.Banner {
		return nil
	}
	teams := resultsTeams(g)
	if len(teams) == 0 {
		return nil
	}
	season := "Season"
	if key, _, err := ActiveSeasonKey(g.GuildID); err == nil && key != "" {
		season = key
	}
	vars := ResultsVars(g.Number, season, g.Mode.Name, g.Map, teams)
	if flow.Delivery.ResultsMode != "builtin" {
		if custom := LoadBanners(g.GuildID).Choose("results", Viewer{}, ""); custom != nil {
			if png, err := RenderBanner(custom, vars); err == nil {
				return png
			} else {
				log.Printf("results banner of the server for game %d (using the built-in card): %v", g.ID, err)
			}
		}
	}
	png, err := RenderBanner(DefaultResultsBanner(teams), vars)
	if err != nil {
		log.Printf("results banner for game %d: %v", g.ID, err)
		return nil
	}
	return png
}

// sendResultsPNG posts the card on its own, where the server wants results (the profile's channel, else game_logs, else the game's chat).
func (d *gameDiscord) sendResultsPNG(s *discordgo.Session, g Game, png []byte) {
	channel := ResultsChannelFor(g.GuildID, g.Channels.Text)
	if channel == "" {
		return
	}
	if _, err := s.ChannelMessageSendComplex(channel, &discordgo.MessageSend{Files: []*discordgo.File{{Name: "results.png", ContentType: "image/png", Reader: bytes.NewReader(png)}}}); err != nil {
		log.Printf("post results banner for game %d: %v", g.ID, err)
	}
}
