package match

import (
	"encoding/json"
	"fmt"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/types"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// Every embed a match posts (pre-game, in-game and post-game) is a template. The defaults below are the old bot's
// wording; a guild changes them on the dashboard (Server Config → Embeds), which stores the changes in the server
// profile's `embeds`. The keys, variables and defaults mirror
// Ranked-Bedwars-Dashboard/src/lib/server-profile/flow.ts: keep the two in step.

type embedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
	Each   string `json:"each,omitempty"` // "team": repeat for every team; "map": for every map of the vote
}

// embedTemplate is one embed as stored. In an override, a field that is absent keeps the default.
type embedTemplate struct {
	Enabled     *bool         `json:"enabled,omitempty"`
	Style       *string       `json:"style,omitempty"`
	Color       *string       `json:"color,omitempty"`
	Title       *string       `json:"title,omitempty"`
	Description *string       `json:"description,omitempty"`
	Thumbnail   *string       `json:"thumbnail,omitempty"`
	Image       *string       `json:"image,omitempty"`
	Footer      *string       `json:"footer,omitempty"`
	Content     *string       `json:"content,omitempty"` // text above the embed: variables work, {mentions} pings the players
	Fields      *[]embedField `json:"fields,omitempty"`
	Buttons     *bool         `json:"buttons,omitempty"`
	Box         *bool         `json:"box,omitempty"`    // false: no embed box, only the text above it (and the picture, when there is one)
	Banner      *bool         `json:"banner,omitempty"` // the "scored" embed: also post the match results card (see results_banner.go); on unless false // the host buttons under the party and team embeds (see host_buttons.go); on unless false

	// How the {teamPlayers} list of a team is written. PlayerLine is one player, PlayerLineOut one who is out of the match
	// (live tracker), TeamEmpty a team with nobody, PlayerSeparator what goes between the lines. A line can use {mention},
	// {name}, {id}, {dot}, {captain}, {team}, {elo}, {kills}, {deaths}, {finalKills}, {beds}, {assists}. Absent = the default of the embed.
	PlayerLine      *string `json:"playerLine,omitempty"`
	PlayerLineOut   *string `json:"playerLineOut,omitempty"`
	TeamEmpty       *string `json:"teamEmpty,omitempty"`
	PlayerSeparator *string `json:"playerSeparator,omitempty"`
}

// embedKind says how an embed behaves; the text lives in the template.
type embedKind struct {
	Live      bool   // edited in place instead of posting again
	Message   string // the key whose message this one edits (Party Ready edits the Party Invitation message; Team Selection and Teams Verified edit Match Joined)
	Plain     bool   // only the text above the embed is sent, as a new message each time (the party reminder)
	TeamLines string // "plain" (a mention each), "status" (joined / waiting), "alive" (alive / eliminated) or "locked" (all on their team)
}

// messageOf is the key the message of this embed is remembered under.
func (k embedKind) messageOf(key string) string {
	if k.Message != "" {
		return k.Message
	}
	return key
}

var embedKinds = map[string]embedKind{
	"introduction":    {TeamLines: "plain"},
	"teams":           {TeamLines: "plain"},
	"partyInvitation": {Live: true},
	"partyReady":      {Live: true, Message: "partyInvitation"},
	"partyReminder":   {Plain: true},
	// Match Joined, Team Selection and Teams Verified are one message that moves through the three states.
	"matchJoined":    {Live: true, TeamLines: "tbd"},
	"teamSelection":  {Live: true, Message: "matchJoined", TeamLines: "status"},
	"teamsVerified":  {Live: true, Message: "matchJoined", TeamLines: "status"},
	"liveTracker":    {Live: true, TeamLines: "alive"},
	"matchEnded":     {Live: true}, // posted when the match ends, then edited with each player's ELO change once it is scored
	"captainPicking": {Live: true, TeamLines: "draft"},
	"partySetup":     {TeamLines: "plain"},
	"teamSetup":      {TeamLines: "plain"},
}

func str(s string) *string { return &s }
func yes(b bool) *bool     { return &b }

