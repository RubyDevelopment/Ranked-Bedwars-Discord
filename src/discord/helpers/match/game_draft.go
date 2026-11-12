package match

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/partymgr"
	"ranked-bedwars/src/discord/helpers/queue"
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

// DraftConfig is defined in package types (games.go).
type DraftConfig = types.DraftConfig

// Draft is a captain draft in progress. Teams[i][0] is team i's captain.
type Draft struct {
	Teams   [][]string
	PerTeam int
	Type    string // effective picking type
	pool    []string
	ratings map[string]int
	partyOf map[string][]string
	order   []int // team indexes, weakest team first
	auto    bool
	rng     *rand.Rand
	filled  []string // the players who were put on the last open team without a pick
	picks   int      // picks the captains have made (party mates and the last player placed for them do not count)
}

// NewDraft elects captains and returns the draft. Captains come from the top
// 2*teams players by rating: the top `teams` of them with probability
// TopEloChance, otherwise a random `teams` of them. With OneCaptainPerParty two
// captains never share a party.
func NewDraft(players []string, teams, perTeam int, ratings map[string]int, parties []partymgr.PartyGroup, cfg DraftConfig, r *rand.Rand) (*Draft, error) {
	if teams < 2 || perTeam < 1 || len(players) != teams*perTeam {
		return nil, ErrBadRoster
	}
	if len(players) < 2 {
		return nil, ErrDraftTooFew
	}
	r = rngOr(r)
	d := &Draft{PerTeam: perTeam, ratings: ratings, partyOf: map[string][]string{}, auto: cfg.AutoAssignParties, rng: r}
	roster := map[string]bool{}
	for _, id := range players {
		roster[id] = true
	}
	hasGroups := false
	for _, pg := range parties {
		var g []string
		for _, id := range pg.Members {
			if roster[id] {
				g = append(g, id)
			}
		}
		if len(g) >= 2 {
			hasGroups = true
			for _, id := range g {
				d.partyOf[id] = g
			}
		}
	}

	byElo := slices.Clone(players)
	sort.SliceStable(byElo, func(i, j int) bool {
		if ratings[byElo[i]] != ratings[byElo[j]] {
			return ratings[byElo[i]] > ratings[byElo[j]]
		}
		return byElo[i] < byElo[j]
	})
	top := slices.Clone(byElo[:min(teams*2, len(byElo))])
	chance := cfg.TopEloChance
	if chance > 1 { // tolerate a 0-100 percentage
		chance /= 100
	}
	chance = min(max(chance, 0), 1)
	if r.Float64() >= chance {
		r.Shuffle(len(top), func(i, j int) { top[i], top[j] = top[j], top[i] })
	}
	captains := slices.Clone(top[:teams])

	if cfg.OneCaptainPerParty && hasGroups {
		sharing := func(a, b string) bool { return slices.Contains(d.partyOf[a], b) }
		for i := 0; i < len(captains); i++ {
			clash := false
			for j := 0; j < i; j++ {
				clash = clash || sharing(captains[i], captains[j])
			}
			if !clash {
				continue
			}
			for _, cand := range byElo { // best remaining player outside every captain's party
				if slices.Contains(captains, cand) {
					continue
				}
				ok := true
				for k, c := range captains {
					ok = ok && (k == i || !sharing(cand, c))
				}
				if ok {
					captains[i] = cand
					break
				}
			}
		}
	}
	r.Shuffle(len(captains), func(i, j int) { captains[i], captains[j] = captains[j], captains[i] })

	d.Teams = make([][]string, teams)
	for i, c := range captains {
		d.Teams[i] = []string{c}
	}
	for _, id := range players {
		if !slices.Contains(captains, id) {
			d.pool = append(d.pool, id)
		}
	}
	captainInParty := false
	for _, c := range captains {
		captainInParty = captainInParty || len(d.partyOf[c]) > 0
	}
	d.Type = cfg.PickingType
	if d.Type == "" {
		d.Type = "one_per_person"
	}
	if hasGroups && captainInParty && len(players) >= cfg.AlternatingMinPlayers && cfg.AlternatingMinPlayers > 0 {
		d.Type = "alternating"
	}
	if d.auto {
		for t := range d.Teams {
			d.pull(t, d.Teams[t][0])
		}
	}
	// The team that picks first (the weakest, by the ELO of its captain and the players who came with them) is always Team A, so
	// the first captain shown is the first to pick.
	sort.SliceStable(d.Teams, func(a, b int) bool { return d.teamElo(a) < d.teamElo(b) })
	d.order = make([]int, teams)
	for i := range d.order {
		d.order[i] = i
	}
	return d, nil
}

