package match

import (
	"bytes"
	"fmt"
	"log"
	"maps"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// The Discord side of a match's life, wired in with GameManager.SetEffects:
// the notices posted in the game chat, moving players into their team voice
// channels, the map vote and the captain draft. The game manager decides what
// happens; this only shows it and collects the clicks.

const (
	mapVoteDuration   = 45 * time.Second
	draftIDPrefix     = "gdraft1|"
	mapVoteIDPrefix   = "gmvote1|"
	maxMapVoteOptions = 20 // five rows of five, minus the room Discord keeps
)

// GameDiscordEffects are the effects this file provides.
type GameDiscordEffects struct {
	Notify         func(g Game, ev GameEvent)
	ApplyTeams     func(g Game) error
	SelectMap      func(g Game, available []MapOption, mapMenuRead bool) (*MapSelection, error)
}

// NewGameDiscordEffects returns the effects for GameManager.SetEffects and
// registers the component handlers their buttons and menus need. session
// returns the live Discord session.
func NewGameDiscordEffects(session func() *discordgo.Session) GameDiscordEffects {
	d := &gameDiscord{session: session, votes: map[uint]*mapVote{}, closedVotes: map[uint]*closedVote{}, drafts: map[uint]*draftUI{}, live: map[uint]*embedState{}, introductions: map[uint]bool{}}
	helpers.RegisterComponentHandler(d.handleComponent)
	gameUI.Store(d)
	return GameDiscordEffects{Notify: d.notify, ApplyTeams: d.applyTeams, SelectMap: d.selectMap}
}

// closedVote is a map vote that is over: its message and the per-map scores it ended with.
type closedVote struct {
	msg  *discordgo.Message
	rows []map[string]string
}

type gameDiscord struct {
	session func() *discordgo.Session

	mu            sync.Mutex
	votes         map[uint]*mapVote
	closedVotes   map[uint]*closedVote // a vote's message once it closed, until the bot has picked the map
	drafts        map[uint]*draftUI
	live          map[uint]*embedState
	introductions map[uint]bool
}

// ============================================================================
// Notices
// ============================================================================

func (d *gameDiscord) notify(g Game, ev GameEvent) {
	s := d.session()
	if s == nil || g.Channels.Text == "" {
		return
	}
	switch ev.Kind {
	case EventDraftNeeded:
		d.startDraft(s, g)
	case EventTeams:
		flow := flowOf(g.GuildID)
		if HostEngine.Connected() && helpers.GuildHasFeature(g.GuildID, "automated-hosting") && (g.Custom == nil || g.Custom.UseBot) {
			d.postIntroduction(s, g)
			// A pipeline with its own "Teams final" Announce step decides when this goes out.
			if !flow.Announced["teams"] {
				d.post(s, g, "teams", embedData{})
			}
		} else if flow.ManualDefaults {
			d.post(s, g, "teams", embedData{}) // hosted by hand with no flow saved: the fixed set. With a flow, its blocks decide.
		}
	case EventStarted:
		if g.Bot != "" {
			d.startTracker(s, g)
			d.scheduleTeamStatuses(s, g, false)
		} else {
			// Hosted by hand: the flow's manual blocks, once the map is chosen (the party and map instructions need it). A game with no flow saved
			// at all gets the old fixed set.
			go func() { // the blocks can wait, so they run beside the rest of the bot
				d.runManual(s, g, flowOf(g.GuildID).Manual["pre"])
				if flowOf(g.GuildID).ManualDefaults {
					d.manualSetup(s, g)
					d.post(s, g, "matchStartedManual", embedData{})
				}
				d.runManual(s, g, flowOf(g.GuildID).Manual["in"])
			}()
		}
	case EventHostStatus:
		// Progress is shown by the configured embeds (EventEmbed); the status only matters to the logs.
	case EventEmbed:
		if id, ok := strings.CutPrefix(ev.Key, "step:"); ok { // a message built in an Announce step
			d.postStepReply(s, g, id)
			return
		}
		if ev.Key == "introduction" {
			d.mu.Lock()
			alreadyPosted := d.introductions[g.ID]
			d.mu.Unlock()
			if alreadyPosted {
				return
			}
		}
		if ev.Key == "mapSelected" && d.finishVote(s, g, ev) {
			return
		}
		// An embed the flow announces with a step of its own is posted even when the Embeds tab has it switched off.
		data := embedData{Vars: ev.Vars, Joined: ev.JoinedIDs, Waiting: ev.WaitingIDs, Invited: ev.InvitedIDs, Force: flowOf(g.GuildID).Announced[ev.Key]}
		st := d.state(g.ID)
		switch ev.Key {
		case "partyInvitation":
			d.mu.Lock()
			st.invite, st.hasInvite = data, true
			d.mu.Unlock()
		case "partyReady":
			d.mu.Lock()
			st.hasInvite = false // the invitation is done; a late reminder must not bring it back
			d.mu.Unlock()
		}
		if ev.Key == "partyReminder" {
			d.postReminderWithInvitation(s, g, st, data)
		} else {
			d.post(s, g, ev.Key, data)
		}
	case EventMatch:
		d.matchEvent(s, g, ev.Match)
	case EventMatchEnded:
		d.scheduleTeamStatuses(s, g, true)
		d.post(s, g, "matchEnded", embedData{Vars: ev.Vars})
	case EventStaffPending:
		d.scheduleTeamStatuses(s, g, true)
		// no "Win Submitted" post: the submitter already got the result embed, with the screenshot, as the reply to their submit
		d.forget(g.ID)
	case EventCasualDone:
		d.scheduleTeamStatuses(s, g, true)
		d.post(s, g, "casualDone", embedData{})
		go func() { // a casual game is a game entry too: its results go out when the mode's "Send results" is on
			png := d.resultsPNG(g, outcomeCasual)
			if len(png) == 0 {
				return
			}
			// With a winning team it is announced like a scored game (the game, its winners and losers, the card attached); without
			// one, or with that embed switched off, the card goes out alone.
			if g.Winner != "" && d.post(s, g, "scored", embedData{File: png}) {
				return
			}
			d.sendResultsPNG(s, g, png, outcomeCasual)
		}()
		if g.Bot == "" {
			go d.runManual(s, g, flowOf(g.GuildID).Manual["post"])
		}
		d.forget(g.ID)
	case EventScored:
		d.scheduleTeamStatuses(s, g, true)
		// The Match Over message, posted before the result existed, now shows each player's ELO before → after. Done before forget, which drops its message id.
		st := d.state(g.ID)
		d.mu.Lock()
		over := st.posted["matchEnded"]
		d.mu.Unlock()
		if over {
			d.post(s, g, "matchEnded", embedData{})
		}
		for _, hook := range ScoredHooks {
			go hook(s, g)
		}
		// One message: the scored embed (text, mentions, a box or none) with the results card attached. When the server switched that
		// embed off, the card alone is posted.
		go func() {
			png := d.resultsPNG(g, resultsOutcome(g, false))
			if !d.post(s, g, "scored", embedData{File: png}) && len(png) > 0 {
				d.sendResultsPNG(s, g, png, resultsOutcome(g, false))
			}
		}()
		if g.Bot == "" {
			go d.runManual(s, g, flowOf(g.GuildID).Manual["post"])
		}
		d.forget(g.ID)
	case EventChatVote:
		n, _ := strconv.Atoi(ev.Vars["number"])
		d.chatVote(s, g, ev.Key, n)
	case EventTrackingLost:
		reason := ev.Reason
		if reason == "" {
			reason = "The hosting bot stopped tracking the match."
		}
		// The same "Submit your win" message hosting by hand posts: players send their win to staff.
		d.post(s, g, "matchStartedManual", embedData{Vars: map[string]string{"reason": reason}})
		StaffAlertPinging(s, g.GuildID, "tracking_lost", g.PlayerIDs(), fmt.Sprintf("Game #%d is no longer tracked by the bot: %s", g.Number, reason))
		d.forget(g.ID)
	case EventVoided:
		d.scheduleTeamStatuses(s, g, true)
		d.post(s, g, "voided", embedData{Vars: map[string]string{"reason": ev.Reason}})
		go func() { // a voided game is still a game entry: its card (the void variant) goes out when the mode's "Send results" is on
			if png := d.resultsPNG(g, outcomeVoid); len(png) > 0 {
				d.sendResultsPNG(s, g, png, outcomeVoid)
			}
		}()
		desc, fields := voidAlert(g, ev.Reason)
		StaffAlertPinging(s, g.GuildID, "game_voided", g.PlayerIDs(), desc, fields...)
		d.forget(g.ID)
	}
}

// finishVote edits the closed map vote's own message with the map the bot picked, like the old bot did. It reports false
// when there is no such message (a game with one map, say), so the caller posts the embed instead.
func (d *gameDiscord) finishVote(s *discordgo.Session, g Game, ev GameEvent) bool {
	d.mu.Lock()
	cv := d.closedVotes[g.ID]
	delete(d.closedVotes, g.ID)
	d.mu.Unlock()
	if cv == nil {
		return false
	}
	emb, _, _ := d.build(g, "mapSelected", embedData{Vars: ev.Vars, MapRows: cv.rows})
	if emb == nil { // the guild switched the embed off: leave the vote as it ended
		return true
	}
	helpers.WithGuildFooterIconForGuild(s, g.GuildID, emb)
	comps := []discordgo.MessageComponent{}
	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{Channel: cv.msg.ChannelID, ID: cv.msg.ID, Embeds: &[]*discordgo.MessageEmbed{emb}, Components: &comps})
	return true
}

