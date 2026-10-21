package helpers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	baseutility "ranked-bedwars/src/utility"
)

// ============================================================================
// Giveaways (dashboard: Community → Giveaways)
// ============================================================================

// The dashboard keeps the giveaways in the guild config's `panels.giveaways`. The bot posts each one that has a
// prize, a channel and an end time, takes entries through its button, draws the winners when it ends and announces
// them. What the bot records (message, entries, winners) lives apart in `panels.giveaway_state`, so saving the
// dashboard's list never overwrites an entry.

type giveawayDoc struct {
	ID          string `json:"id"`
	Prize       string `json:"prize"`
	Winners     int    `json:"winners"`
	RequiredElo int    `json:"requiredElo"`
	ChannelID   string `json:"channel_id"`
	EndsAt      string `json:"ends_at"`
}

type giveawayState struct {
	ChannelID string   `json:"channel_id"`
	MessageID string   `json:"message_id"`
	Ended     bool     `json:"ended"`
	Entries   []string `json:"entries"`
	Winners   []string `json:"winners"`
}

var (
	giveawaySession atomic.Pointer[discordgo.Session]
	giveawayOnce    sync.Once
)

const (
	giveawaySweepEvery = 30 * time.Second
	giveawayIDPrefix   = "gw:"
)

// StartGiveaways starts the sweep that posts giveaways and draws their winners; the ready event calls it.
func StartGiveaways(s *discordgo.Session) {
	giveawaySession.Store(s)
	giveawayOnce.Do(func() {
		go func() {
			for range time.Tick(giveawaySweepEvery) {
				sweepGiveaways(giveawaySession.Load())
			}
		}()
	})
}

func readGiveaways(cfg *models.GuildConfigsModel) ([]giveawayDoc, map[string]*giveawayState) {
	var sections struct {
		Giveaways []giveawayDoc             `json:"giveaways"`
		State     map[string]*giveawayState `json:"giveaway_state"`
	}
	if !cfg.Panels.IsEmpty() {
		_ = cfg.Panels.Unmarshal(&sections)
	}
	if sections.State == nil {
		sections.State = map[string]*giveawayState{}
	}
	return sections.Giveaways, sections.State
}

// updateGiveawayState applies fn to the giveaway's state inside the guild's panels field; fn returns false to
// leave the field untouched.
// panelsWriteMu serializes the bot's own writes to the panels field, so a burst of entries does not trip over itself.
var panelsWriteMu sync.Mutex

func updateGiveawayState(guildID, id string, fn func(st *giveawayState) bool) error {
	panelsWriteMu.Lock()
	defer panelsWriteMu.Unlock()
	return database.Must().GuildConfig.UpdateField(sdk.SystemActor, guildID, "panels", func(cfg *models.GuildConfigsModel) (any, error) {
		sections := map[string]json.RawMessage{}
		if !cfg.Panels.IsEmpty() {
			if err := cfg.Panels.Unmarshal(&sections); err != nil {
				return nil, err
			}
		}
		state := map[string]*giveawayState{}
		_ = json.Unmarshal(sections["giveaway_state"], &state)
		st := state[id]
		if st == nil {
			st = &giveawayState{}
			state[id] = st
		}
		if !fn(st) {
			return sections, errNoChange
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return nil, err
		}
		sections["giveaway_state"] = raw
		return sections, nil
	})
}

var errNoChange = fmt.Errorf("no change")

func sweepGiveaways(s *discordgo.Session) {
	if s == nil {
		return
	}
	for _, g := range s.State.Guilds {
		func() {
			defer func() {
				if r := recover(); r != nil {
					panelLogger.Error(fmt.Sprintf("giveaway sweep panic in %s: %v", g.ID, r))
				}
			}()
			sweepGuildGiveaways(s, g.ID)
		}()
	}
}