func (d *Draft) teamElo(t int) int {
	sum := 0
	for _, id := range d.Teams[t] {
		sum += d.ratings[id]
	}
	return sum
}

// Pool is who can still be picked.
func (d *Draft) Pool() []string { return slices.Clone(d.pool) }

// Captains lists each team's captain, by team index.
func (d *Draft) Captains() []string {
	out := make([]string, len(d.Teams))
	for i, t := range d.Teams {
		out[i] = t[0]
	}
	return out
}

// pull moves id's party mates from the pool onto team t while it has room.
func (d *Draft) pull(t int, id string) {
	for _, mate := range d.partyOf[id] {
		if len(d.Teams[t]) >= d.PerTeam {
			return
		}
		if i := slices.Index(d.pool, mate); i >= 0 {
			d.pool = slices.Delete(d.pool, i, i+1)
			d.Teams[t] = append(d.Teams[t], mate)
		}
	}
}

func (d *Draft) open() []int {
	var open []int
	for t := range d.Teams {
		if len(d.Teams[t]) < d.PerTeam {
			open = append(open, t)
		}
	}
	return open
}

// alternatingQuota is how many picks the team at weakness rank i makes in the
// 4-captain alternating pattern; the last team is filled automatically.
var alternatingQuota = [...]int{1, 2, 2, 0}

// Turn returns the team that picks next, or -1 when the draft is over. In the
// 4-team alternating pattern the quota-0 team is filled from the pool as soon
// as the other teams have made their picks.
func (d *Draft) Turn() int {
	for {
		open := d.open()
		if len(open) == 0 {
			return -1
		}
		isOpen := func(t int) bool { return slices.Contains(d.open(), t) }

		if d.Type == "alternating" && len(d.Teams) == 4 {
			for rank, t := range d.order {
				if q := alternatingQuota[rank]; q > 0 && len(d.Teams[t])-1 < q && isOpen(t) {
					return t
				}
			}
			filled := false
			for rank, t := range d.order {
				for alternatingQuota[rank] == 0 && isOpen(t) && len(d.pool) > 0 {
					d.take(t, d.pool[d.rng.IntN(len(d.pool))])
					filled = true
				}
			}
			if filled {
				continue
			}
		}
		if len(open) == 1 {
			// Only one team has room, so whoever is left in the pool goes there: nobody has to pick the last player.
			if len(d.pool) > 0 {
				d.filled = append(d.filled, d.pool...)
				for len(d.pool) > 0 && len(d.Teams[open[0]]) < d.PerTeam {
					d.take(open[0], d.pool[0])
				}
				continue
			}
			return open[0]
		}
		if d.Type == "alternating" && len(d.Teams) == 2 {
			// 1-2-2-1: the weaker team picks once, then the other picks twice, then the weaker twice, and so on in pairs. It is
			// counted in picks, not in players, so a party mate who came along with a pick does not cost a captain a turn.
			lo, hi := d.order[0], d.order[1]
			pick := lo
			if d.picks > 0 {
				pick = hi
				if ((d.picks-1)/2)%2 == 1 {
					pick = lo
				}
			}
			if isOpen(pick) {
				return pick
			}
			return open[0]
		}
		best := open[0]
		for _, t := range open[1:] {
			if len(d.Teams[t]) < len(d.Teams[best]) {
				best = t
			}
		}
		return best
	}
}

func (d *Draft) take(t int, id string) {
	if i := slices.Index(d.pool, id); i >= 0 {
		d.pool = slices.Delete(d.pool, i, i+1)
	}
	d.Teams[t] = append(d.Teams[t], id)
	if d.auto {
		d.pull(t, id)
	}
}