// manualVars are the variables the party and map instructions of a hand-hosted game use: the invite command for everyone but the host, and
// the chosen map's height and team colours.
func (d *gameDiscord) manualVars(g Game) map[string]string {
	flow := flowOf(g.GuildID)
	var invite []string
	for _, p := range g.Players {
		if p.UserID == g.Host {
			continue
		}
		if name := inviteName(g.GuildID, p); name != "" {
			invite = append(invite, name)
		}
	}
	command := fill(base.FirstNonEmpty(flow.PartyInvite, "/party invite {players}"), map[string]string{"players": strings.Join(invite, " ")})
	vars := map[string]string{"inviteCommand": strings.TrimSpace(command), "mapHeight": "—", "mapTeams": "—", "mapCount": "0"}
	maps, _ := helpers.GuildMetaMaps(g.GuildID)
	count := 0
	for _, m := range maps {
		if m.IsEnabled && m.IsMapAvailable {
			count++
		}
		if g.Map != "" && m.Name == g.Map {
			if m.MapHeight > 0 {
				vars["mapHeight"] = strconv.Itoa(m.MapHeight)
			}
			if teams := m.TeamsFor(g.Mode.Name); len(teams) > 0 {
				vars["mapTeams"] = strings.Join(teams, ", ")
			}
		}
	}
	vars["mapCount"] = strconv.Itoa(count)
	return vars
}