func sweepGuildGiveaways(s *discordgo.Session, guildID string) {
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		return
	}
	list, state := readGiveaways(cfg)
	now := time.Now()
	for _, g := range list {
		ends, perr := time.Parse(time.RFC3339, g.EndsAt)
		if strings.TrimSpace(g.Prize) == "" || g.ChannelID == "" || perr != nil {
			continue
		}
		st := state[g.ID]
		switch {
		case st != nil && st.Ended:
		case (st == nil || st.MessageID == "") && now.Before(ends):
			postGiveaway(s, guildID, g, ends)
		case st != nil && st.MessageID != "" && !now.Before(ends):
			drawGiveaway(s, guildID, g, st)
		case st == nil || st.MessageID == "":
			// It ended before the bot ever posted it: there is nothing to draw.
			_ = updateGiveawayState(guildID, g.ID, func(st *giveawayState) bool { st.Ended = true; return true })
		}
	}
}

func giveawayEmbed(g giveawayDoc, ends time.Time, ended bool, winners []string) map[string]any {
	var d strings.Builder
	fmt.Fprintf(&d, "**Prize:** %s\n**Winners:** %d\n", g.Prize, max(g.Winners, 1))
	if g.RequiredElo > 0 {
		fmt.Fprintf(&d, "**Required ELO:** %d\n", g.RequiredElo)
	}
	title, color := "🎉 Giveaway", 0x55FFFF
	if ended {
		title, color = "🎉 Giveaway ended", 0x393a41
		if len(winners) == 0 {
			d.WriteString("**Winners:** nobody entered.\n")
		} else {
			mentions := make([]string, len(winners))
			for idx, w := range winners {
				mentions[idx] = "<@" + w + ">"
			}
			fmt.Fprintf(&d, "**Won by:** %s\n", strings.Join(mentions, ", "))
		}
	} else {
		fmt.Fprintf(&d, "**Ends:** <t:%d:R>\nPress the button to enter.", ends.Unix())
	}
	return map[string]any{"title": title, "description": d.String(), "color": color}
}

func giveawayButton(id string, disabled bool) []map[string]any {
	return []map[string]any{{"type": 1, "components": []map[string]any{{
		"type": 2, "style": 3, "label": "Enter", "emoji": map[string]any{"name": "🎉"}, "custom_id": giveawayIDPrefix + id, "disabled": disabled,
	}}}}
}

