package match

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/banner"
	"ranked-bedwars/src/discord/helpers/ratings"

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
func resultsTeams(g Game, outcome string) []banner.ResultsTeam {
	byUser := map[string]ratings.EloResult{}
	for _, r := range g.Results {
		byUser[r.UserID] = r
	}
	unrated := outcome == outcomeVoid || outcome == outcomeCasual
	// Nothing was scored, so each player's card line shows where they stand now: asked for all of them at once, not one by one.
	type standing struct {
		rank string
		elo  int
	}
	standings := map[string]standing{}
	if unrated {
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, id := range g.PlayerIDs() {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if st, err := database.Must().Stats.Get(g.GuildID, id, 0); err == nil {
					mu.Lock()
					standings[id] = standing{st.Rank, st.Elo}
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
	}
	var out []banner.ResultsTeam
	for _, name := range g.Teams {
		team := banner.ResultsTeam{Name: name, Label: outcomeLabels[outcome]}
		for _, p := range g.TeamPlayers(name) {
			r, ok := byUser[p.UserID]
			if !ok && !unrated {
				continue
			}
			team.Win = r.Win
			if unrated {
				// Nothing was scored: show where the player stands now (rank and ELO on both sides, no change).
				if st, ok := standings[p.UserID]; ok {
					r.RankBefore, r.RankAfter, r.Before, r.After = st.rank, st.rank, st.elo, st.elo
				}
			}
			team.Players = append(team.Players, banner.ResultsPlayer{
				Name: p.Username, Head: banner.HeadURL(p.Username), RankBefore: r.RankBefore, RankAfter: r.RankAfter,
				Before: r.Before, After: r.After, Change: r.Change, MVP: r.MVP, Unrated: unrated,
			})
		}
		if len(team.Players) > 0 {
			out = append(out, team)
		}
	}
	return out
}

// chooseResultsBanner is the server's banner for this outcome: the one made for it, else the win card, else nil (use the built-in).
func chooseResultsBanner(doc *banner.Doc, outcome string) *banner.Banner {
	for _, command := range []string{"results:" + outcome, "results"} {
		if list := doc.Eligible(command, banner.Viewer{}); len(list) > 0 {
			return list[0]
		}
	}
	return nil
}

// minResultsRows is how many player rows a team box of a custom banner always has; a seat nobody sits in is an empty row
// ({{#if name}} in the HTML draws the row's contents only for a real player).
const minResultsRows = 4

// padResultsVars returns vars with every team given at least rows player entries, the missing ones blank.
func padResultsVars(vars banner.Vars, teams []banner.ResultsTeam, rows int) banner.Vars {
	out := make(banner.Vars, len(vars)+rows*len(teams)*8)
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
	if key, _, err := helpers.ActiveSeasonKey(g.GuildID); err == nil && key != "" {
		season = key
	}
	vars := banner.ResultsVars(g.Number, season, g.Mode.Name, g.Map, teams)
	// The mode's settings, for a banner that draws things only when they apply ({{#if match.elo == on}}).
	vars["match.outcome"], vars["match.status"] = outcome, outcomeStatus[outcome]
	showElo := g.Mode.Rated() && outcome != outcomeVoid && outcome != outcomeCasual
	vars["match.elo"], vars["match.stats"], vars["match.party"] = onOff(showElo), onOff(g.Mode.IsStatsTrackingEnabled), onOff(g.Mode.IsPartyQueueEnabled)
	if flow.Delivery.ResultsMode != "builtin" {
		if custom := chooseResultsBanner(banner.LoadBanners(g.GuildID), outcome); custom != nil {
			if png, err := banner.RenderBanner(custom, padResultsVars(vars, teams, minResultsRows)); err == nil {
				return png
			} else {
				log.Printf("results banner of the server for game %d (using the built-in card): %v", g.ID, err)
			}
		}
	}
	png, err := banner.RenderBanner(banner.DefaultResultsBannerFor(teams, showElo), vars)
	if err != nil {
		log.Printf("results banner for game %d: %v", g.ID, err)
		return nil
	}
	return png
}

// resultsSummary is the text that goes with the results card: who won and who lost (the players are pinged), or how the
// game ended when nobody did.
func resultsSummary(g Game, outcome string) string {
	mentions := func(ids []string) string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			out = append(out, "<@"+id+">")
		}
		return strings.Join(out, " ")
	}
	head := "**" + helpers.MatchLink(g.GuildID, g.SeasonID, g.Number, g.Total, fmt.Sprintf("Game #%d", g.Number)) + "**"
	if outcome == outcomeWin {
		var winners, losers []string
		for _, r := range g.Results {
			if r.Win {
				winners = append(winners, r.UserID)
			} else {
				losers = append(losers, r.UserID)
			}
		}
		return fmt.Sprintf("%s\n**Winners:** %s\n**Losers:** %s", head, mentions(winners), mentions(losers))
	}
	label := map[string]string{outcomeVoid: "was voided", outcomeTie: "ended in a tie", outcomeCasual: "has finished"}[outcome]
	return fmt.Sprintf("%s %s\n**Players:** %s", head, label, mentions(g.PlayerIDs()))
}

// sendResultsPNG posts the card with its summary text, where the server wants results (the profile's channel, else game_results, else the game's chat).
func (d *gameDiscord) sendResultsPNG(s *discordgo.Session, g Game, png []byte, outcome string) {
	channel := ResultsChannelFor(g.GuildID, g.Channels.Text)
	if channel == "" {
		return
	}
	if _, err := s.ChannelMessageSendComplex(channel, &discordgo.MessageSend{
		Content:         resultsSummary(g, outcome),
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeUsers}},
		Files:           []*discordgo.File{{Name: "results.png", ContentType: "image/png", Reader: bytes.NewReader(png)}},
	}); err != nil {
		log.Printf("post results banner for game %d: %v", g.ID, err)
	}
}