// Pick makes captain pick player for their team. It must be that team's turn.
func (d *Draft) Pick(captain, player string) (team int, err error) {
	turn := d.Turn()
	if turn < 0 {
		return -1, ErrDraftOver
	}
	t := slices.IndexFunc(d.Teams, func(tm []string) bool { return tm[0] == captain })
	if t < 0 {
		return -1, ErrNotACaptain
	}
	if t != turn {
		return -1, ErrNotYourTurn
	}
	if !slices.Contains(d.pool, player) {
		return -1, ErrNotInPool
	}
	d.take(t, player)
	d.picks++
	return t, nil
}

// AutoPick makes the current turn's pick at random (the captain timed out).
func (d *Draft) AutoPick() (captain, player string, err error) {
	t := d.Turn()
	if t < 0 {
		return "", "", ErrDraftOver
	}
	if len(d.pool) == 0 {
		return "", "", ErrNotInPool
	}
	player = d.pool[d.rng.IntN(len(d.pool))]
	d.take(t, player)
	d.picks++
	return d.Teams[t][0], player, nil
}

const (
	hostGameIDPrefix  = "hostgame1|"
	hostGameMaxTeams  = 4
	hostGameSessionTT = 20 * time.Minute
)

// The panel runs as four steps: mode → options → teams → review.
const (
	pageMode    = "mode"
	pageOptions = "options"
	pageTeams   = "teams"
	pageReview  = "review"
)

var hostGamePages = []string{pageMode, pageOptions, pageTeams, pageReview}

type hostMode struct {
	Mode       types.Mode
	ChannelID  string // a queue channel bound to the mode
	CategoryID string
}

type HostSession struct {
	ID, GuildID, UserID string
	Page                string
	Modes               []hostMode
	ModeIdx             int
	TeamCount           int
	Ranked, Stats       bool
	UseBot              bool
	Active              int
	Teams               [][]string
	Voice               []string
	Notice              string
	Created             time.Time
}

var hostSessions = struct {
	sync.Mutex
	byID map[string]*HostSession
}{byID: map[string]*HostSession{}}

func NewHostSession(s *discordgo.Session, guildID, userID string) (*HostSession, *discordgo.MessageEmbed) {
	snap, err := queue.Queues.Snapshot(guildID)
	if err != nil || snap == nil {
		return nil, HostGameErrorEmbed(errors.New("the queue configuration could not be loaded"))
	}

	// One entry per mode that an enabled queue channel is bound to: that queue supplies the game's category.
	channels := make([]string, 0, len(snap.Bindings))
	for id := range snap.Bindings {
		channels = append(channels, id)
	}
	sort.Strings(channels)
	seen := map[string]bool{}
	var modes []hostMode
	for _, id := range channels {
		b := snap.Bindings[id]
		key := strings.ToLower(b.Mode.Name)
		if seen[key] || len(modes) >= 25 {
			continue
		}
		seen[key] = true
		category := strings.TrimSpace(b.Queue.CategoryID)
		if category == "" {
			if ch, err := s.State.Channel(id); err == nil {
				category = ch.ParentID
			}
		}
		if category == "" {
			continue // nowhere to create the game's channels
		}
		modes = append(modes, hostMode{Mode: b.Mode, ChannelID: id, CategoryID: category})
	}
	sort.Slice(modes, func(a, b int) bool { return modes[a].Mode.Name < modes[b].Mode.Name })
	if len(modes) == 0 {
		return nil, HostGameErrorEmbed(errors.New("no game mode is attached to an enabled queue, so there is nothing to host. Configure a queue first"))
	}

	session := &HostSession{
		ID: strconv.FormatInt(time.Now().UnixNano(), 36), GuildID: guildID, UserID: userID, Page: pageMode, Modes: modes,
		TeamCount: 2, Ranked: modes[0].Mode.IsEloGainEnabled, Stats: modes[0].Mode.IsStatsTrackingEnabled, UseBot: true,
		Teams: make([][]string, hostGameMaxTeams), Voice: make([]string, hostGameMaxTeams), Created: time.Now(),
	}
	if len(modes) == 1 {
		session.Page = pageOptions
	}

	hostSessions.Lock()
	for id, old := range hostSessions.byID {
		if time.Since(old.Created) > hostGameSessionTT {
			delete(hostSessions.byID, id)
		}
	}
	hostSessions.byID[session.ID] = session
	hostSessions.Unlock()
	return session, nil
}

func (h *HostSession) customID(action string, arg ...string) string {
	return strings.Join(append([]string{strings.TrimSuffix(hostGameIDPrefix, "|"), h.ID, action}, arg...), "|")
}

