package match

import (
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// A message built in a flow step (the Announce step's own message builder): the same reply a custom command is built with —
// a plain message, classic embeds or a Components V2 layout — saved on the step instead of picked from the built-in embeds.

// stepReply is that reply as the dashboard saves it.
type stepReply struct {
	Type                  string                   `json:"type"` // message, embed or component
	Text                  string                   `json:"text"`
	Content               string                   `json:"content"`
	AllowMentions         bool                     `json:"allow_mentions"`
	AllowedMentions       *helpers.AllowedMentions `json:"allowed_mentions"`
	SuppressEmbeds        bool                     `json:"suppress_embeds"`
	SuppressNotifications bool                     `json:"suppress_notifications"`
	Buttons               []helpers.LinkButton     `json:"buttons"`
	Embeds                []helpers.Embed          `json:"embeds"`
	Components            []helpers.V2Item         `json:"components"`
}

// doc is the message the reply describes.
func (r *stepReply) doc() *helpers.MessageDoc {
	doc := &helpers.MessageDoc{AllowMentions: r.AllowMentions, AllowedMentions: r.AllowedMentions, SuppressEmbeds: r.SuppressEmbeds, SuppressNotifications: r.SuppressNotifications}
	switch r.Type {
	case "component":
		doc.Mode, doc.V2 = "v2", r.Components
	case "embed":
		doc.Mode, doc.Content, doc.Embeds, doc.Buttons = "embeds", r.Content, r.Embeds, r.Buttons
	default:
		doc.Mode, doc.Content, doc.Buttons = "embeds", base.FirstNonEmpty(r.Content, r.Text), r.Buttons
	}
	return doc
}

// stepVars are the variables a step's message can use: {match.number}, {match.mode}, {match.map}, {match.bot}, {match.host},
// {match.players}, {match.teams}..., {channel.*} and {time.*}. The {game.*} spellings of the first ones still work.
func stepVars(g Game, channelName string) map[string]string {
	m := g.Map
	if m == "" || m == MapUndecided {
		m = "Not chosen"
	}
	mentions := make([]string, 0, len(g.Players))
	byTeam := map[string][]string{}
	for _, p := range g.Players {
		mentions = append(mentions, "<@"+p.UserID+">")
		byTeam[p.Team] = append(byTeam[p.Team], "<@"+p.UserID+">")
	}
	names := slices.Clone(g.Teams)
	for team := range byTeam {
		if !slices.Contains(names, team) && team != "" {
			names = append(names, team)
		}
	}
	lines := make([]string, 0, len(names))
	for _, team := range names {
		if len(byTeam[team]) > 0 {
			lines = append(lines, "**"+team+"**: "+strings.Join(byTeam[team], " "))
		}
	}
	now := time.Now()
	started := "—"
	if !g.StartedAt.IsZero() {
		started = "<t:" + strconv.FormatInt(g.StartedAt.Unix(), 10) + ":R>"
	}
	mvp := "—"
	if g.MVP != "" {
		mvp = "<@" + g.MVP + ">"
	}
	vars := map[string]string{
		"match.number": strconv.Itoa(g.Number), "match.mode": g.Mode.Name, "match.map": m, "match.bot": g.Bot,
		"match.status": string(g.State), "match.host": "", "match.host_mention": "—",
		"match.players": strings.Join(mentions, " "), "match.player_count": strconv.Itoa(len(g.Players)),
		"match.teams": strings.Join(lines, "\n"), "match.team_count": strconv.Itoa(len(lines)),
		"match.winner": g.Winner, "match.mvp_mention": mvp, "match.started": started,
		"channel.name": channelName, "channel.mention": "<#" + g.Channels.Text + ">", "channel.id": g.Channels.Text,
		"time.unix": strconv.FormatInt(now.Unix(), 10), "time.relative": "<t:" + strconv.FormatInt(now.Unix(), 10) + ":R>",
		"time.date": now.UTC().Format("2006-01-02"), "time.time": now.UTC().Format("15:04"),
	}
	if p, ok := g.Player(g.Host); ok {
		vars["match.host"], vars["match.host_mention"] = p.Username, "<@"+p.UserID+">"
	}
	for _, k := range []string{"number", "mode", "map", "bot", "status", "host", "host_mention", "players", "player_count"} {
		vars["game."+k] = vars["match."+k]
	}
	return vars
}

// postStepReply posts the message saved on a flow step in the game's channel, with its variables filled in. It reports whether it did.
func (d *gameDiscord) postStepReply(s *discordgo.Session, g Game, stepID string) bool {
	rep := flowOf(g.GuildID).Replies[stepID]
	if rep == nil || g.Channels.Text == "" {
		return false
	}
	payload := rep.doc().Payload(helpers.VarFiller(stepVars(g, channelNameOf(s, g.Channels.Text))))
	// A message that mentions the players (text with {match.players}) has to be allowed to ping them.
	if text, _ := payload["content"].(string); strings.Contains(text, "<@") && payload["allowed_mentions"] == nil {
		payload["allowed_mentions"] = map[string]any{"parse": []string{"users"}}
	}
	_, err := helpers.SendPayload(s, g.Channels.Text, payload)
	return err == nil
}

// channelNameOf is the name of a channel the session knows, or "".
func channelNameOf(s *discordgo.Session, id string) string {
	if s == nil || s.State == nil || id == "" {
		return ""
	}
	if ch, err := s.State.Channel(id); err == nil && ch != nil {
		return ch.Name
	}
	return ""
}