// manualSetup tells the players of a game nobody hosts for them how to set it up: the party to form and the map to play.
func (d *gameDiscord) manualSetup(s *discordgo.Session, g Game) {
	vars := d.manualVars(g)
	d.post(s, g, "partySetup", embedData{Vars: map[string]string{"inviteCommand": vars["inviteCommand"]}})
	if g.Map == "" || g.Map == MapUndecided {
		return
	}
	d.post(s, g, "mapSetup", embedData{Vars: vars})
}

// embedState is what the live embeds of a running match remember: their messages and the match state they show.
type embedState struct {
	msgs       map[string]string // live embed key -> message id
	eliminated map[string]bool   // user id -> out of the match
	bedLost    map[string]bool   // team -> bed gone
	startedAt  time.Time
	pending    bool            // a tracker refresh is already scheduled
	posted     map[string]bool // embeds already posted in this game, so a shared block never posts the welcome or the teams twice
	invite     embedData       // the last Party Invitation, posted again under each reminder
	hasInvite  bool
}

// postReminderWithInvitation is the party reminder: the Party Invitation moves to the bottom of the chat as one message that
// carries the reminder text (pinging the players who have not joined) above the embed, with the latest lists. The old invitation
// is deleted. With no invitation to resend, or a reminder the guild switched off, it falls back to the reminder on its own.
func (d *gameDiscord) postReminderWithInvitation(s *discordgo.Session, g Game, st *embedState, reminder embedData) {
	d.mu.Lock()
	invite, ok := st.invite, st.hasInvite
	d.mu.Unlock()
	emb, t, built := d.build(g, "partyReminder", reminder)
	if !built || emb == nil {
		return // the guild switched the reminder off: nothing is said, and the invitation stays where it is
	}
	if !ok || t.Content == nil {
		d.post(s, g, "partyReminder", reminder)
		return
	}
	reminder.Game = g
	// The invitation keeps its own lists (the host sends it again right before each reminder): the reminder only names who is
	// still waiting, so copying its lists over would empty Joined and Invited.
	invite.Content = strings.TrimSpace(fill(*t.Content, reminder.common()))
	d.mu.Lock()
	st.invite = invite
	d.mu.Unlock()
	d.resendPartyInvitation(s, g, st)
	d.mu.Lock()
	st.invite.Content = "" // only this resend carries the reminder
	d.mu.Unlock()
}