func postGiveaway(s *discordgo.Session, guildID string, g giveawayDoc, ends time.Time) {
	id, err := SendPayload(s, g.ChannelID, map[string]any{
		"embeds": []map[string]any{giveawayEmbed(g, ends, false, nil)}, "components": giveawayButton(g.ID, false),
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	if err != nil {
		panelLogger.Warn(fmt.Sprintf("posting giveaway %s in %s: %v", g.ID, guildID, err))
		return
	}
	if err := updateGiveawayState(guildID, g.ID, func(st *giveawayState) bool { st.ChannelID, st.MessageID = g.ChannelID, id; return true }); err != nil {
		panelLogger.Warn(fmt.Sprintf("saving giveaway %s in %s: %v", g.ID, guildID, err))
	}
}

func drawGiveaway(s *discordgo.Session, guildID string, g giveawayDoc, st *giveawayState) {
	entries := slices.Clone(st.Entries)
	rand.Shuffle(len(entries), func(a, b int) { entries[a], entries[b] = entries[b], entries[a] })
	winners := entries[:min(max(g.Winners, 1), len(entries))]

	// Record the result first: a draw that is announced twice would be worse than one that is not announced.
	already := false
	if err := updateGiveawayState(guildID, g.ID, func(cur *giveawayState) bool {
		if cur.Ended {
			already = true
			return false
		}
		cur.Ended, cur.Winners = true, winners
		return true
	}); err != nil && err != errNoChange {
		panelLogger.Warn(fmt.Sprintf("ending giveaway %s in %s: %v", g.ID, guildID, err))
		return
	}
	if already {
		return
	}

	channelID := st.ChannelID
	_ = EditPayload(s, channelID, st.MessageID, map[string]any{
		"embeds": []map[string]any{giveawayEmbed(g, time.Time{}, true, winners)}, "components": giveawayButton(g.ID, true),
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	text := fmt.Sprintf("Nobody entered the giveaway for **%s**.", g.Prize)
	if len(winners) > 0 {
		mentions := make([]string, len(winners))
		for idx, w := range winners {
			mentions[idx] = "<@" + w + ">"
		}
		text = fmt.Sprintf("🎉 Congratulations %s! You won **%s**.", strings.Join(mentions, ", "), g.Prize)
	}
	_, _ = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:         text,
		Reference:       &discordgo.MessageReference{MessageID: st.MessageID, ChannelID: channelID, GuildID: guildID},
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: winners},
	})
}

// ============================================================================
// Entering
// ============================================================================

func HandleGiveawayEntry(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, giveawayIDPrefix) {
		return false
	}
	if i.Member == nil || i.Member.User == nil {
		return true
	}
	say := func(text string) { _ = respondText(s, i, text) }
	id, userID := strings.TrimPrefix(customID, giveawayIDPrefix), i.Member.User.ID

	cfg, err := database.Must().GuildConfig.Get(i.GuildID)
	if err != nil {
		say("Something went wrong. Please try again in a moment.")
		return true
	}
	list, state := readGiveaways(cfg)
	var g *giveawayDoc
	for idx := range list {
		if list[idx].ID == id {
			g = &list[idx]
		}
	}
	if g == nil {
		say("This giveaway is no longer available.")
		return true
	}
	if st := state[id]; st != nil && st.Ended {
		say("This giveaway has ended.")
		return true
	}
	if ends, err := time.Parse(time.RFC3339, g.EndsAt); err == nil && !time.Now().Before(ends) {
		say("This giveaway has ended.")
		return true
	}

	if g.RequiredElo > 0 {
		view, err := database.Must().Player.Get(i.GuildID, userID)
		if err != nil {
			say(fmt.Sprintf("You must be registered and have at least **%d ELO** to enter. Register with `/player_register`.", g.RequiredElo))
			return true
		}
		_, seasonID, _ := ActiveSeasonKey(i.GuildID)
		stats, err := database.Must().Stats.GetOrCreate(i.GuildID, sdk.UintToString(view.Player.ID), seasonID)
		if err != nil {
			say("Something went wrong. Please try again in a moment.")
			return true
		}
		if stats.Elo < g.RequiredElo {
			say(fmt.Sprintf("You need at least **%d ELO** to enter this giveaway. You have **%d**.", g.RequiredElo, stats.Elo))
			return true
		}
	}

	entered, count := false, 0
	err = updateGiveawayState(i.GuildID, id, func(st *giveawayState) bool {
		count = len(st.Entries)
		if slices.Contains(st.Entries, userID) {
			return false
		}
		st.Entries = append(st.Entries, userID)
		entered, count = true, len(st.Entries)
		return true
	})
	switch {
	case err != nil && err != errNoChange:
		say("Something went wrong. Please try again in a moment.")
	case !entered:
		say("You have already entered this giveaway.")
	default:
		say(fmt.Sprintf("You are in! 🎉 (%d %s so far)", count, map[bool]string{true: "entry", false: "entries"}[count == 1]))
	}
	return true
}

// ============================================================================
// Custom panels (dashboard: Builders → Panels)
// ============================================================================

// A panel is a saved message plus buttons and select menus, each bound to an action. The dashboard stores them in
// the guild config's `panels.component_panels`. This file posts them (and keeps them up to date) in their channel and
// answers presses, whose custom ids are `cp:<panel id>:<item id>`.

var (
	panelLogger  = baseutility.CreateModuleLogger("PANELS", "discord")
	panelSession atomic.Pointer[discordgo.Session]
	panelOnce    sync.Once
	panelPosted  sync.Map // guildID/panelID -> hash of what was last posted
)

const (
	panelSyncEvery = time.Minute
	panelIDPrefix  = "cp:"
)

type panelAction struct {
	Type             string `json:"type"`
	TicketPanelID    string `json:"ticket_panel_id"`
	TicketCategoryID string `json:"ticket_category_id"`
	RoleID           string `json:"role_id"`
	URL              string `json:"url"`
	Text             string `json:"text"`
	Command          string `json:"command"`
}

type panelOption struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Emoji       string      `json:"emoji"`
	Description string      `json:"description"`
	Action      panelAction `json:"action"`
}

