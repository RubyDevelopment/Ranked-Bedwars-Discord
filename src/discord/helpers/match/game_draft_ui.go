package match

import (
	"fmt"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/banner"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

type draftUI struct {
	channelID string
	messageID string
	turnKey   string            // changes whenever someone picks, so a stale auto-pick timer does nothing
	endsAt    int64             // when the current pick times out (Unix seconds), 0 without a time limit
	prev      [][]string        // the rosters as the message last showed them, to tell who a pick pulled along
	ratios    map[string]string // user id -> "W/L 1.22 | K/D 0.74" for the pick menu, looked up once when the draft starts
}

// setTimer notes when the pick that starts now times out.
func (ui *draftUI) setTimer(seconds int) {
	ui.endsAt = 0
	if seconds > 0 {
		ui.endsAt = time.Now().Add(time.Duration(seconds) * time.Second).Unix()
	}
}

// draftRatios looks up every player's win/loss and kill/death ratio for the season, side by side, for the pick menu. A player
// with no stats yet reads 0.00; when the season or a lookup is unavailable the menu simply shows ELO alone.
func draftRatios(g Game) map[string]string {
	_, seasonID, err := helpers.ActiveSeasonKey(g.GuildID)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(g.Players))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range g.Players {
		wg.Add(1)
		go func() {
			defer wg.Done()
			text := "W/L 0.00 | K/D 0.00"
			if st, err := database.Must().Stats.Get(g.GuildID, p.UserID, seasonID); err == nil {
				text = "W/L " + banner.Ratio(st.Wins, st.Losses) + " | K/D " + banner.Ratio(st.Kills, st.Deaths)
			}
			mu.Lock()
			out[p.UserID] = text
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func (d *gameDiscord) startDraft(s *discordgo.Session, g Game) {
	view, err := Games.Draft(g.ID)
	if err != nil {
		return
	}
	// The welcome and the player list go out first, as they do for a game with automatic teams (which only posts them once
	// the teams exist, after the draft would already be under way); then the draft starts.
	d.announceBeforeDraft(s, g)
	d.gatherForDraft(s, g)
	ui := &draftUI{channelID: g.Channels.Text, ratios: draftRatios(g)}
	ui.setTimer(Games.FlowSettings(g.GuildID).CaptainFor(g.Mode).PickTimeoutSeconds)
	embed := d.draftEmbed(s, g, view, ui)
	helpers.WithGuildFooterIconForGuild(s, g.GuildID, embed)
	msg, err := s.ChannelMessageSendComplex(g.Channels.Text, &discordgo.MessageSend{
		Content:    draftPing(view),
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: draftComponents(g, view, ui.ratios),
	})
	if err != nil {
		return
	}
	ui.messageID = msg.ID
	// The captains' party mates were put on their teams before the first pick.
	captainsOnly := make([][]string, len(view.Teams))
	for t, members := range view.Teams {
		captainsOnly[t] = members[:min(1, len(members))]
	}
	d.announceAutoAssigned(s, g, captainsOnly, view.Teams, false)
	ui.prev = cloneTeams(view.Teams)
	d.mu.Lock()
	d.drafts[g.ID] = ui
	d.mu.Unlock()
	d.armDraftTimer(s, g.ID, ui)
}

// armDraftTimer auto-picks for a captain who takes too long.
func (d *gameDiscord) armDraftTimer(s *discordgo.Session, gameID uint, ui *draftUI) {
	g, ok := Games.Get(gameID)
	if !ok {
		return
	}
	seconds := Games.FlowSettings(g.GuildID).CaptainFor(g.Mode).PickTimeoutSeconds
	if seconds <= 0 {
		return
	}
	d.mu.Lock()
	ui.turnKey = strconv.FormatInt(time.Now().UnixNano(), 10)
	key := ui.turnKey
	d.mu.Unlock()

	// Halfway through, the captain is reminded.
	if view, err := Games.Draft(gameID); err == nil && view.Turn >= 0 && view.Turn < len(view.Teams) && len(view.Teams[view.Turn]) > 0 {
		captain := view.Teams[view.Turn][0]
		time.AfterFunc(time.Duration(seconds)*time.Second/2, func() {
			d.mu.Lock()
			current := d.drafts[gameID] == ui && ui.turnKey == key
			d.mu.Unlock()
			if current {
				d.say(s, ui.channelID, "<@"+captain+"> ⏰ **Half the time has passed!** Please make your selection soon.")
			}
		})
	}

	time.AfterFunc(time.Duration(seconds)*time.Second, func() {
		d.mu.Lock()
		current := d.drafts[gameID] == ui && ui.turnKey == key
		d.mu.Unlock()
		if !current {
			return
		}
		teamName := ""
		if before, err := Games.Draft(gameID); err == nil && before.Turn >= 0 {
			teamName = TeamName(before.Turn)
		}
		if captain, picked, err := Games.DraftAutoPick(gameID); err == nil {
			d.say(s, ui.channelID, "<@"+captain+"> has picked <@"+picked+"> for **"+teamName+"** (auto-pick: timeout)")
			d.refreshDraft(s, gameID, ui)
		}
	})
}

// refreshDraft redraws the draft message after a pick, or closes it when the draft is done.
func (d *gameDiscord) refreshDraft(s *discordgo.Session, gameID uint, ui *draftUI) {
	g, ok := Games.Get(gameID)
	if !ok {
		return
	}
	view, err := Games.Draft(gameID)
	d.mu.Lock()
	before := ui.prev
	d.mu.Unlock()
	if err != nil || view.Turn < 0 {
		d.mu.Lock()
		delete(d.drafts, gameID)
		d.mu.Unlock()
		// The teams embed that follows shows the finished rosters, so the picking message goes.
		if s.ChannelMessageDelete(ui.channelID, ui.messageID) != nil {
			done := d.draftEmbed(s, g, view, ui)
			done.Description = "The draft is complete."
			helpers.WithGuildFooterIconForGuild(s, g.GuildID, done)
			_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{Channel: ui.channelID, ID: ui.messageID, Embeds: &[]*discordgo.MessageEmbed{done}, Components: &[]discordgo.MessageComponent{}})
		}
		if err == nil {
			d.announceAutoAssigned(s, g, before, withoutIDs(view.Teams, view.AutoFilled), true)
			d.announceLastPlayer(s, g, view)
		}
		return
	}
	d.mu.Lock()
	ui.setTimer(Games.FlowSettings(g.GuildID).CaptainFor(g.Mode).PickTimeoutSeconds)
	d.mu.Unlock()
	embed := d.draftEmbed(s, g, view, ui)
	helpers.WithGuildFooterIconForGuild(s, g.GuildID, embed)
	// The old picking message goes and a fresh one is posted below the pick line, so it is the newest message (and pings
	// the next captain again).
	d.mu.Lock()
	oldID := ui.messageID
	d.mu.Unlock()
	msg, sendErr := s.ChannelMessageSendComplex(ui.channelID, &discordgo.MessageSend{
		Content: draftPing(view), Embeds: []*discordgo.MessageEmbed{embed}, Components: draftComponents(g, view, ui.ratios),
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeUsers}},
	})
	if sendErr == nil {
		_ = s.ChannelMessageDelete(ui.channelID, oldID)
		d.mu.Lock()
		ui.messageID = msg.ID
		d.mu.Unlock()
	} else { // could not repost: keep the old message up to date instead
		_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
			Channel: ui.channelID, ID: oldID, Content: ptr(draftPing(view)),
			Embeds: &[]*discordgo.MessageEmbed{embed}, Components: ptr(draftComponents(g, view, ui.ratios)),
		})
	}
	d.announceAutoAssigned(s, g, before, view.Teams, true)
	d.mu.Lock()
	ui.prev = cloneTeams(view.Teams)
	d.mu.Unlock()
	d.armDraftTimer(s, gameID, ui)
}