// resendPartyInvitation moves the Party Invitation to the bottom of the chat after a reminder: the old message is deleted and the
// embed is posted again (with the latest joined/waiting/invited lists the reminder carried, when it had any).
func (d *gameDiscord) resendPartyInvitation(s *discordgo.Session, g Game, st *embedState) {
	d.mu.Lock()
	data, ok, old := st.invite, st.hasInvite, st.msgs["partyInvitation"]
	d.mu.Unlock()
	if !ok {
		return
	}
	// Already the newest message: nothing to move, so edit it in place with the latest lists instead of posting another.
	if old != "" {
		if latest, err := s.ChannelMessages(g.Channels.Text, 1, "", "", ""); err == nil && len(latest) == 1 && latest[0].ID == old {
			if d.post(s, g, "partyInvitation", data) {
				return
			}
		}
	}
	d.mu.Lock()
	delete(st.msgs, "partyInvitation")
	d.mu.Unlock()
	if old != "" {
		if err := s.ChannelMessageDelete(g.Channels.Text, old); err != nil {
			log.Printf("delete party invitation for game %d: %v", g.ID, err)
		}
	}
	d.post(s, g, "partyInvitation", data)
}

func (d *gameDiscord) state(id uint) *embedState {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.live[id]
	if st == nil {
		st = &embedState{msgs: map[string]string{}, eliminated: map[string]bool{}, bedLost: map[string]bool{}, posted: map[string]bool{}}
		d.live[id] = st
	}
	return st
}

func (d *gameDiscord) forget(id uint) {
	d.mu.Lock()
	delete(d.live, id)
	delete(d.introductions, id)
	d.mu.Unlock()
	ClearVoidVotes(id)
}

// runManual runs the blocks of a hand-hosted game, in order: the same flow blocks as a bot-hosted game, minus everything that needs the bot.
// An Announce posts its message (a custom one, or a built-in embed even when it is switched off), a Wait pauses, and a Group runs its blocks
// one by one or all at once. Anything else is the bot's business and is skipped.
func (d *gameDiscord) runManual(s *discordgo.Session, g Game, steps []flowStep) {
	for _, st := range steps {
		if st.Enabled != nil && !*st.Enabled {
			continue
		}
		if cur, ok := Games.Get(g.ID); ok {
			g = cur // the map or the teams may have changed while an earlier block waited
		}
		switch st.Type {
		case "announce":
			d.manualAnnounce(s, g, st)
		case "wait":
			time.Sleep(time.Duration(st.WaitSec * float64(time.Second)))
		case "group":
			if st.Mode != "parallel" {
				d.runManual(s, g, st.Children)
				continue
			}
			var wg sync.WaitGroup
			for _, child := range st.Children {
				wg.Add(1)
				go func() {
					defer wg.Done()
					d.runManual(s, g, []flowStep{child})
				}()
			}
			wg.Wait()
		}
	}
}

// manualAnnounce posts one Announce block of a hand-hosted game. The welcome and the teams are posted once per game, however many blocks
// (or the bot path before a fallback) ask for them.
func (d *gameDiscord) manualAnnounce(s *discordgo.Session, g Game, st flowStep) {
	flow := flowOf(g.GuildID)
	if flow.Replies[st.ID] != nil {
		d.postStepReply(s, g, st.ID)
		return
	}
	if st.Embed == "" {
		return
	}
	state := d.state(g.ID)
	d.mu.Lock()
	already := state.posted[st.Embed]
	d.mu.Unlock()
	if already && (st.Embed == "introduction" || st.Embed == "teams") {
		return
	}
	data := embedData{Force: true, Vars: d.manualVars(g)}
	if st.Embed == "mapSetup" {
		data.MapRows = d.manualMapRows(g)
	}
	d.post(s, g, st.Embed, data)
}