type panelItem struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Label    string `json:"label"`
	Emoji    string `json:"emoji"`
	Style    int    `json:"style"`
	Disabled bool   `json:"disabled"`
	Action   panelAction
	// select menus
	Placeholder string        `json:"placeholder"`
	Min         int           `json:"min"`
	Max         int           `json:"max"`
	Options     []panelOption `json:"options"`
}

type panelRow struct {
	ID    string      `json:"id"`
	Items []panelItem `json:"items"`
}

type panelDoc struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Enabled   *bool           `json:"enabled"`
	Message   json.RawMessage `json:"message"`
	Rows      []panelRow      `json:"rows"`
	ChannelID string          `json:"channel_id"`
	MessageID string          `json:"message_id"`
}

func (p *panelDoc) enabled() bool { return p.Enabled == nil || *p.Enabled }

// panelSections is what the bot reads of the guild's `panels` field.
type panelSections struct {
	ComponentPanels []panelDoc        `json:"component_panels"`
	Messages        []json.RawMessage `json:"messages"`
}

func loadPanelSections(cfg *models.GuildConfigsModel) panelSections {
	var ps panelSections
	if !cfg.Panels.IsEmpty() {
		_ = cfg.Panels.Unmarshal(&ps)
	}
	return ps
}

// StartPanelSync posts the dashboard's panels now and keeps them current; the ready event calls it.
func StartPanelSync(s *discordgo.Session) {
	panelSession.Store(s)
	panelOnce.Do(func() {
		go func() {
			syncAllPanels(panelSession.Load())
			for range time.Tick(panelSyncEvery) {
				syncAllPanels(panelSession.Load())
			}
		}()
	})
}

// ============================================================================
// Posting
// ============================================================================

var emojiPattern = regexp.MustCompile(`^<(a?):(\w{2,32}):(\d{17,20})>$`)

func emojiObject(e string) map[string]any {
	if m := emojiPattern.FindStringSubmatch(e); m != nil {
		return map[string]any{"id": m[3], "name": m[2], "animated": m[1] == "a"}
	}
	return map[string]any{"name": e}
}

func rowsPayload(p *panelDoc, f func(string) string) []map[string]any {
	rows := make([]map[string]any, 0, len(p.Rows))
	for _, row := range p.Rows {
		items := make([]map[string]any, 0, len(row.Items))
		for _, it := range row.Items {
			id := panelIDPrefix + p.ID + ":" + it.ID
			if it.Kind == "select" {
				opts := make([]map[string]any, 0, len(it.Options))
				for _, o := range it.Options {
					opt := map[string]any{"label": f(o.Label), "value": o.ID}
					if o.Description != "" {
						opt["description"] = f(o.Description)
					}
					if o.Emoji != "" {
						opt["emoji"] = emojiObject(o.Emoji)
					}
					opts = append(opts, opt)
				}
				items = append(items, map[string]any{"type": 3, "custom_id": id, "placeholder": f(it.Placeholder), "min_values": it.Min, "max_values": it.Max, "options": opts})
				continue
			}
			isLink := it.Action.Type == "url"
			b := map[string]any{"type": 2, "style": it.Style, "disabled": it.Disabled || !p.enabled()}
			if isLink {
				b["style"], b["url"] = 5, it.Action.URL
			} else {
				b["custom_id"] = id
			}
			if it.Label != "" {
				b["label"] = f(it.Label)
			}
			if it.Emoji != "" {
				b["emoji"] = emojiObject(it.Emoji)
			}
			items = append(items, b)
		}
		rows = append(rows, map[string]any{"type": 1, "components": items})
	}
	return rows
}

// payload is the whole message the bot posts: the saved message, then the component rows.
func (p *panelDoc) payload(f func(string) string) (map[string]any, error) {
	doc, err := ParseMessageDoc(p.Message)
	if err != nil {
		return nil, err
	}
	doc.Buttons = nil
	body := doc.Payload(f)
	comps, _ := body["components"].([]map[string]any)
	body["components"] = append(comps, rowsPayload(p, f)...)
	return body, nil
}