func ptr[T any](v T) *T { return &v }

func draftPing(view DraftView) string {
	if view.Turn < 0 || view.Turn >= len(view.Teams) || len(view.Teams[view.Turn]) == 0 {
		return ""
	}
	return "<@" + view.Teams[view.Turn][0] + ">"
}

// PlayerRatios maps each user id to their record, "**12W/9L** · **30K/40D**", for the active season (zeros without stats yet);
// nil when the guild has no active season.
func PlayerRatios(guildID string, ids []string) map[string]string {
	_, seasonID, err := helpers.ActiveSeasonKey(guildID)
	if err != nil || seasonID == 0 {
		return nil
	}
	out := make(map[string]string, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wl, kd := "0W/0L", "0K/0D"
			if st, err := database.Must().Stats.Get(guildID, id, seasonID); err == nil {
				wl, kd = fmt.Sprintf("%dW/%dL", st.Wins, st.Losses), fmt.Sprintf("%dK/%dD", st.Kills, st.Deaths)
			}
			mu.Lock()
			out[id] = "**" + wl + "** · **" + kd + "**"
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// draftRows are the rosters of the captain draft for the "captainPicking" template's per-team fields. The draft has no
// teams on the game yet, so they come from the draft's own view.
func draftRows(g Game, view DraftView, t embedTemplate) []map[string]string {
	def := defaultPlayerLines["draft"]
	line, empty := orDefault(t.PlayerLine, def.line), orDefault(t.TeamEmpty, def.empty)
	sep := "\n"
	if t.PlayerSeparator != nil && *t.PlayerSeparator != "" {
		sep = strings.ReplaceAll(*t.PlayerSeparator, `\n`, "\n")
	}
	rows := make([]map[string]string, 0, len(view.Teams))
	for ti, members := range view.Teams {
		lines := make([]string, 0, len(members))
		for i, id := range members {
			name, elo := id, "0"
			if p, ok := g.Player(id); ok {
				name, elo = p.Username, strconv.Itoa(p.EloBefore)
			}
			captain := ""
			if i == 0 {
				captain = "👑"
			}
			lines = append(lines, strings.TrimSpace(fill(line, map[string]string{
				"mention": "<@" + id + ">", "name": name, "id": id, "captain": captain, "team": TeamName(ti), "elo": elo,
			})))
		}
		if len(lines) == 0 {
			lines = []string{empty}
		}
		rows = append(rows, map[string]string{"team": TeamName(ti), "teamPlayers": strings.Join(lines, sep), "bed": ""})
	}
	return rows
}

// draftEmbed is the captain draft as the guild's "captainPicking" template draws it: whose turn it is and the rosters so far.
// The draft cannot be skipped, so a template the guild switched off is drawn anyway.
func (d *gameDiscord) draftEmbed(s *discordgo.Session, g Game, view DraftView, ui *draftUI) *discordgo.MessageEmbed {
	flow := flowOf(g.GuildID)
	t, _ := flow.embedFor("captainPicking")
	vars := map[string]string{"timeLeft": "No time limit"}
	if ui != nil && ui.endsAt > 0 {
		vars["timeLeft"] = "<t:" + strconv.FormatInt(ui.endsAt, 10) + ":R>"
		vars["endsAt"] = strconv.FormatInt(ui.endsAt, 10)
	}
	if view.Turn >= 0 && view.Turn < len(view.Teams) && len(view.Teams[view.Turn]) > 0 {
		captain := view.Teams[view.Turn][0]
		vars["captain"], vars["team"] = "<@"+captain+">", TeamName(view.Turn)
		vars["captainName"] = captain
		if p, ok := g.Player(captain); ok {
			vars["captainName"] = p.Username
		}
		if s != nil {
			if u, err := s.User(captain); err == nil {
				vars["captainAvatar"] = banner.AvatarOf(u)
			}
		}
	}
	remaining := make([]string, 0, len(view.Pool))
	ratios := PlayerRatios(g.GuildID, view.Pool)
	for _, id := range view.Pool {
		line := "- <@" + id + ">"
		if p, ok := g.Player(id); ok {
			line += fmt.Sprintf(" · ELO: `%d`", p.EloBefore)
		}
		if r, ok := ratios[id]; ok {
			line += " · " + r
		}
		remaining = append(remaining, line)
	}
	vars["remaining"] = "`None`"
	if len(remaining) > 0 {
		vars["remaining"] = strings.Join(remaining, "\n")
	}
	emb, _, _ := d.build(g, "captainPicking", embedData{Vars: vars, TeamRows: draftRows(g, view, t), Force: true})
	if emb == nil {
		emb = &discordgo.MessageEmbed{Title: "Captain Picking", Description: "Captains pick their teams in turn."}
	}
	return emb
}

// announceAutoAssigned posts the "partyMembersAutoAssigned" embed for the players a pick pulled onto a team to stay with
// their party. before is the rosters as they were when the message was last drawn.
func (d *gameDiscord) announceAutoAssigned(s *discordgo.Session, g Game, before, after [][]string, picked bool) {
	for t, members := range after {
		from := 0
		if t < len(before) {
			from = len(before[t])
		}
		if from >= len(members) {
			continue
		}
		added := members[from:]
		if picked && from > 0 && len(added) > 0 { // the first newcomer is the pick itself
			added = added[1:]
		}
		if len(added) == 0 || len(members) == 0 {
			continue
		}
		names := make([]string, len(added))
		for i, id := range added {
			names[i] = "<@" + id + ">"
		}
		// "to stay with" the player the pick pulled them along with (the pick itself), or the captain for the first assignment.
		captain := members[0]
		if picked && from > 0 && from < len(members) {
			captain = members[from]
		}
		captain = "<@" + captain + ">"
		wasWere := "were"
		if len(added) == 1 {
			wasWere = "was"
		}
		d.post(s, g, "partyMembersAutoAssigned", embedData{Vars: map[string]string{
			"names": strings.Join(names, ", "), "wasWere": wasWere, "team": TeamName(t), "captain": captain, "count": strconv.Itoa(len(added)),
		}})
	}
}

// withoutIDs is the teams without the given players.
func withoutIDs(teams [][]string, ids []string) [][]string {
	out := make([][]string, len(teams))
	for t, members := range teams {
		out[t] = slices.DeleteFunc(slices.Clone(members), func(id string) bool { return slices.Contains(ids, id) })
	}
	return out
}

// announceLastPlayer says who was put on the last open team because nobody was left to pick.
func (d *gameDiscord) announceLastPlayer(s *discordgo.Session, g Game, view DraftView) {
	for _, id := range view.AutoFilled {
		for t, members := range view.Teams {
			if slices.Contains(members, id) {
				d.say(s, g.Channels.Text, "<@"+id+"> was the last player left, so they were placed on **"+TeamName(t)+"** automatically.")
			}
		}
	}
}

func cloneTeams(in [][]string) [][]string {
	out := make([][]string, len(in))
	for i, t := range in {
		out[i] = slices.Clone(t)
	}
	return out
}

func draftComponents(g Game, view DraftView, ratios map[string]string) []discordgo.MessageComponent {
	if view.Turn < 0 || len(view.Pool) == 0 {
		return nil
	}
	options := make([]discordgo.SelectMenuOption, 0, len(view.Pool))
	for _, id := range view.Pool {
		label, desc := id, ""
		if p, ok := g.Player(id); ok {
			label, desc = p.Username, fmt.Sprintf("ELO: %d", p.EloBefore)
			if r := ratios[id]; r != "" {
				desc += " | " + r
			}
			if g.inParty(id) {
				label += " (Party)"
				desc += " | In a party"
			}
		}
		options = append(options, discordgo.SelectMenuOption{Label: truncateName(label, 100), Value: id, Description: desc})
	}
	if len(options) > 25 {
		options = options[:25]
	}
	return []discordgo.MessageComponent{&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.SelectMenu{CustomID: draftIDPrefix + strconv.FormatUint(uint64(g.ID), 10), Placeholder: "Select a player to pick...", Options: options},
	}}}
}

// ============================================================================
// Component handling