// manualMapRows lists every map of the rotation with its max height and team colours, as the old bot's self-hosted map setup did: nobody
// votes, the players pick from this list.
func (d *gameDiscord) manualMapRows(g Game) []map[string]string {
	maps, _ := helpers.GuildMetaMaps(g.GuildID)
	rows := make([]map[string]string, 0, len(maps))
	for _, m := range maps {
		if !m.IsEnabled || !m.IsMapAvailable || strings.TrimSpace(m.Name) == "" {
			continue
		}
		height, teams := "N/A", "None"
		if m.MapHeight > 0 {
			height = strconv.Itoa(m.MapHeight)
		}
		if t := m.TeamsFor(g.Mode.Name); len(t) > 0 {
			teams = strings.Join(t, ", ")
		}
		rows = append(rows, map[string]string{"mapName": m.Name, "mapHeight": height, "mapTeams": teams})
	}
	sort.Slice(rows, func(i, j int) bool { return strings.ToLower(rows[i]["mapName"]) < strings.ToLower(rows[j]["mapName"]) })
	return rows
}

// announceBeforeDraft posts the introduction (welcome to the mode, who is playing) unless it already went out for this game.
func (d *gameDiscord) announceBeforeDraft(s *discordgo.Session, g Game) {
	state := d.state(g.ID)
	d.mu.Lock()
	already := d.introductions[g.ID] || state.posted["introduction"]
	d.mu.Unlock()
	if already {
		return
	}
	if d.post(s, g, "introduction", embedData{Force: flowOf(g.GuildID).Announced["introduction"]}) {
		d.mu.Lock()
		d.introductions[g.ID] = true
		d.mu.Unlock()
	}
}

// postIntroduction sends the pipeline's introduction once teams are known, but before the teams and map-vote
// announcements. The engine's announce step still runs first in its own pipeline; its later echo is suppressed.
func (d *gameDiscord) postIntroduction(s *discordgo.Session, g Game) {
	if !flowOf(g.GuildID).Announced["introduction"] {
		return
	}
	d.mu.Lock()
	already := d.introductions[g.ID] // a captain draft posts it before the picks
	d.mu.Unlock()
	if already {
		return
	}
	if d.post(s, g, "introduction", embedData{}) {
		d.mu.Lock()
		d.introductions[g.ID] = true
		d.mu.Unlock()
	}
}

// build renders the guild's template for key against the game as it is now. ok is false for a key without a default. The
// embed is nil when the guild switched it off (unless data.Force).
func (d *gameDiscord) build(g Game, key string, data embedData) (emb *discordgo.MessageEmbed, t embedTemplate, ok bool) {
	flow := flowOf(g.GuildID)
	t, ok = flow.embedFor(key)
	if !ok {
		return nil, t, false
	}
	st := d.state(g.ID)
	d.mu.Lock()
	elim, bed := maps.Clone(st.eliminated), maps.Clone(st.bedLost)
	started := st.startedAt
	d.mu.Unlock()

	data.Game, data.Flow, data.Eliminated, data.BedLost = g, flow, elim, bed
	if !started.IsZero() {
		vars := maps.Clone(data.Vars)
		if vars == nil {
			vars = map[string]string{}
		}
		vars["startedAt"] = strconv.FormatInt(started.Unix(), 10)
		data.Vars = vars
	}
	return renderEmbed(key, t, data), t, true
}