func syncAllPanels(s *discordgo.Session) {
	if s == nil {
		return
	}
	for _, g := range s.State.Guilds {
		func() {
			defer func() {
				if r := recover(); r != nil {
					panelLogger.Error(fmt.Sprintf("panel sync panic in %s: %v", g.ID, r))
				}
			}()
			syncGuildPanels(s, g.ID)
		}()
	}
}

func syncGuildPanels(s *discordgo.Session, guildID string) {
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		return
	}
	vars := map[string]string(GuildVars(s, guildID, nil))
	for _, p := range loadPanelSections(cfg).ComponentPanels {
		if p.ChannelID == "" {
			continue
		}
		body, err := p.payload(VarFiller(vars))
		if err != nil {
			panelLogger.Warn(fmt.Sprintf("panel %s in %s: %v", p.ID, guildID, err))
			continue
		}
		raw, _ := json.Marshal(body)
		sum := sha256.Sum256(append([]byte(p.ChannelID+"|"+p.MessageID+"|"), raw...))
		hash := hex.EncodeToString(sum[:])
		key := guildID + "/" + p.ID
		if last, ok := panelPosted.Load(key); ok && last == hash {
			continue
		}

		messageID := p.MessageID
		if messageID != "" {
			if err := EditPayload(s, p.ChannelID, messageID, body); err != nil {
				if !isGone(err) {
					panelLogger.Warn(fmt.Sprintf("editing panel %s in %s: %v", p.ID, guildID, err))
					continue
				}
				messageID = "" // the message was deleted: post it again
			}
		}
		if messageID == "" {
			if messageID, err = SendPayload(s, p.ChannelID, body); err != nil {
				panelLogger.Warn(fmt.Sprintf("posting panel %s in %s: %v", p.ID, guildID, err))
				continue
			}
			if err := rememberPanelMessage(guildID, p.ID, p.ChannelID, messageID); err != nil {
				panelLogger.Warn(fmt.Sprintf("saving the message id of panel %s in %s: %v", p.ID, guildID, err))
				continue
			}
			sum = sha256.Sum256(append([]byte(p.ChannelID+"|"+messageID+"|"), raw...))
			hash = hex.EncodeToString(sum[:])
		}
		panelPosted.Store(key, hash)
	}
}

// isGone reports whether Discord says the message (or its channel) no longer exists.
func isGone(err error) bool {
	if rest, ok := err.(*discordgo.RESTError); ok && rest.Response != nil {
		return rest.Response.StatusCode == 404
	}
	return false
}

// rememberPanelMessage writes the id of the message the bot posted onto the panel, unless the dashboard has meanwhile
// moved the panel to another channel.
func rememberPanelMessage(guildID, panelID, channelID, messageID string) error {
	panelsWriteMu.Lock()
	defer panelsWriteMu.Unlock()
	return database.Must().GuildConfig.UpdateField(sdk.SystemActor, guildID, "panels", func(cfg *models.GuildConfigsModel) (any, error) {
		sections := map[string]json.RawMessage{}
		if !cfg.Panels.IsEmpty() {
			if err := cfg.Panels.Unmarshal(&sections); err != nil {
				return nil, err
			}
		}
		var list []map[string]any
		if err := json.Unmarshal(sections["component_panels"], &list); err != nil {
			return nil, err
		}
		for _, p := range list {
			if p["id"] == panelID && p["channel_id"] == channelID {
				p["message_id"] = messageID
			}
		}
		raw, err := json.Marshal(list)
		if err != nil {
			return nil, err
		}
		sections["component_panels"] = raw
		return sections, nil
	})
}

// ============================================================================
// Presses
// ============================================================================