func tpl(style, title, description string, fields ...embedField) embedTemplate {
	t := embedTemplate{Style: str(style), Title: str(title), Description: str(description)}
	if len(fields) > 0 {
		t.Fields = &fields
	}
	return t
}

func off(t embedTemplate) embedTemplate { t.Enabled = yes(false); return t }

var teamField = embedField{Name: "{team}", Value: "{teamPlayers}", Inline: true, Each: "team"}

// merge puts an override on top of a default.
func (t embedTemplate) merge(o embedTemplate) embedTemplate {
	if o.Enabled != nil {
		t.Enabled = o.Enabled
	}
	if o.Style != nil {
		t.Style = o.Style
	}
	if o.Color != nil {
		t.Color = o.Color
	}
	if o.Title != nil {
		t.Title = o.Title
	}
	if o.Description != nil {
		t.Description = o.Description
	}
	if o.Thumbnail != nil {
		t.Thumbnail = o.Thumbnail
	}
	if o.Image != nil {
		t.Image = o.Image
	}
	if o.Footer != nil {
		t.Footer = o.Footer
	}
	if o.Content != nil {
		t.Content = o.Content
	}
	if o.Buttons != nil {
		t.Buttons = o.Buttons
	}
	if o.Banner != nil {
		t.Banner = o.Banner
	}
	if o.Box != nil {
		t.Box = o.Box
	}
	if o.Fields != nil {
		t.Fields = o.Fields
	}
	if o.PlayerLine != nil {
		t.PlayerLine = o.PlayerLine
	}
	if o.PlayerLineOut != nil {
		t.PlayerLineOut = o.PlayerLineOut
	}
	if o.TeamEmpty != nil {
		t.TeamEmpty = o.TeamEmpty
	}
	if o.PlayerSeparator != nil {
		t.PlayerSeparator = o.PlayerSeparator
	}
	return t
}

func (t embedTemplate) on() bool { return t.Enabled == nil || *t.Enabled }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ============================================================================
// The guild's flow config (embeds and custom stats)
// ============================================================================

type statLabel struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Announce bool   `json:"announce"`
}

// flowStep is one block of the profile's pipeline as the Go side needs it. Groups and ifs hold more blocks.
type flowStep struct {
	Type      string     `json:"type"`
	Embed     string     `json:"embed"`
	Enabled   *bool      `json:"enabled"`
	Phase     string     `json:"phase"`
	ID        string     `json:"id"`
	Message   string     `json:"message"`
	Builtin   bool       `json:"builtin"`
	Hosting   string     `json:"hosting"`
	WaitSec   float64    `json:"waitSec"`
	Mode      string     `json:"mode"`
	Children  []flowStep `json:"children"`
	Otherwise []flowStep `json:"otherwise"`
}

type guildFlow struct {
	Embeds    map[string]embedTemplate
	Stats     []statLabel
	Announced map[string]bool       // embeds the pipeline posts itself with an Announce step
	Manual    map[string][]flowStep // phase -> the blocks of a hand-hosted game, in order (everything that is not the bot's alone)
	// ManualDefaults: no flow was saved at all, so a hand-hosted game gets the fixed party, map and submit-your-win messages.
	ManualDefaults bool
	Replies        map[string]*stepReply // step id -> the message built in that Announce step
	PartyInvite    string                // the profile's party invite command, with {players}
	ButtonLabels   map[string]string     // host button id -> its text (hosting.buttons), blank = the default
	Delivery       delivery              // where results and staff alerts go (delivery_helper.go)
	MapVote        struct {              // hosting.mapVote; zero values mean the defaults
		Seconds float64
		Perks   *bool
		Winner  string
		Enabled *bool
		Manual  *bool // hosted by hand: vote for the map too (default: list the maps, nobody votes)
	}
}

// mapVoteRules is how the guild's map vote runs: how long, whether perks weigh the votes, whether the winner is drawn by vote share, and
// whether players vote at all (off: the map is picked by chance).
func (f guildFlow) mapVoteRules() (lasts time.Duration, perks, weighted, enabled bool) {
	lasts = mapVoteDuration
	if f.MapVote.Seconds > 0 {
		lasts = time.Duration(f.MapVote.Seconds * float64(time.Second))
	}
	perks = f.MapVote.Perks == nil || *f.MapVote.Perks
	return lasts, perks, f.MapVote.Winner == "weighted", f.MapVote.Enabled == nil || *f.MapVote.Enabled
}