// post renders the guild's template for key and sends it, or edits the earlier message of a live embed.
func (d *gameDiscord) post(s *discordgo.Session, g Game, key string, data embedData) bool {
	emb, t, ok := d.build(g, key, data)
	if !ok || emb == nil {
		return false
	}
	st := d.state(g.ID)
	d.mu.Lock()
	kind := embedKinds[key]
	msgID := st.msgs[kind.messageOf(key)]
	d.mu.Unlock()
	helpers.WithGuildFooterIconForGuild(s, g.GuildID, emb)
	live := kind.Live
	buttons := hostButtons(g, key, t)
	// The text above the embed, when the template has one (variables work, {mentions} pings the players).
	data.Game = g
	var content *string
	if t.Content != nil {
		text := strings.TrimSpace(fill(*t.Content, data.common()))
		content = &text
	}
	if kind.Plain {
		// A reminder is a plain message sent anew every time, so it pings again; the embed itself is not sent.
		if content == nil || *content == "" {
			return false
		}
		_, err := s.ChannelMessageSendComplex(g.Channels.Text, &discordgo.MessageSend{Content: *content, AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeUsers}}})
		return err == nil
	}
	if live && msgID != "" {
		if content == nil && kind.Message != "" {
			none := "" // the message it edits may carry text of an earlier state
			content = &none
		}
		embeds := []*discordgo.MessageEmbed{emb}
		edit := &discordgo.MessageEdit{Channel: g.Channels.Text, ID: msgID, Embeds: &embeds, Content: content}
		if key == "partyReady" { // the party is complete: the invitation's host buttons have nothing left to do
			none := []discordgo.MessageComponent{}
			edit.Components = &none
		} else if buttons != nil {
			edit.Components = &buttons
		}
		if _, err := s.ChannelMessageEditComplex(edit); err == nil {
			return true
		}
	}
	if key == "partyReady" {
		buttons = nil
	}
	send := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{emb}, Components: buttons}
	if len(data.File) > 0 {
		send.Files = []*discordgo.File{{Name: "results.png", ContentType: "image/png", Reader: bytes.NewReader(data.File)}}
		if embedIsEmpty(emb) || (t.Box != nil && !*t.Box) {
			send.Embeds = nil // nothing to show in a box: the message is its text and the picture, like a plain post
		} else if emb.Image == nil {
			emb.Image = &discordgo.MessageEmbedImage{URL: "attachment://results.png"}
		}
	}
	if content != nil { // the template says what goes above the embed, even nothing
		send.Content = *content
	} else if key == "scored" && len(data.File) > 0 { // no text set: say who won and who lost, as the old bot did
		send.Content = resultsSummary(g, resultsOutcome(g, false))
	}
	if data.Content != "" {
		send.Content = data.Content
	}
	if send.Content != "" {
		send.AllowedMentions = &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeUsers}}
		if key == "scored" {
			// Everyone is named in the results, but only the players who keep Ping On Results on (/settings) are notified.
			send.AllowedMentions = &discordgo.MessageAllowedMentions{Users: resultPingUsers(g)}
		}
	}
	channel := g.Channels.Text
	if key == "scored" {
		channel = ResultsChannelFor(g.GuildID, channel) // results go to the profile's channel, else game_logs, not the game chat that gets cleaned up
	}
	m, err := s.ChannelMessageSendComplex(channel, send)
	if err != nil {
		return false
	}
	d.mu.Lock()
	st.posted[key] = true
	if live {
		st.msgs[kind.messageOf(key)] = m.ID
	}
	d.mu.Unlock()
	return true
}

// startTracker posts the live match tracker the moment the bot starts tracking.
func (d *gameDiscord) startTracker(s *discordgo.Session, g Game) {
	st := d.state(g.ID)
	d.mu.Lock()
	st.startedAt = time.Now()
	d.mu.Unlock()
	d.post(s, g, "liveTracker", embedData{})
}

// refreshTracker edits the live tracker, at most once every moment even when kills come in bursts.
func (d *gameDiscord) refreshTracker(s *discordgo.Session, g Game) {
	st := d.state(g.ID)
	d.mu.Lock()
	if st.pending {
		d.mu.Unlock()
		return
	}
	st.pending = true
	d.mu.Unlock()
	time.AfterFunc(1500*time.Millisecond, func() {
		d.mu.Lock()
		st.pending = false
		d.mu.Unlock()
		d.post(s, g, "liveTracker", embedData{})
	})
}

func mention(id string) string {
	if id == "" {
		return "someone"
	}
	return "<@" + id + ">"
}

