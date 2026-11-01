package helpers

import (
	"bytes"
	"fmt"
	"log"

	"github.com/bwmarrin/discordgo"
)

// How a game ended, for the results card. Each has its own banner in the Banner Builder: "Match results" is the win card, and
// "results:tie", "results:void" and "results:casual" are the others (a variant the server did not design falls back to the win card).
const (
	outcomeWin    = "win"    // a team won
	outcomeTie    = "tie"    // scored, but nobody won
	outcomeVoid   = "void"   // voided: it was a game, but nothing was scored
	outcomeCasual = "casual" // finished with no score: the mode does not rate or has no winner
)

// outcomeLabels replace VICTORY / DEFEAT on a team when the game has no single winner.
var outcomeLabels = map[string]string{outcomeTie: "TIE", outcomeVoid: "VOIDED", outcomeCasual: "COMPLETED"}

// outcomeStatus is the {match.status} line of the card.
var outcomeStatus = map[string]string{outcomeWin: "Final", outcomeTie: "Tie", outcomeVoid: "Voided", outcomeCasual: "Casual game"}

// resultsOutcome says how g ended. voided is true for the voided event.
func resultsOutcome(g Game, voided bool) string {
	if voided {
		return outcomeVoid
	}
	if len(g.Results) == 0 {
		return outcomeCasual
	}
	for _, r := range g.Results {
		if r.Win {
			return outcomeWin
		}
	}
	return outcomeTie
}

// resultsTeams lays a game out for the results card: its teams in order, the players of each with their rank and ELO change.
// A scored game lists the players that were rated; a void or casual game has no rating, so every player is listed with none.
func resultsTeams(g Game, outcome string) []ResultsTeam {
	byUser := map[string]EloResult{}
	for _, r := range g.Results {
		byUser[r.UserID] = r
	}
	unrated := outcome == outcomeVoid || outcome == outcomeCasual
	var out []ResultsTeam
	for _, name := range g.Teams {
		team := ResultsTeam{Name: name, Label: outcomeLabels[outcome]}
		for _, p := range g.TeamPlayers(name) {
			r, ok := byUser[p.UserID]
			if !ok && !unrated {
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

// chooseResultsBanner is the server's banner for this outcome: the one made for it, else the win card, else nil (use the built-in).
func chooseResultsBanner(doc *Doc, outcome string) *Banner {
	for _, command := range []string{"results:" + outcome, "results"} {
		if list := doc.Eligible(command, Viewer{}); len(list) > 0 {
			return list[0]
		}
	}
	return nil
}

// minResultsRows is how many player rows a team box of a custom banner always has; a seat nobody sits in is an empty row
// ({{#if name}} in the HTML draws the row's contents only for a real player).
const minResultsRows = 4

// padResultsVars returns vars with every team given at least rows player entries, the missing ones blank.
func padResultsVars(vars Vars, teams []ResultsTeam, rows int) Vars {
	out := make(Vars, len(vars)+rows*len(teams)*8)
	for k, v := range vars {
		out[k] = v
	}
	for t, team := range teams {
		for p := len(team.Players); p < rows; p++ {
			q := fmt.Sprintf("t%d.p%d.", t+1, p+1)
			for _, f := range []string{"name", "head", "rank", "rank_before", "rank_after", "before", "after", "change", "mvp"} {
				out[q+f] = ""
			}
		}
	}
	return out
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// resultsPNG draws the results card of a game that ended as outcome, or returns nil when there is none to post: "Send results" is off
// for the game's mode, the server turned the card off, the scored embed switched it off with "banner": false, or nobody is on a team.
// A server's own banner (Banner Builder) is used when it has one and can be drawn, the built-in card otherwise.
func (d *gameDiscord) resultsPNG(g Game, outcome string) []byte {
	flow := flowOf(g.GuildID)
	if (g.Mode.SendResults != nil && !*g.Mode.SendResults) || flow.Delivery.ResultsMode == "off" {
		return nil
	}
	if t, ok := flow.embedFor("scored"); ok && t.Banner != nil && !*t.Banner {
		return nil
	}
	teams := resultsTeams(g, outcome)
	if len(teams) == 0 {
		return nil
	}
	season := "Season"
	if key, _, err := ActiveSeasonKey(g.GuildID); err == nil && key != "" {
		season = key
	}
	vars := ResultsVars(g.Number, season, g.Mode.Name, g.Map, teams)
	// The mode's settings, for a banner that draws things only when they apply ({{#if match.elo == on}}).
	vars["match.outcome"], vars["match.status"] = outcome, outcomeStatus[outcome]
	showElo := g.Mode.IsEloGainEnabled && outcome != outcomeVoid && outcome != outcomeCasual
	vars["match.elo"], vars["match.stats"], vars["match.party"] = onOff(showElo), onOff(g.Mode.IsStatsTrackingEnabled), onOff(g.Mode.IsPartyQueueEnabled)
	if flow.Delivery.ResultsMode != "builtin" {
		if custom := chooseResultsBanner(LoadBanners(g.GuildID), outcome); custom != nil {
			if png, err := RenderBanner(custom, padResultsVars(vars, teams, minResultsRows)); err == nil {
				return png
			} else {
				log.Printf("results banner of the server for game %d (using the built-in card): %v", g.ID, err)
			}
		}
	}
	png, err := RenderBanner(DefaultResultsBannerFor(teams, showElo), vars)
	if err != nil {
		log.Printf("results banner for game %d: %v", g.ID, err)
		return nil
	}
	return png
}

// sendResultsPNG posts the card on its own, where the server wants results (the profile's channel, else game_results, else the game's chat).
func (d *gameDiscord) sendResultsPNG(s *discordgo.Session, g Game, png []byte) {
	channel := ResultsChannelFor(g.GuildID, g.Channels.Text)
	if channel == "" {
		return
	}
	if _, err := s.ChannelMessageSendComplex(channel, &discordgo.MessageSend{Files: []*discordgo.File{{Name: "results.png", ContentType: "image/png", Reader: bytes.NewReader(png)}}}); err != nil {
		log.Printf("post results banner for game %d: %v", g.ID, err)
	}
}
