package match

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
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
	"tracking_lost": {"Match tracking lost", 0xf97316},
	"team_call":     {"Team call", 0x38bdf8},
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
	logs := helpers.EventRequirementID(guildID, helpers.HostGameEventName, types.ReqChannel, types.GameResultsKey)
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

// StaffAlertPosts says whether a staff alert of this kind is posted in a channel of the guild (an alerts channel is set and this kind is on).
func StaffAlertPosts(guildID, key string) bool {
	flow := flowOf(guildID)
	if _, known := AlertKinds[key]; !known {
		return false
	}
	if strings.TrimSpace(flow.Delivery.AlertsChannel) == "" {
		flow.Delivery.AlertsChannel = helpers.EventRequirementID(guildID, helpers.HostGameEventName, types.ReqChannel, types.GameAlertsKey)
	}
	return flow.alertOn(key)
}

// StaffAlertPinging is StaffAlert that also pings the given users (the players the alert is about) in the message.
func StaffAlertPinging(s *discordgo.Session, guildID, key string, pingUserIDs []string, description string, fields ...*discordgo.MessageEmbedField) {
	staffAlertPingingTo(s, guildID, key, "", pingUserIDs, description, fields...)
}

// staffAlertPingingTo is StaffAlertPinging posting to channel when one is given (a command's own alerts channel) instead of the profile's.
func staffAlertPingingTo(s *discordgo.Session, guildID, key, channel string, pingUserIDs []string, description string, fields ...*discordgo.MessageEmbedField) {
	if s == nil {
		return
	}
	flow := flowOf(guildID)
	kind, known := AlertKinds[key]
	if !known {
		return
	}
	embed := &discordgo.MessageEmbed{Title: kind.Title, Description: description, Color: kind.Color, Fields: fields}
	if channel != "" {
		flow.Delivery.AlertsChannel = channel
	}
	if strings.TrimSpace(flow.Delivery.AlertsChannel) == "" { // the profile names none: the shared game_alerts channel
		flow.Delivery.AlertsChannel = helpers.EventRequirementID(guildID, helpers.HostGameEventName, types.ReqChannel, types.GameAlertsKey)
	}
	if !flow.alertOn(key) {
		return
	}
	helpers.WithGuildFooterIconForGuild(s, guildID, embed)
	send := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}}
	mentions := &discordgo.MessageAllowedMentions{}
	var content []string
	if role := strings.TrimSpace(flow.Delivery.AlertRole); role != "" {
		content = append(content, "<@&"+role+">")
		mentions.Roles = []string{role}
	}
	for _, id := range alertPingUsers(guildID, pingUserIDs) {
		content = append(content, "<@"+id+">")
		mentions.Users = append(mentions.Users, id)
	}
	if len(content) > 0 {
		send.Content, send.AllowedMentions = strings.Join(content, " "), mentions
	}
	if _, err := s.ChannelMessageSendComplex(flow.Delivery.AlertsChannel, send); err != nil {
		log.Printf("staff alert %s for guild %s: %v", key, guildID, err)
	}
}

// alertPingUsers are those of ids who want to be pinged by alerts (everyone, unless they switched Ping On Alerts off in /settings or
// their settings cannot be read). The alert is still posted: only the ping is left out.
func alertPingUsers(guildID string, ids []string) []string {
	keep := make([]bool, len(ids))
	var wg sync.WaitGroup
	for n, id := range ids {
		keep[n] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			if view, err := database.Must().Player.Get(guildID, id); err == nil && view.Config != nil && !view.Config.IsPingOnAlertsEnabled {
				keep[n] = false
			}
		}()
	}
	wg.Wait()
	out := make([]string, 0, len(ids))
	for n, id := range ids {
		if keep[n] {
			out = append(out, id)
		}
	}
	return out
}

// voidAlert is the staff alert of a voided game: what the game was, who voided it, why, who was in it and what it means for ratings.
func voidAlert(g Game, reason string) (string, []*discordgo.MessageEmbedField) {
	who := "the system"
	if by := strings.TrimSpace(g.VoidedBy); by != "" {
		who = by
		if _, err := strconv.ParseUint(by, 10, 64); err == nil {
			who = "<@" + by + ">"
		}
	}
	title := fmt.Sprintf("Game #%d", g.Number)
	if g.Mode.Name != "" {
		title += " (" + g.Mode.Name + ")"
	}
	desc := fmt.Sprintf("%s was voided by %s.", title, who)

	progress := "Voided before the match started."
	if !g.StartedAt.IsZero() {
		progress = fmt.Sprintf("Voided %s into the match.", time.Since(g.StartedAt).Round(time.Second))
	}
	effect := "No wins, losses or stats were recorded for this game."
	if g.Mode.IsEloGainEnabled {
		effect = "No ELO, wins, losses or stats were recorded, and any already scored are reversed."
	}

	lines := func(players []GamePlayer) string {
		out := make([]string, 0, len(players))
		for _, p := range players {
			out = append(out, "<@"+p.UserID+"> (`"+p.Username+"`)")
		}
		return strings.Join(out, "\n")
	}
	fields := []*discordgo.MessageEmbedField{
		{Name: "Reason", Value: base.FirstNonEmpty(reason, "No reason given")},
		{Name: "Mode", Value: base.FirstNonEmpty(g.Mode.Name, "Unknown"), Inline: true},
		{Name: "Map", Value: base.FirstNonEmpty(g.Map, "Not picked"), Inline: true},
		{Name: "Host", Value: base.FirstNonEmpty(g.Host, "Automatic"), Inline: true},
		{Name: "Progress", Value: progress},
		{Name: "Effect", Value: effect},
	}
	teamed := false
	for _, name := range g.Teams {
		if players := g.TeamPlayers(name); len(players) > 0 {
			fields = append(fields, &discordgo.MessageEmbedField{Name: name, Value: lines(players), Inline: true})
			teamed = true
		}
	}
	if !teamed && len(g.Players) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: fmt.Sprintf("Players (%d)", len(g.Players)), Value: lines(g.Players)})
	}
	return desc, fields
}

// NotifyTeamCall tells a player they were added to a game's team voice channel (/call): in the guild's alerts channel, pinging
// them, as the old bot did. With no alerts channel set the
// player gets a direct message instead so they always hear about it.
func NotifyTeamCall(s *discordgo.Session, guildID, actorID, targetID, voiceID string, gameNumber int) {
	text := fmt.Sprintf("<@%s> added you to <#%s> for game **#%d**.", actorID, voiceID, gameNumber)
	// /call's own alerts channel comes first, then the game profile's, then the shared game alerts channel.
	callChannel, _ := helpers.RequirementSlotID(guildID, "call", types.ReqChannel, types.CallAlertsKey)
	flow := flowOf(guildID)
	channel := callChannel
	if channel == "" {
		channel = strings.TrimSpace(flow.Delivery.AlertsChannel)
	}
	if channel == "" {
		channel = helpers.EventRequirementID(guildID, helpers.HostGameEventName, types.ReqChannel, types.GameAlertsKey)
	}
	if channel != "" {
		staffAlertPingingTo(s, guildID, "team_call", callChannel, []string{targetID}, text)
		return
	}
	embed := &discordgo.MessageEmbed{Title: "Team Call Added", Description: text, Color: AlertKinds["team_call"].Color}
	helpers.WithGuildFooterIconForGuild(s, guildID, embed)
	if dm, err := s.UserChannelCreate(targetID); err == nil {
		_, _ = s.ChannelMessageSendEmbed(dm.ID, embed)
	}
}