// matchEvent updates what the live tracker shows and posts the in-game embeds the guild switched on.
func (d *gameDiscord) matchEvent(s *discordgo.Session, g Game, ev *MatchEvent) {
	if ev == nil {
		return
	}
	st := d.state(g.ID)
	switch ev.Kind {
	case "kill":
		killVars := map[string]string{"killer": mention(ev.Killer), "victim": mention(ev.Victim)}
		if ev.FirstBlood {
			d.post(s, g, "firstBlood", embedData{Vars: killVars})
		}
		if !ev.Final {
			d.post(s, g, "kill", embedData{Vars: killVars})
			return
		}
		d.mu.Lock()
		st.eliminated[ev.Victim] = true
		d.mu.Unlock()
		d.scheduleTeamStatuses(s, g, false)
		d.post(s, g, "finalKill", embedData{Vars: map[string]string{"killer": mention(ev.Killer), "victim": mention(ev.Victim)}})
	case "bed":
		d.mu.Lock()
		st.bedLost[ev.Team] = true
		d.mu.Unlock()
		d.scheduleTeamStatuses(s, g, false)
		d.post(s, g, "bedDestroyed", embedData{Vars: map[string]string{"team": ev.Team, "destroyer": mention(ev.Destroyer)}})
	case "eliminated":
		d.mu.Lock()
		st.bedLost[ev.Team] = true
		for _, p := range g.TeamPlayers(ev.Team) {
			st.eliminated[p.UserID] = true
		}
		d.mu.Unlock()
		d.scheduleTeamStatuses(s, g, false)
		d.post(s, g, "teamEliminated", embedData{Vars: map[string]string{"team": ev.Team}})
	case "phase":
		name := ev.PhaseName
		if name == "" {
			name = fmt.Sprintf("Phase %d", ev.Phase)
		}
		d.post(s, g, "phaseChanged", embedData{Vars: map[string]string{"phase": strconv.Itoa(ev.Phase), "phaseName": name}})
		return
	case "stat":
		if !flowOf(g.GuildID).announces(ev.Stat) {
			return
		}
		d.post(s, g, "statTracked", embedData{Vars: map[string]string{
			"stat": flowOf(g.GuildID).label(ev.Stat), "player": mention(ev.Player),
			"amount": strconv.FormatFloat(ev.Amount, 'f', -1, 64), "total": strconv.FormatFloat(ev.Total, 'f', -1, 64),
		}})
		return
	}
	d.refreshTracker(s, g)
}

// gatherForDraft moves everyone already in voice into the first team's voice channel while the captains pick, so they
// talk in one place; applyTeams sends each player to their own team's channel once the draft is done.
func (d *gameDiscord) gatherForDraft(s *discordgo.Session, g Game) {
	vcID := g.Channels.Voice[TeamName(0)]
	if vcID == "" {
		return
	}
	for _, p := range g.Players {
		if vs, err := s.State.VoiceState(g.GuildID, p.UserID); err != nil || vs == nil || vs.ChannelID == "" || vs.ChannelID == vcID {
			continue
		}
		_ = s.GuildMemberMove(g.GuildID, p.UserID, &vcID)
	}
}

// applyTeams moves players already in voice into their team's voice channel.
func (d *gameDiscord) applyTeams(g Game) error {
	s := d.session()
	if s == nil {
		return nil
	}
	for _, p := range g.Players {
		vcID := g.Channels.Voice[p.Team]
		if vcID == "" {
			continue
		}
		if vs, err := s.State.VoiceState(g.GuildID, p.UserID); err != nil || vs.ChannelID == "" {
			continue // not in voice: nothing to move
		}
		_ = s.GuildMemberMove(g.GuildID, p.UserID, &vcID)
	}
	return nil
}

// ============================================================================
// Map vote

// resultPingUsers are the players of the game who want to be pinged when it is scored (everyone, unless they switched Ping On
// Results off in /settings or their settings cannot be read).
func resultPingUsers(g Game) []string {
	users := make([]string, len(g.Players))
	keep := make([]bool, len(g.Players))
	var wg sync.WaitGroup
	for n, p := range g.Players {
		users[n], keep[n] = p.UserID, true
		wg.Add(1)
		go func() {
			defer wg.Done()
			if view, err := database.Must().Player.Get(g.GuildID, p.UserID); err == nil && view.Config != nil && !view.Config.IsPingOnResultsEnabled {
				keep[n] = false
			}
		}()
	}
	wg.Wait()
	out := make([]string, 0, len(users))
	for n, id := range users {
		if keep[n] {
			out = append(out, id)
		}
	}
	return out
}

// ScoredHooks run, each on its own, when a ranked game has been scored. Things that read results (the integrity check) register here, as the
// match package cannot depend on them.
var ScoredHooks []func(s *discordgo.Session, g Game)