func (h *HostSession) Render(s *discordgo.Session) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	step := slices.Index(hostGamePages, h.Page) + 1
	embed := helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Host Game • %s (%d/%d)", hostGamePageTitle(h.Page), step, len(hostGamePages)),
		Description: h.describe(),
		FooterText:  "Ranked Bedwars • this panel expires after 20 minutes",
	})
	if h.Notice != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: "Notice", Value: h.Notice})
	}

	switch h.Page {
	case pageMode:
		return embed, []discordgo.MessageComponent{h.modeMenu(), h.buttons(false, h.ModeIdx >= 0)}
	case pageOptions:
		return embed, []discordgo.MessageComponent{h.optionButtons(), h.teamCountMenu(), h.buttons(len(h.Modes) > 1, true)}
	case pageTeams:
		return embed, []discordgo.MessageComponent{h.teamMenu(), h.playerMenu(), h.voiceMenu(), h.buttons(true, true)}
	default:
		return embed, []discordgo.MessageComponent{h.reviewButtons()}
	}
}

func hostGamePageTitle(page string) string {
	switch page {
	case pageMode:
		return "Game Mode"
	case pageOptions:
		return "Options"
	case pageTeams:
		return "Teams & Voice"
	}
	return "Review & Start"
}

func (h *HostSession) describe() string {
	mode := h.Modes[h.ModeIdx].Mode
	switch h.Page {
	case pageMode:
		return "Pick the mode this game is played in. Its queue decides where the game's channels are created."
	case pageOptions:
		return fmt.Sprintf("**Mode:** `%s`\n\nChoose how many teams play, whether the match counts toward ELO and season stats, and whether the hosting bot runs it. Without the bot, staff finish the game with `/host finish`.", mode.Name)
	case pageTeams:
		return fmt.Sprintf("Pick a team, then choose its players and, optionally, an existing voice channel for it. Every player must be registered.\n\n%s", h.teamLines())
	}
	return fmt.Sprintf("**Mode:** `%s`\n**Ranks:** %s · **Stats:** %s · **Bot:** %s\n\n%s", mode.Name, HostOnOff(h.Ranked), HostOnOff(h.Stats), HostOnOff(h.UseBot), h.teamLines())
}

func HostOnOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func (h *HostSession) teamLines() string {
	var lines []string
	for t := 0; t < h.TeamCount; t++ {
		line := fmt.Sprintf("**%s** (%d)", TeamName(t), len(h.Teams[t]))
		if len(h.Teams[t]) > 0 {
			line += ": <@" + strings.Join(h.Teams[t], "> <@") + ">"
		}
		if h.Voice[t] != "" {
			line += " · <#" + h.Voice[t] + ">"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (h *HostSession) modeMenu() discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, len(h.Modes))
	for i, m := range h.Modes {
		options[i] = discordgo.SelectMenuOption{Label: m.Mode.Name, Value: strconv.Itoa(i), Default: i == h.ModeIdx}
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.SelectMenu{CustomID: h.customID("mode"), Placeholder: "Game mode", Options: options}}}
}

func (h *HostSession) optionButtons() discordgo.MessageComponent {
	style := func(on bool) discordgo.ButtonStyle {
		if on {
			return discordgo.SuccessButton
		}
		return discordgo.SecondaryButton
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.Button{Label: "Ranks: " + HostOnOff(h.Ranked), Style: style(h.Ranked), CustomID: h.customID("ranked")},
		&discordgo.Button{Label: "Stats: " + HostOnOff(h.Stats), Style: style(h.Stats), CustomID: h.customID("stats")},
		&discordgo.Button{Label: "Bot: " + HostOnOff(h.UseBot), Style: style(h.UseBot), CustomID: h.customID("bot")},
	}}
}

func (h *HostSession) teamCountMenu() discordgo.MessageComponent {
	var options []discordgo.SelectMenuOption
	for n := 2; n <= hostGameMaxTeams; n++ {
		options = append(options, discordgo.SelectMenuOption{Label: fmt.Sprintf("%d teams", n), Value: strconv.Itoa(n), Default: n == h.TeamCount})
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.SelectMenu{CustomID: h.customID("count"), Placeholder: "Number of teams", Options: options}}}
}