func (f guildFlow) announces(id string) bool {
	for _, s := range f.Stats {
		if s.ID == id {
			return s.Announce
		}
	}
	return false
}

func (f guildFlow) label(id string) string {
	for _, s := range f.Stats {
		if s.ID == id && strings.TrimSpace(s.Label) != "" {
			return s.Label
		}
	}
	return id
}

// embedFor is the embed as configured: the default with the guild's changes on top.
func (f guildFlow) embedFor(key string) (embedTemplate, bool) {
	def, ok := defaultEmbeds[key]
	if !ok {
		return embedTemplate{}, false
	}
	if o, ok := f.Embeds[key]; ok {
		def = def.merge(o)
	}
	return def, true
}

var (
	flowMu    sync.Mutex
	flowCache = map[string]struct {
		at   time.Time
		flow guildFlow
	}{}

	// flowLoader reads the server profile's flow config; tests replace it.
	flowLoader = loadGuildFlow
)

const flowTTL = 15 * time.Second

// flowOf returns the guild's flow config, cached briefly. Any failure falls back to the defaults.
func flowOf(guildID string) guildFlow {
	flowMu.Lock()
	defer flowMu.Unlock()
	if hit, ok := flowCache[guildID]; ok && time.Since(hit.at) < flowTTL {
		return hit.flow
	}
	f := flowLoader(guildID)
	flowCache[guildID] = struct {
		at   time.Time
		flow guildFlow
	}{time.Now(), f}
	return f
}

func loadGuildFlow(guildID string) guildFlow {
	raw, err := database.Must().GameConfig.Field(guildID, "server")
	if err != nil || len(raw) == 0 {
		return guildFlow{}
	}
	return parseGuildFlow(raw)
}