func HandlePanelPress(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, panelIDPrefix) {
		return false
	}
	parts := strings.Split(customID, ":")
	if len(parts) != 3 || i.Member == nil || i.Member.User == nil {
		return true
	}
	say := func(text string) { _ = respondText(s, i, text) }

	cfg, err := database.Must().GuildConfig.Get(i.GuildID)
	if err != nil {
		say("Something went wrong. Please try again in a moment.")
		return true
	}
	var panel *panelDoc
	sections := loadPanelSections(cfg)
	for idx := range sections.ComponentPanels {
		if sections.ComponentPanels[idx].ID == parts[1] {
			panel = &sections.ComponentPanels[idx]
		}
	}
	if panel == nil {
		say("This panel is no longer available.")
		return true
	}
	if !panel.enabled() {
		say("This panel is turned off right now.")
		return true
	}

	var actions []panelAction
	for _, row := range panel.Rows {
		for _, it := range row.Items {
			if it.ID != parts[2] {
				continue
			}
			if it.Kind != "select" {
				actions = append(actions, it.Action)
				continue
			}
			for _, picked := range i.MessageComponentData().Values {
				for _, o := range it.Options {
					if o.ID == picked {
						actions = append(actions, o.Action)
					}
				}
			}
		}
	}
	if len(actions) == 0 {
		say("This button is no longer available.")
		return true
	}
	runPanelActions(s, i, actions)
	return true
}

// runPanelActions carries out what was pressed. Role and reply actions are answered together; an action that
// answers on its own (opening a ticket, running a command) takes over the response, so only the first such one runs.
func runPanelActions(s *discordgo.Session, i *discordgo.InteractionCreate, actions []panelAction) {
	var lines []string
	owning := false
	roles := slices.Clone(i.Member.Roles)
	for _, a := range actions {
		switch a.Type {
		case "role_add", "role_remove", "role_toggle":
			line, now := changeRole(s, i.GuildID, i.Member.User.ID, roles, a)
			roles = now
			lines = append(lines, line)
		case "reply":
			vars := map[string]string(GuildVars(s, i.GuildID, i.Member.User))
			lines = append(lines, FillVars(a.Text, vars))
		case "ticket":
			if !owning {
				owning = true
				HandleTicketComponent(s, i, "tkt:open:"+a.TicketPanelID+":"+a.TicketCategoryID)
			}
		case "command":
			if !owning {
				owning = true
				runPanelCommand(s, i, a.Command)
			}
		}
	}
	if owning {
		return
	}
	if len(lines) == 0 {
		_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
		return
	}
	_ = respondText(s, i, strings.Join(lines, "\n"))
}

// changeRole applies a give/take/toggle and reports it, with the member's roles afterwards.
func changeRole(s *discordgo.Session, guildID, userID string, roles []string, a panelAction) (string, []string) {
	has := slices.Contains(roles, a.RoleID)
	add := a.Type == "role_add" || (a.Type == "role_toggle" && !has)
	switch {
	case add && has:
		return fmt.Sprintf("You already have <@&%s>.", a.RoleID), roles
	case !add && !has:
		return fmt.Sprintf("You do not have <@&%s>.", a.RoleID), roles
	case add:
		if err := s.GuildMemberRoleAdd(guildID, userID, a.RoleID); err != nil {
			return fmt.Sprintf("I could not give you <@&%s>. Ask an admin to check my permissions and role order.", a.RoleID), roles
		}
		return fmt.Sprintf("You now have <@&%s>.", a.RoleID), append(roles, a.RoleID)
	default:
		if err := s.GuildMemberRoleRemove(guildID, userID, a.RoleID); err != nil {
			return fmt.Sprintf("I could not take <@&%s> from you. Ask an admin to check my permissions and role order.", a.RoleID), roles
		}
		return fmt.Sprintf("<@&%s> was taken from you.", a.RoleID), slices.DeleteFunc(slices.Clone(roles), func(r string) bool { return r == a.RoleID })
	}
}

// runPanelCommand runs a command for the member as if they had typed it without options, answering only them.
func runPanelCommand(s *discordgo.Session, i *discordgo.InteractionCreate, name string) {
	cmd := FindCommand(name)
	if cmd == nil {
		cmd = FindCustomCommand(i.GuildID, name)
	}
	if cmd == nil {
		_ = respondText(s, i, "That command does not exist.")
		return
	}
	ci := *i.Interaction
	ci.Type = discordgo.InteractionApplicationCommand
	ci.Data = discordgo.ApplicationCommandInteractionData{ID: i.ID, Name: cmd.Name, CommandType: discordgo.ChatApplicationCommand}
	RunSlashCommand(s, &discordgo.InteractionCreate{Interaction: &ci}, cmd, true)
}