func (h *HostSession) teamMenu() discordgo.MessageComponent {
	var options []discordgo.SelectMenuOption
	for t := 0; t < h.TeamCount; t++ {
		options = append(options, discordgo.SelectMenuOption{Label: TeamName(t), Value: strconv.Itoa(t), Default: t == h.Active, Description: fmt.Sprintf("%d player(s)", len(h.Teams[t]))})
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.SelectMenu{CustomID: h.customID("team"), Placeholder: "Team to edit", Options: options}}}
}

func (h *HostSession) playerMenu() discordgo.MessageComponent {
	zero := 0
	menu := &discordgo.SelectMenu{
		MenuType: discordgo.UserSelectMenu, CustomID: h.customID("players"), Placeholder: "Players of " + TeamName(h.Active),
		MinValues: &zero, MaxValues: 25,
	}
	for _, id := range h.Teams[h.Active] {
		menu.DefaultValues = append(menu.DefaultValues, discordgo.SelectMenuDefaultValue{ID: id, Type: discordgo.SelectMenuDefaultValueUser})
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}}
}

func (h *HostSession) voiceMenu() discordgo.MessageComponent {
	zero := 0
	menu := &discordgo.SelectMenu{
		MenuType: discordgo.ChannelSelectMenu, CustomID: h.customID("voice"), Placeholder: "Voice channel for " + TeamName(h.Active) + " (optional: one is created)",
		MinValues: &zero, MaxValues: 1, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildVoice},
	}
	if id := h.Voice[h.Active]; id != "" {
		menu.DefaultValues = []discordgo.SelectMenuDefaultValue{{ID: id, Type: discordgo.SelectMenuDefaultValueChannel}}
	}
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}}
}

func (h *HostSession) buttons(back, next bool) discordgo.MessageComponent {
	row := &discordgo.ActionsRow{}
	row.Components = append(row.Components, &discordgo.Button{Label: "Back", Style: discordgo.SecondaryButton, Disabled: !back, CustomID: h.customID("back")})
	row.Components = append(row.Components, &discordgo.Button{Label: "Next", Style: discordgo.PrimaryButton, Disabled: !next, CustomID: h.customID("next")})
	row.Components = append(row.Components, &discordgo.Button{Label: "Cancel", Style: discordgo.DangerButton, CustomID: h.customID("cancel")})
	return row
}

func (h *HostSession) reviewButtons() discordgo.MessageComponent {
	return &discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.Button{Label: "Back", Style: discordgo.SecondaryButton, CustomID: h.customID("back")},
		&discordgo.Button{Label: "Start game", Style: discordgo.SuccessButton, CustomID: h.customID("start")},
		&discordgo.Button{Label: "Cancel", Style: discordgo.DangerButton, CustomID: h.customID("cancel")},
	}}
}

func HandleHostGameComponent(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, strings.TrimSuffix(hostGameIDPrefix, "|")+"|") {
		return false
	}
	parts := strings.Split(customID, "|")
	if len(parts) < 3 {
		return true
	}

	hostSessions.Lock()
	h := hostSessions.byID[parts[1]]
	hostSessions.Unlock()
	user := helpers.InteractionUserID(i)
	switch {
	case h == nil || time.Since(h.Created) > hostGameSessionTT:
		respondHostGame(s, i, HostGameErrorEmbed(errors.New("this panel has expired, run `/host create` again")), nil)
		return true
	case user != h.UserID:
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "Only the staff member who opened this panel can use it.", Flags: discordgo.MessageFlagsEphemeral}})
		return true
	}

	values := i.MessageComponentData().Values
	h.Notice = ""
	switch parts[2] {
	case "mode":
		if len(values) == 1 {
			h.ModeIdx, _ = strconv.Atoi(values[0])
			h.Ranked, h.Stats = h.Modes[h.ModeIdx].Mode.IsEloGainEnabled, h.Modes[h.ModeIdx].Mode.IsStatsTrackingEnabled
		}
	case "ranked":
		h.Ranked = !h.Ranked
	case "stats":
		h.Stats = !h.Stats
	case "bot":
		h.UseBot = !h.UseBot
	case "count":
		if len(values) == 1 {
			h.TeamCount, _ = strconv.Atoi(values[0])
			h.Active = min(h.Active, h.TeamCount-1)
		}
	case "team":
		if len(values) == 1 {
			h.Active, _ = strconv.Atoi(values[0])
		}
	case "players":
		h.setPlayers(s, values)
	case "voice":
		h.Voice[h.Active] = ""
		if len(values) == 1 {
			h.Voice[h.Active] = values[0]
		}
	case "back":
		h.Page = hostGamePages[max(0, slices.Index(hostGamePages, h.Page)-1)]
		if h.Page == pageMode && len(h.Modes) == 1 {
			h.Page = pageOptions
		}
	case "next":
		h.Page = hostGamePages[min(len(hostGamePages)-1, slices.Index(hostGamePages, h.Page)+1)]
		if h.Page == pageReview {
			h.Notice = h.problem()
		}
	case "cancel":
		hostSessions.Lock()
		delete(hostSessions.byID, h.ID)
		hostSessions.Unlock()
		respondHostGame(s, i, helpers.HostGameDoneEmbed("Setup cancelled."), nil)
		return true
	case "start":
		h.start(s, i)
		return true
	}
	embed, components := h.Render(s)
	respondHostGame(s, i, embed, components)
	return true
}