// parseGuildFlow reads the `server` game config: {"profile": {"embeds": {...}, "stats": [...]}}.
func parseGuildFlow(raw json.RawMessage) guildFlow {
	var server struct {
		Profile struct {
			Embeds   map[string]json.RawMessage `json:"embeds"`
			Stats    []statLabel                `json:"stats"`
			Commands struct {
				PartyInvite string `json:"partyInvite"`
			} `json:"commands"`
			Hosting struct {
				MapVote struct {
					DurationSec float64 `json:"durationSec"`
					UsePerks    *bool   `json:"usePerks"`
					Winner      string  `json:"winner"`
					Enabled     *bool   `json:"enabled"`
					Manual      *bool   `json:"manual"`
				} `json:"mapVote"`
				Buttons struct {
					PartyWarpLabel   string `json:"partyWarpLabel"`
					PartyInviteLabel string `json:"partyInviteLabel"`
					GameWarpLabel    string `json:"gameWarpLabel"`
					PGKickLabel      string `json:"pgKickLabel"`
				} `json:"buttons"`
			} `json:"hosting"`
			Delivery struct {
				Results struct {
					ChannelID string `json:"channel_id"`
					Mode      string `json:"mode"`
				} `json:"results"`
				Alerts struct {
					ChannelID string          `json:"channel_id"`
					RoleID    string          `json:"role_id"`
					Events    map[string]bool `json:"events"`
				} `json:"alerts"`
			} `json:"delivery"`
			Messages []struct {
				ID    string          `json:"id"`
				Reply json.RawMessage `json:"reply"`
			} `json:"messages"`
			Pipeline []flowStep `json:"pipeline"`
		} `json:"profile"`
	}
	if json.Unmarshal(raw, &server) != nil {
		return guildFlow{}
	}
	f := guildFlow{Embeds: map[string]embedTemplate{}, Stats: server.Profile.Stats, Announced: map[string]bool{}, Manual: map[string][]flowStep{}, Replies: map[string]*stepReply{}, PartyInvite: strings.TrimSpace(server.Profile.Commands.PartyInvite)}
	b := server.Profile.Hosting.Buttons
	f.MapVote.Seconds, f.MapVote.Perks, f.MapVote.Winner, f.MapVote.Enabled = server.Profile.Hosting.MapVote.DurationSec, server.Profile.Hosting.MapVote.UsePerks, server.Profile.Hosting.MapVote.Winner, server.Profile.Hosting.MapVote.Enabled
	f.MapVote.Manual = server.Profile.Hosting.MapVote.Manual
	f.Delivery = delivery{
		ResultsChannel: server.Profile.Delivery.Results.ChannelID, ResultsMode: server.Profile.Delivery.Results.Mode,
		AlertsChannel: server.Profile.Delivery.Alerts.ChannelID, AlertRole: server.Profile.Delivery.Alerts.RoleID, Alerts: server.Profile.Delivery.Alerts.Events,
	}
	f.ButtonLabels = map[string]string{"party_warp": b.PartyWarpLabel, "party_invite": b.PartyInviteLabel, "game_warp": b.GameWarpLabel, "pg_kick": b.PGKickLabel}
	if server.Profile.Pipeline == nil { // no flow saved at all: the default one posts the introduction; an empty flow posts nothing
		f.Announced["introduction"] = true
		f.ManualDefaults = true
	}
	saved := map[string]*stepReply{} // the profile's custom embeds, by id
	for _, m := range server.Profile.Messages {
		var rep stepReply
		if json.Unmarshal(m.Reply, &rep) == nil && m.ID != "" {
			saved[m.ID] = &rep
			f.Replies["custom:"+m.ID] = &rep // failure policies post custom messages without a step
		}
	}
	var walk func(steps []flowStep, phase, hosting string)
	walk = func(steps []flowStep, phase, hosting string) {
		for _, st := range steps {
			// A block inside a group or an if belongs to the phase and hosting of the block holding it.
			stPhase, stHosting := base.FirstNonEmpty(st.Phase, phase), base.FirstNonEmpty(st.Hosting, hosting)
			walk(st.Children, stPhase, stHosting)
			walk(st.Otherwise, stPhase, stHosting)
			if st.Builtin { // the step posts its built-in embed: a custom embed kept on it is not used
			} else if rep := saved[st.Message]; st.Message != "" && rep != nil {
				f.Replies[st.ID] = rep
			}
			if st.Type == "announce" && (st.Enabled == nil || *st.Enabled) && stHosting == "manual" {
				continue // posted by the hand-hosting flow only, so it never keeps the bot's default posting away
			}
			if st.Type == "announce" && (st.Enabled == nil || *st.Enabled) && st.Embed != "" {
				f.Announced[st.Embed] = true
			}
		}
	}
	walk(server.Profile.Pipeline, "", "")
	// The hand-hosted flow: every top-level block that is not the bot's alone, by phase. The blocks inside a group travel with it.
	for _, st := range server.Profile.Pipeline {
		if st.Hosting != "automatic" && (st.Enabled == nil || *st.Enabled) {
			p := base.FirstNonEmpty(st.Phase, "pre")
			f.Manual[p] = append(f.Manual[p], st)
		}
	}
	for key, body := range server.Profile.Embeds {
		var t embedTemplate
		if json.Unmarshal(body, &t) == nil { // one bad template doesn't take the others down
			f.Embeds[key] = t
		}
	}
	return f
}

// ============================================================================
// Rendering
// ============================================================================

// embedData is everything a template can draw on.
type embedData struct {
	Game       Game
	Vars       map[string]string // extra variables for this embed
	Joined     []string          // Discord ids that joined the party / their team
	Waiting    []string          // ... that are still missing
	Eliminated map[string]bool   // user ids out of the match (live tracker)
	BedLost    map[string]bool   // team name -> bed gone (live tracker)
	Flow       guildFlow
	TeamRows   []map[string]string // per-team values the caller supplies (the captain draft's rosters) instead of the game's teams
	MapRows    []map[string]string // per-map values of the map vote ({mapName}, {mapIcon}, {mapScore}, {winnerTag})
	Invited    []string            // ... invited to the party but not joined yet
	Force      bool                // draw it even when the guild switched the embed off: the vote and the draft cannot be skipped
	Content    string              // text above the embed that replaces the template's (the party reminder rides on the invitation)
	File       []byte              // a picture (the results card) sent with the message: it is the embed's image, or the whole message when the embed has no text
}