// respondText answers the press privately with plain text and no pings.
func respondText(s *discordgo.Session, i *discordgo.InteractionCreate, text string) error {
	return InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: text, Flags: discordgo.MessageFlagsEphemeral, AllowedMentions: &discordgo.MessageAllowedMentions{}},
	})
}

// ============================================================================
// Reaction roles (dashboard: Community → Reaction Roles)
// ============================================================================

// A binding ties an emoji on one message to a role: reacting gives the role, taking the reaction back removes it.
// The dashboard keeps the bindings in the guild config's `reactions` object, keyed by binding id.

type reactionRole struct {
	ID        string `json:"id"`
	Emoji     string `json:"emoji"`
	MessageID string `json:"message_id"`
	ChannelID string `json:"channel_id"`
	RoleID    string `json:"role_id"`
	Enabled   bool   `json:"enabled"`
}

type reactionCache struct {
	at    time.Time
	rules []reactionRole
}

var (
	reactionMu    sync.Mutex
	reactionRules = map[string]*reactionCache{}
)

const reactionCacheFor = 15 * time.Second

// HandleReactionAdd gives the role bound to the reaction that was added.
func HandleReactionAdd(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	reactionChange(s, r.MessageReaction, r.Member, true)
}

// HandleReactionRemove takes the role bound to the reaction that was removed.
func HandleReactionRemove(s *discordgo.Session, r *discordgo.MessageReactionRemove) {
	reactionChange(s, r.MessageReaction, nil, false)
}

func rulesFor(guildID string) []reactionRole {
	reactionMu.Lock()
	defer reactionMu.Unlock()
	if c := reactionRules[guildID]; c != nil && time.Since(c.at) < reactionCacheFor {
		return c.rules
	}
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		if c := reactionRules[guildID]; c != nil {
			return c.rules
		}
		return nil
	}
	byID := map[string]reactionRole{}
	if !cfg.Reactions.IsEmpty() {
		_ = cfg.Reactions.Unmarshal(&byID)
	}
	rules := make([]reactionRole, 0, len(byID))
	for _, r := range byID {
		rules = append(rules, r)
	}
	reactionRules[guildID] = &reactionCache{at: time.Now(), rules: rules}
	return rules
}

// sameEmoji compares a binding's emoji (a unicode emoji, or `name:id` for a custom one) with a reaction's.
func sameEmoji(bound string, e discordgo.Emoji) bool {
	bound = strings.Trim(strings.TrimSpace(bound), "<>")
	bound = strings.TrimPrefix(bound, "a:")
	if e.ID != "" {
		return bound == e.Name+":"+e.ID || bound == ":"+e.Name+":"+e.ID
	}
	strip := func(s string) string { return strings.ReplaceAll(s, "️", "") }
	return strip(bound) == strip(e.Name)
}

func reactionChange(s *discordgo.Session, r *discordgo.MessageReaction, member *discordgo.Member, add bool) {
	if r == nil || r.GuildID == "" || (s.State.User != nil && r.UserID == s.State.User.ID) {
		return
	}
	if member != nil && member.User != nil && member.User.Bot {
		return
	}
	for _, rule := range rulesFor(r.GuildID) {
		if !rule.Enabled || rule.RoleID == "" || rule.MessageID != r.MessageID || !sameEmoji(rule.Emoji, r.Emoji) {
			continue
		}
		if rule.ChannelID != "" && rule.ChannelID != r.ChannelID {
			continue
		}
		var err error
		if add {
			err = s.GuildMemberRoleAdd(r.GuildID, r.UserID, rule.RoleID)
		} else {
			err = s.GuildMemberRoleRemove(r.GuildID, r.UserID, rule.RoleID)
		}
		if err != nil {
			panelLogger.Warn(fmt.Sprintf("reaction role %s for %s in %s: %v", rule.ID, r.UserID, r.GuildID, err))
		}
	}
}