func respondHostGame(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) {
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: components},
	})
}

// setPlayers sets the active team's players. Bots are dropped and a player can only be on one team.
func (h *HostSession) setPlayers(s *discordgo.Session, ids []string) {
	var kept []string
	for _, id := range ids {
		if u, err := s.User(id); err == nil && u.Bot {
			h.Notice = "Bots can't play."
			continue
		}
		kept = append(kept, id)
	}
	for t := range h.Teams {
		if t == h.Active {
			continue
		}
		h.Teams[t] = slices.DeleteFunc(h.Teams[t], func(id string) bool { return slices.Contains(kept, id) })
	}
	h.Teams[h.Active] = kept
}

// problem is why the game can't start yet, or "".
func (h *HostSession) problem() string {
	for t := 0; t < h.TeamCount; t++ {
		if len(h.Teams[t]) == 0 {
			return fmt.Sprintf("%s has no players yet.", TeamName(t))
		}
	}
	return ""
}

func (h *HostSession) start(s *discordgo.Session, i *discordgo.InteractionCreate) {
	fail := func(notice string) {
		h.Notice = notice
		embed, components := h.Render(s)
		respondHostGame(s, i, embed, components)
	}
	if p := h.problem(); p != "" {
		fail(p)
		return
	}

	chosen := h.Modes[h.ModeIdx]
	teams := make([][]queue.QueueMember, h.TeamCount)
	for t := 0; t < h.TeamCount; t++ {
		for _, id := range h.Teams[t] {
			name := id
			if m, err := s.GuildMember(h.GuildID, id); err == nil {
				name = m.Nick
				if name == "" && m.User != nil {
					name = m.User.Username
				}
			}
			teams[t] = append(teams[t], queue.QueueMember{UserID: id, Username: helpers.MinecraftName(h.GuildID, id, name)})
		}
	}

	game, err := Games.StartCustom(CustomSpec{
		GuildID: h.GuildID, HostedBy: h.UserID, QueueChannelID: chosen.ChannelID, CategoryID: chosen.CategoryID, Mode: chosen.Mode,
		Ranked: h.Ranked, Stats: h.Stats, UseBot: h.UseBot, Teams: teams, Voice: slices.Clone(h.Voice[:h.TeamCount]),
	})
	if err != nil {
		var busy *BusyError
		var unregistered *NotRegisteredError
		switch {
		case errors.As(err, &busy):
			fail(fmt.Sprintf("<@%s> is already in a game.", busy.UserID))
		case errors.As(err, &unregistered):
			fail("These players aren't registered: <@" + strings.Join(unregistered.UserIDs, "> <@") + ">.")
		case errors.Is(err, ErrNoSeason):
			fail("This server has no active season.")
		default:
			fail("The game could not be started: " + err.Error())
		}
		return
	}

	hostSessions.Lock()
	delete(hostSessions.byID, h.ID)
	hostSessions.Unlock()
	respondHostGame(s, i, helpers.HostGameDoneEmbed(fmt.Sprintf("Game **#%d** is being set up in `%s`. Its channels will appear in a moment.", game.Number, chosen.Mode.Name)), nil)
}

// ============================================================================
// Team formations
