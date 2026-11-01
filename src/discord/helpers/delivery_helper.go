package helpers

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

// How a server wants the outcome of its games delivered. It lives in the server profile (Game phases > Post-game > Results and alerts):
//
//	profile.delivery.results = { channel_id, mode }   where the match results card goes and which card: "banner" (the server's own results
//	                                                  banner, else the built-in one), "builtin", or "off"
//	profile.delivery.alerts  = { channel_id, role_id, events: { key: bool } }  a staff channel told when something needs eyes: a player
//	                                                  refused a queue, a game voided or scored, the hosting bot lost a match
//
// An alert is sent when its channel is set and its event has not been switched off (events not listed are on).

// delivery is the parsed profile.delivery.
type delivery struct {
	ResultsChannel string
	ResultsMode    string
	ResultsOff     bool // the "Send results" toggle is off: the results banner is not posted, in any mode
	AlertsChannel  string
	AlertRole      string
	Alerts         map[string]bool
}

// AlertKinds are the events a staff alert can be about, with the title and colour each is posted with.
var AlertKinds = map[string]struct {
	Title string
	Color int
}{
	"queue_denied":  {"Queue denied", 0xf59e0b},
	"game_voided":   {"Game voided", 0xef4444},
	"game_scored":   {"Game scored", 0x22c55e},
	"tracking_lost": {"Match tracking lost", 0xf97316},
}

// resultsChannel is where the results card goes: the channel chosen in the profile, else the game's own chat.
func (f guildFlow) resultsChannel(gameChat string) string {
	if id := strings.TrimSpace(f.Delivery.ResultsChannel); id != "" {
		return id
	}
	return gameChat
}

// ResultsChannelFor is where a game's results go: the channel the profile names, else the guild's game_results channel (Events > host game),
// else the game's own chat, which is cleaned up when the game ends.
func ResultsChannelFor(guildID, gameChat string) string {
	logs := EventRequirementID(guildID, HostGameEventName, types.ReqChannel, types.GameResultsKey)
	if strings.TrimSpace(logs) == "" {
		logs = gameChat
	}
	return flowOf(guildID).resultsChannel(logs)
}

// alertOn says whether staff are told about this kind of event.
func (f guildFlow) alertOn(key string) bool {
	if strings.TrimSpace(f.Delivery.AlertsChannel) == "" {
		return false
	}
	on, listed := f.Delivery.Alerts[key]
	return !listed || on
}

// StaffAlert posts a short embed in the guild's alerts channel when the profile asks for that kind of alert. It never blocks or fails
// what raised it: an undeliverable alert is only logged.
func StaffAlert(s *discordgo.Session, guildID, key, description string, fields ...*discordgo.MessageEmbedField) {
	if s == nil {
		return
	}
	flow := flowOf(guildID)
	kind, known := AlertKinds[key]
	if !known {
		return
	}
	embed := &discordgo.MessageEmbed{Title: kind.Title, Description: description, Color: kind.Color, Fields: fields}
	if strings.TrimSpace(flow.Delivery.AlertsChannel) == "" { // the profile names none: the shared game_alerts channel
		flow.Delivery.AlertsChannel = EventRequirementID(guildID, HostGameEventName, types.ReqChannel, types.GameAlertsKey)
	}
	if !flow.alertOn(key) {
		return
	}
	send := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}}
	if role := strings.TrimSpace(flow.Delivery.AlertRole); role != "" {
		send.Content = "<@&" + role + ">"
		send.AllowedMentions = &discordgo.MessageAllowedMentions{Roles: []string{role}}
	}
	if _, err := s.ChannelMessageSendComplex(flow.Delivery.AlertsChannel, send); err != nil {
		log.Printf("staff alert %s for guild %s: %v", key, guildID, err)
	}
}

// scoreSummary is one line of who won and what the MVP got, for the "game scored" alert.
func scoreSummary(g Game) string {
	winners := []string{}
	for _, r := range g.Results {
		if r.Win {
			winners = append(winners, "<@"+r.UserID+">")
		}
	}
	text := fmt.Sprintf("Game #%d (%s on %s) was scored.", g.Number, g.Mode.Name, g.Map)
	if len(winners) > 0 {
		text += " Winners: " + strings.Join(winners, ", ")
	}
	return text
}