func fill(text string, vars map[string]string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(text, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(text[i:], '}')
		if j < 0 {
			break
		}
		name := text[i+1 : i+j]
		if name == "" || strings.ContainsAny(name, " \n{") || !isWord(name) {
			b.WriteString(text[:i+1])
			text = text[i+1:]
			continue
		}
		b.WriteString(text[:i])
		b.WriteString(vars[name]) // unknown names are empty, so a field built only from one disappears
		text = text[i+j+1:]
	}
	b.WriteString(text)
	return b.String()
}

func isWord(s string) bool {
	for _, r := range s {
		if !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func (d embedData) common() map[string]string {
	g := d.Game
	m := g.Map
	if m == "" || m == MapUndecided {
		m = "Not chosen"
	}
	vars := map[string]string{
		"game": strconv.Itoa(g.Number), "gameUrl": helpers.MatchURL(g.GuildID, g.SeasonID, g.Number, g.Total), "gameLink": helpers.MatchLink(g.GuildID, g.SeasonID, g.Number, g.Total, "Game #"+strconv.Itoa(g.Number)), "gameNumberLink": helpers.MatchLink(g.GuildID, g.SeasonID, g.Number, g.Total, "#"+strconv.Itoa(g.Number)), "mode": g.Mode.Name, "map": m, "bot": g.Bot, "host": "", "hostMention": "—",
		"winner": g.Winner, "mvp": g.MVP, "mvpMention": "—", "playerCount": strconv.Itoa(len(g.Players)),
	}
	if p, ok := g.Player(g.Host); ok {
		vars["host"], vars["hostMention"] = p.Username, "<@"+p.UserID+">"
	}
	if g.MVP != "" {
		vars["mvpMention"] = "<@" + g.MVP + ">"
	}
	if len(g.Results) > 0 {
		vars["winners"], vars["losers"] = resultLines(g)
		vars["winnerMentions"], vars["loserMentions"] = resultMentions(g)
	} else if g.Winner != "" {
		// A casual game rates nobody, but the team that won is known: it is announced like a scored game.
		vars["winnerMentions"], vars["loserMentions"] = teamMentions(g)
	}
	vars["mentions"] = pingsOf(g)
	pending := make([]string, 0, len(d.Waiting)+len(d.Invited))
	for _, id := range append(slices.Clone(d.Waiting), d.Invited...) {
		pending = append(pending, "<@"+id+">")
	}
	vars["waitingMentions"] = strings.Join(pending, " ") // everyone the party or the teams are still waiting for
	vars["statTable"] = statTable(g, d.Flow)
	vars["joined"], vars["joinedCount"] = namesList(g, d.Joined)
	vars["waiting"], vars["waitingCount"] = namesList(g, d.Waiting)
	vars["invited"], vars["invitedCount"] = namesList(g, d.Invited)
	vars["flow"], vars["teamClause"] = "Standard", "teams have been automatically assigned"
	switch {
	case g.Custom != nil:
		vars["flow"], vars["teamClause"] = "Custom", "with teams chosen by staff"
	case g.Captain:
		vars["flow"], vars["teamClause"] = "Captain", "teams will be drafted by team captains"
	case len(g.Parties) > 0:
		vars["teamClause"] = "teams have been automatically assigned with party groups kept together"
	}
	for k, v := range d.Vars {
		vars[k] = v
	}
	return vars
}

// namesList is who is in the list: a line with a mention each (their Discord name, whoever they are), "None" when empty.
func namesList(g Game, ids []string) (string, string) {
	if len(ids) == 0 {
		return "None", "0"
	}
	lines := make([]string, len(ids))
	for i, id := range ids {
		lines[i] = "- <@" + id + ">"
	}
	return strings.Join(lines, "\n"), strconv.Itoa(len(ids))
}

// PlayersExcept lists the Discord ids of the game's players who are not in skip.
func PlayersExcept(g Game, skip []string) []string {
	var out []string
	for _, p := range g.Players {
		if !slices.Contains(skip, p.UserID) {
			out = append(out, p.UserID)
		}
	}
	return out
}

func resultLines(g Game) (winners, losers string) {
	name := func(id string) string {
		if p, ok := g.Player(id); ok {
			return p.Username
		}
		return id
	}
	var w, l []string
	for _, r := range g.Results {
		mvp := ""
		if r.MVP {
			mvp = " ⭐"
		}
		line := fmt.Sprintf("%s%s `%d → %d (%+d)`", name(r.UserID), mvp, r.Before, r.After, r.Change)
		if r.Win {
			w = append(w, line)
		} else {
			l = append(l, line)
		}
	}
	return strings.Join(w, "\n"), strings.Join(l, "\n")
}

// teamMentions lists the winning team's players and everyone else on a team as plain mentions, for a game that was not scored
// (a casual match): there are no results to read them from, but the team that won is on the game.
func teamMentions(g Game) (winners, losers string) {
	var w, l []string
	for _, p := range g.Players {
		switch {
		case p.Team == teamUnassigned:
			continue
		case strings.EqualFold(p.Team, g.Winner):
			w = append(w, "<@"+p.UserID+">")
		default:
			l = append(l, "<@"+p.UserID+">")
		}
	}
	return strings.Join(w, " "), strings.Join(l, " ")
}

// resultMentions lists the winners and the losers as plain mentions, the way the old bot did.
func resultMentions(g Game) (winners, losers string) {
	var w, l []string
	for _, r := range g.Results {
		if r.Win {
			w = append(w, "<@"+r.UserID+">")
		} else {
			l = append(l, "<@"+r.UserID+">")
		}
	}
	return strings.Join(w, " "), strings.Join(l, " ")
}

// statTable is one line per player with the built-in stats and any custom stats, best first. It stops before
// Discord's field limit.
func statTable(g Game, f guildFlow) string {
	players := slices.Clone(g.Players)
	sort.SliceStable(players, func(i, j int) bool {
		if players[i].Kills != players[j].Kills {
			return players[i].Kills > players[j].Kills
		}
		return players[i].BedsDestroyed > players[j].BedsDestroyed
	})
	var lines []string
	size := 0
	for _, p := range players {
		if p.Kills+p.Deaths+p.FinalKills+p.BedsDestroyed+p.BedsLost == 0 && len(p.Custom) == 0 {
			continue
		}
		parts := []string{fmt.Sprintf("K %d", p.Kills), fmt.Sprintf("D %d", p.Deaths), fmt.Sprintf("FK %d", p.FinalKills), fmt.Sprintf("B %d", p.BedsDestroyed)}
		ids := make([]string, 0, len(p.Custom))
		for id := range p.Custom {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			parts = append(parts, fmt.Sprintf("%s %g", f.label(id), p.Custom[id]))
		}
		line := fmt.Sprintf("<@%s> %s", p.UserID, strings.Join(parts, " · "))
		if size+len(line)+1 > 1000 {
			break
		}
		size += len(line) + 1
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// defaultPlayerLines are how each kind of embed writes a player of a team (and one who is out of the match), and an
// empty team, unless the guild's template says otherwise.
var defaultPlayerLines = map[string]struct{ line, out, empty string }{
	"plain":  {"- {mention}", "- {mention}", "`Empty`"},
	"status": {"{dot} {mention}{partyMark}", "{dot} {mention}{partyMark}", "No players"},
	"locked": {"{dot} {mention}{partyMark}", "{dot} {mention}{partyMark}", "No players"},
	"tbd":    {"{dot} {mention}{partyMark}", "{dot} {mention}{partyMark}", ""},
	"alive":  {"{dot} {mention}", "{dot} ~~{mention}~~", "No players"},
	"draft":  {"{mention} {captain}", "{mention} {captain}", "No players"},
}

func orDefault(v *string, def string) string {
	if v != nil && strings.TrimSpace(*v) != "" {
		return *v
	}
	return def
}

// teamVars builds the per-team values for fields repeated for every team. Each player is written with the template's
// PlayerLine (or the kind's default), so a guild chooses what a player's line shows.
// teamRowsFor is the per-team values of fields repeated for every team: the caller's own, or the game's teams.
func (d embedData) teamRowsFor(lines string, t embedTemplate) []map[string]string {
	if d.TeamRows != nil {
		return d.TeamRows
	}
	return d.teamVars(lines, t)
}

func (d embedData) teamVars(lines string, t embedTemplate) []map[string]string {
	waiting := map[string]bool{}
	for _, id := range d.Waiting {
		waiting[id] = true
	}
	def := defaultPlayerLines[lines]
	if def.line == "" {
		def = defaultPlayerLines["plain"]
	}
	line, out, empty := orDefault(t.PlayerLine, def.line), orDefault(t.PlayerLineOut, def.out), orDefault(t.TeamEmpty, def.empty)
	sep := "\n"
	if t.PlayerSeparator != nil && *t.PlayerSeparator != "" {
		sep = strings.ReplaceAll(*t.PlayerSeparator, `\n`, "\n")
	}
	var teams []map[string]string
	for _, team := range d.Game.Teams {
		var rows []string
		for _, p := range d.Game.TeamPlayers(team) {
			dot, tpl := "", line
			switch lines {
			case "status":
				dot = helpers.DotGreen
				if waiting[p.UserID] {
					dot = helpers.DotRed
				} else if len(d.Joined) == 0 && len(d.Waiting) == 0 {
					dot = helpers.DotGray
				}
			case "tbd": // the bot just joined: nobody is placed yet
				dot = helpers.DotGray
			case "locked": // the match began: every player is on their team
				dot = helpers.DotGreen
			case "alive":
				dot = helpers.DotGreen
				if d.Eliminated[p.UserID] {
					dot, tpl = helpers.DotRed, out
				}
			}
			captain := ""
			if p.Captain {
				captain = "👑"
			}
			partyName, partyMark := p.Username, ""
			if d.Game.inParty(p.UserID) {
				partyName += " 👥"
				partyMark = " 👥"
			}
			rows = append(rows, strings.TrimSpace(fill(tpl, map[string]string{
				"mention": "<@" + p.UserID + ">", "name": p.Username, "partyName": partyName, "partyMark": partyMark, "id": p.UserID, "dot": dot, "captain": captain, "team": team,
				"elo": strconv.Itoa(p.EloBefore), "kills": strconv.Itoa(p.Kills), "deaths": strconv.Itoa(p.Deaths),
				"finalKills": strconv.Itoa(p.FinalKills), "beds": strconv.Itoa(p.BedsDestroyed), "assists": strconv.Itoa(p.Assists),
			})))
		}
		if len(rows) == 0 {
			rows = []string{empty}
		}
		bed := "🛏️"
		if d.BedLost[team] {
			bed = "❌"
		}
		teams = append(teams, map[string]string{"team": team, "teamPlayers": strings.Join(rows, sep), "teamStats": TeamStatLines(d.Game, team, d.Flow), "bed": bed})
	}
	return teams
}

var styleToPreset = map[string]string{"success": "success", "pending": "loading", "info": "info", "caution": "caution", "error": "error", "default": "default"}

// renderEmbed fills a template. It returns nil when the embed is switched off.
func renderEmbed(key string, t embedTemplate, d embedData) *discordgo.MessageEmbed {
	if !t.on() && !d.Force {
		return nil
	}
	vars := d.common()
	var fields []*discordgo.MessageEmbedField
	for _, f := range deref2(t.Fields) {
		rows := []map[string]string{vars}
		if f.Each == "team" && key == "introduction" && d.Game.Captain {
			continue // the teams are not drafted yet
		}
		if f.Each == "map" {
			rows = nil
			for _, mv := range d.MapRows {
				merged := make(map[string]string, len(vars)+4)
				for k, v := range vars {
					merged[k] = v
				}
				for k, v := range mv {
					merged[k] = v
				}
				rows = append(rows, merged)
			}
		}
		if f.Each == "team" {
			rows = nil
			for _, tv := range d.teamRowsFor(embedKinds[key].TeamLines, t) {
				merged := make(map[string]string, len(vars)+3)
				for k, v := range vars {
					merged[k] = v
				}
				for k, v := range tv {
					merged[k] = v
				}
				rows = append(rows, merged)
			}
		}
		for _, v := range rows {
			name, value := strings.TrimSpace(fill(f.Name, v)), strings.TrimSpace(fill(f.Value, v))
			if name == "" || value == "" {
				continue
			}
			fields = append(fields, &discordgo.MessageEmbedField{Name: base.Clip(name, 256), Value: base.Clip(value, 1024), Inline: f.Inline})
			if len(fields) == 25 {
				break
			}
		}
	}

	opts := types.PresetOptions{
		Title:       base.Clip(strings.TrimSpace(fill(deref(t.Title), vars)), 256),
		Description: base.Clip(strings.TrimSpace(fill(deref(t.Description), vars)), 4096),
		Fields:      fields,
		Thumbnail:   strings.TrimSpace(fill(deref(t.Thumbnail), vars)),
		Image:       strings.TrimSpace(fill(deref(t.Image), vars)),
	}
	if footer := strings.TrimSpace(fill(deref(t.Footer), vars)); footer != "" {
		opts.FooterText = base.Clip(footer, 2048)
	}
	if c := strings.TrimPrefix(strings.TrimSpace(deref(t.Color)), "#"); len(c) == 6 {
		if n, err := strconv.ParseInt(c, 16, 32); err == nil {
			opts.Color = int(n)
		}
	}
	style := styleToPreset[deref(t.Style)]
	return helpers.Preset(style, opts)
}

func deref2(f *[]embedField) []embedField {
	if f == nil {
		return nil
	}
	return *f
}

// pingsOf lists the mentions that go with a message when the template pings the players.
func pingsOf(g Game) string {
	ids := make([]string, 0, len(g.Players))
	for _, p := range g.Players {
		ids = append(ids, "<@"+p.UserID+">")
	}
	return strings.Join(ids, " ")
}

// Embeds shared by the commands that act on a game through the GameManager
// (pick, voidmatch, score, rescore, forcevoid, partialvoid, unvoid,
// submitresult). Known refusals get their own embed; anything else is an
// Unexpected Error so internal details never reach the channel.

// ============================================================================
// Embed Builders

// TeamStatLines is a team's players one block each: the mention with their username beside it, then their ELO change (once the
// game is scored, as before → after) and their stats. It stops before Discord's field limit.
func TeamStatLines(g Game, team string, f guildFlow) string {
	var blocks []string
	size := 0
	for _, p := range g.TeamPlayers(team) {
		head := fmt.Sprintf("<@%s> (`%s`)", p.UserID, MCName(g.GuildID, p))
		if p.Captain {
			head = "👑 " + head
		}
		elo := fmt.Sprintf("ELO: `%d`", p.EloBefore)
		for _, r := range g.Results {
			if r.UserID == p.UserID {
				elo = fmt.Sprintf("ELO: `%d` → `%d` (`%+d`)", r.Before, r.After, r.Change) // before → after, with the gain or loss
				break
			}
		}
		stats := fmt.Sprintf("K/D: `%d/%d` · FK: `%d` · Beds: `%d`", p.Kills, p.Deaths, p.FinalKills, p.BedsDestroyed)
		ids := make([]string, 0, len(p.Custom))
		for id := range p.Custom {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			stats += fmt.Sprintf(" · %s: `%g`", f.label(id), p.Custom[id])
		}
		block := head + "\n> " + elo + "\n> " + stats // the player's name, then the ELO change, then K/D, FK and beds
		if size+len(block)+1 > 1000 {
			break
		}
		size += len(block) + 1
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return "No players"
	}
	return strings.Join(blocks, "\n") // the next player right below
}

// MCName is the Minecraft name the player registered (the Discord display name carries their ELO prefix); without one, the display name.
func MCName(guildID string, p GamePlayer) string {
	if view, err := database.Must().Player.Get(guildID, p.UserID); err == nil && view.Config != nil && strings.TrimSpace(view.Config.Username) != "" {
		return view.Config.Username
	}
	return p.Username
}

// TeamStats is TeamStatLines with the guild's own stat labels, for commands outside this package.
func TeamStats(g Game, team string) string { return TeamStatLines(g, team, flowOf(g.GuildID)) }
