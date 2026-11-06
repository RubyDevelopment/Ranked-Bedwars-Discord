package panels

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/guildlink"
)

// ============================================================================
// Giveaways (dashboard: Extra → Giveaways, and the /giveaway command)
// ============================================================================

// Giveaways are rows of the database (guild_giveaways, with their entries in guild_giveaway_entries). The dashboard and /giveaway make them; the
// bot posts each one that is due, takes entries through its button, draws the winners when it ends (once, recorded in the database before
// anything is announced) and announces them. They work for any server (roles, time in the server, boosters) and, for a ranked Bedwars server,
// with the ladder's own rules (ELO, rank, games, wins, recent play, record). The post points at the commands that help meet each rule.

type giveawayDoc struct {
	ID          string `json:"id"`
	Prize       string `json:"prize"`
	Winners     int    `json:"winners"`
	RequiredElo int    `json:"requiredElo"`
	ChannelID   string `json:"channel_id"`
	EndsAt      string `json:"ends_at"`
	Description string `json:"description"`

	// Rules for ranked Bedwars (see giveaway_rules.go); every one is optional.
	MaxElo             int            `json:"maxElo"`
	MinRank            string         `json:"minRank"`
	MaxRank            string         `json:"maxRank"`
	MinGames           int            `json:"minGames"`
	MinWins            int            `json:"minWins"`
	ActiveDays         int            `json:"activeDays"`
	RequireRegistered  bool           `json:"requireRegistered"`
	RequiredRoleIDs    []string       `json:"requiredRoleIds"`
	BlockedRoleIDs     []string       `json:"blockedRoleIds"`
	NoActivePunishment bool           `json:"noActivePunishment"`
	ExcludePastWinners bool           `json:"excludePastWinners"`
	BonusRanks         map[string]int `json:"bonusRanks"`
	BonusRoles         map[string]int `json:"bonusRoles"`
	PrizeRoleID        string         `json:"prizeRoleId"`

	// Rules for any server.
	MinJoinDays  int  `json:"minJoinDays"`
	BoostersOnly bool `json:"boostersOnly"`

	host string                    // the Discord id of who made it
	ref  func(name string) string // writes a command for a message (clickable when known)
}

// GiveawayRules are the rules a giveaway is made with (what /giveaway create sets); the dashboard saves the same fields.
type GiveawayRules struct {
	RequiredElo        int            `json:"requiredElo,omitempty"`
	MaxElo             int            `json:"maxElo,omitempty"`
	MinRank            string         `json:"minRank,omitempty"`
	MaxRank            string         `json:"maxRank,omitempty"`
	MinGames           int            `json:"minGames,omitempty"`
	MinWins            int            `json:"minWins,omitempty"`
	ActiveDays         int            `json:"activeDays,omitempty"`
	RequireRegistered  bool           `json:"requireRegistered,omitempty"`
	RequiredRoleIDs    []string       `json:"requiredRoleIds,omitempty"`
	BlockedRoleIDs     []string       `json:"blockedRoleIds,omitempty"`
	NoActivePunishment bool           `json:"noActivePunishment,omitempty"`
	ExcludePastWinners bool           `json:"excludePastWinners,omitempty"`
	BonusRanks         map[string]int `json:"bonusRanks,omitempty"`
	BonusRoles         map[string]int `json:"bonusRoles,omitempty"`
	PrizeRoleID        string         `json:"prizeRoleId,omitempty"`
	MinJoinDays        int            `json:"minJoinDays,omitempty"`
	BoostersOnly       bool           `json:"boostersOnly,omitempty"`
}

var (
	giveawaySession atomic.Pointer[discordgo.Session]
	giveawayOnce    sync.Once
	giveawayPosted  sync.Map // guild/giveaway -> true, while a post is going out
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

func sweepGiveaways(s *discordgo.Session) {
	if s == nil {
		return
	}
	for _, g := range s.State.Guilds {
		if guildlink.IsSecondary(g.ID) {
			continue // a linked secondary server has no data of its own: its main guild is swept
		}
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
	rows, err := database.Giveaways(guildID, "scheduled,running")
	if err != nil {
		return
	}
	now := time.Now()
	for _, row := range rows {
		g := docOf(row)
		switch {
		case row.Status != "running" && now.Before(row.EndsAt):
			postGiveaway(s, guildID, row, g)
		case row.Status == "running" && row.MessageID != "" && !now.Before(row.EndsAt):
			drawGiveaway(s, guildID, row, g)
		case !now.Before(row.EndsAt):
			// It ended before the bot ever posted it: there is nothing to draw.
			_ = database.FinishGiveaway(guildID, row.ID, nil)
		}
	}
}

// docOf is a giveaway row as the rules and the post read it: the row's own fields and the rules saved with it.
func docOf(row database.Giveaway) giveawayDoc {
	var g giveawayDoc
	_ = json.Unmarshal(row.Rules, &g)
	g.ID = strconv.FormatUint(uint64(row.ID), 10)
	g.Prize, g.Description, g.Winners, g.ChannelID = row.Prize, row.Description, row.Winners, row.ChannelID
	g.EndsAt = row.EndsAt.UTC().Format(time.RFC3339)
	g.host = row.CreatedBy
	return g
}

// ---- the post ----

type giveawayState int

const (
	giveawayRunning giveawayState = iota
	giveawayOver
	giveawayCancelled
)

type giveawayView struct {
	state   giveawayState
	winners []string
	entries int
}

var snowflake = regexp.MustCompile(`^\d{15,25}$`)

func field(name, value string, inline bool) map[string]any {
	return map[string]any{"name": name, "value": value, "inline": inline}
}

func mentions(ids []string) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = "<@" + id + ">"
	}
	return strings.Join(out, ", ")
}

// giveawayEmbed is the post: what is given, who can enter and how, with the commands that help.
func giveawayEmbed(g giveawayDoc, ends time.Time, v giveawayView) map[string]any {
	prize := strings.TrimSpace(g.Prize)
	var fields []map[string]any
	embed := map[string]any{}
	var d strings.Builder
	if text := strings.TrimSpace(g.Description); text != "" {
		d.WriteString(text + "\n\n")
	}
	winnersText := strconv.Itoa(max(g.Winners, 1))
	switch v.state {
	case giveawayRunning:
		embed["title"], embed["color"] = "🎉 "+prize, 0x5865f2
		d.WriteString("Press **Enter** below to join.")
		if g.needsStats() || g.needsRegistration() {
			d.WriteString(" You are checked when you press it, and told what is missing.")
		}
		fields = append(fields, field("🏆 Winners", winnersText, true), field("⏰ Ends", fmt.Sprintf("<t:%d:R>", ends.Unix()), true))
		if snowflake.MatchString(g.host) {
			fields = append(fields, field("👤 Hosted by", "<@"+g.host+">", true))
		}
		embed["timestamp"] = ends.UTC().Format(time.RFC3339)
	case giveawayOver:
		embed["title"], embed["color"] = "🎉 "+prize+" · ended", 0x393a41
		if len(v.winners) == 0 {
			fields = append(fields, field("🏆 Won by", "Nobody entered.", false))
		} else {
			fields = append(fields, field("🏆 Won by", mentions(v.winners), false))
		}
		fields = append(fields, field("🎟️ Entries", strconv.Itoa(v.entries), true), field("Winners", winnersText, true))
		if snowflake.MatchString(g.host) {
			fields = append(fields, field("👤 Hosted by", "<@"+g.host+">", true))
		}
	case giveawayCancelled:
		embed["title"], embed["color"] = "🚫 "+prize+" · cancelled", 0xed4245
		d.WriteString("This giveaway was cancelled by staff.")
	}
	if v.state == giveawayRunning {
		if reqs := g.requirements(); len(reqs) > 0 {
			fields = append(fields, field("📋 Requirements", "• "+strings.Join(reqs, "\n• "), false))
		} else {
			fields = append(fields, field("📋 Requirements", "Open to everyone in the server.", false))
		}
		if bonus := g.bonusText(); bonus != "" {
			fields = append(fields, field("✨ Bonus entries", bonus, false))
		}
		if g.PrizeRoleID != "" {
			fields = append(fields, field("🎁 Winners also get", "<@&"+g.PrizeRoleID+">", false))
		}
		if g.asksStats() || len(g.BonusRanks) > 0 {
			fields = append(fields, field("🔎 Check where you stand",
				fmt.Sprintf("%s for your ELO and rank · %s for the standings · %s if you are not registered yet", g.cmd("stats"), g.cmd("leaderboard"), g.cmd("register")), false))
		}
	}
	embed["description"] = strings.TrimSpace(d.String())
	embed["fields"] = fields
	state := map[giveawayState]string{giveawayRunning: "Giveaway", giveawayOver: "Giveaway ended", giveawayCancelled: "Giveaway cancelled"}[v.state]
	embed["footer"] = map[string]any{"text": fmt.Sprintf("%s #%s", state, g.ID)}
	return embed
}

func giveawayButton(id string, disabled bool) []map[string]any {
	return []map[string]any{{"type": 1, "components": []map[string]any{{
		"type": 2, "style": 3, "label": "Enter", "emoji": map[string]any{"name": "🎉"}, "custom_id": giveawayIDPrefix + id, "disabled": disabled,
	}}}}
}

func postGiveaway(s *discordgo.Session, guildID string, row database.Giveaway, g giveawayDoc) {
	key := guildID + "/" + g.ID
	if _, busy := giveawayPosted.LoadOrStore(key, true); busy {
		return // posted a moment ago; saving that it is running has not gone through yet
	}
	g.ref = commandRefs(s, guildID)
	id, err := helpers.SendPayload(s, g.ChannelID, map[string]any{
		"embeds": []map[string]any{giveawayEmbed(g, row.EndsAt, giveawayView{})}, "components": giveawayButton(g.ID, false),
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	if err != nil {
		giveawayPosted.Delete(key)
		panelLogger.Warn(fmt.Sprintf("posting giveaway %s in %s: %v", g.ID, guildID, err))
		return
	}
	if err := database.UpdateGiveaway(guildID, row.ID, map[string]any{"status": "running", "message_id": id}); err != nil {
		panelLogger.Warn(fmt.Sprintf("saving giveaway %s in %s: %v", g.ID, guildID, err))
	}
}

// ---- who is drawn ----

// memberOf finds a member of the guild, from the cache or from Discord; nil when they have left.
func memberOf(s *discordgo.Session, guildID, userID string) *discordgo.Member {
	if m, err := s.State.Member(guildID, userID); err == nil && m != nil {
		return m
	}
	if m, err := s.GuildMember(guildID, userID); err == nil {
		return m
	}
	return nil
}

// stillQualifies is the check at the draw: the person is still in the server and still meets the giveaway's rules as they stand now (a role
// lost, a punishment, ELO that dropped since they entered). A check that cannot be made does not disqualify anyone.
func stillQualifies(s *discordgo.Session, guildID string, row database.Giveaway, g giveawayDoc) func(string) bool {
	return func(userID string) bool {
		m := memberOf(s, guildID, userID)
		if m == nil {
			return false
		}
		who, order, err := gatherEntrant(guildID, userID, m, g, row.ID)
		if err != nil {
			return true
		}
		return g.check(who, order) == ""
	}
}

// chooseWinners draws n people from the entries (skipping skip), each with the chance their entries give, passing over any who no longer qualify.
func chooseWinners(s *discordgo.Session, guildID string, row database.Giveaway, g giveawayDoc, entries []database.GiveawayEntry, n int, skip map[string]bool) []string {
	var ids []string
	weights := make(map[string]int, len(entries))
	for _, e := range entries {
		if skip[e.UserID] {
			continue
		}
		ids = append(ids, e.UserID)
		weights[e.UserID] = e.Weight
	}
	return pickWinnersChecked(ids, weights, n, rand.New(rand.NewSource(time.Now().UnixNano())), stillQualifies(s, guildID, row, g))
}

// drawGiveaway ends a giveaway: it draws the winners, records the result (once, in the database), updates the post and announces them. It
// reports the winners and whether this call was the one that ended it.
func drawGiveaway(s *discordgo.Session, guildID string, row database.Giveaway, g giveawayDoc) ([]string, bool) {
	entries, err := database.GiveawayEntries(guildID, row.ID)
	if err != nil {
		panelLogger.Warn(fmt.Sprintf("reading the entries of giveaway %s in %s: %v", g.ID, guildID, err))
		return nil, false
	}
	g.ref = commandRefs(s, guildID)
	winners := chooseWinners(s, guildID, row, g, entries, max(g.Winners, 1), nil)

	// Record the result first, in the database, where it works once: a draw that is announced twice would be worse than one that is not.
	if err := database.FinishGiveaway(guildID, row.ID, winners); err != nil {
		if apiErr, ok := err.(*database.APIStatusError); !ok || apiErr.Status >= 500 {
			panelLogger.Warn(fmt.Sprintf("ending giveaway %s in %s: %v", g.ID, guildID, err))
		}
		return nil, false
	}

	channelID := row.ChannelID
	_ = helpers.EditPayload(s, channelID, row.MessageID, map[string]any{
		"embeds":     []map[string]any{giveawayEmbed(g, time.Time{}, giveawayView{state: giveawayOver, winners: winners, entries: len(entries)})},
		"components": giveawayButton(g.ID, true), "allowed_mentions": map[string]any{"parse": []string{}},
	})
	text := fmt.Sprintf("Nobody entered the giveaway for **%s**.", g.Prize)
	if len(winners) > 0 {
		text = fmt.Sprintf("🎉 Congratulations %s! You won **%s**.", mentions(winners), g.Prize)
		text += grantPrizeRole(s, guildID, g, winners)
	}
	announce(s, channelID, row.MessageID, guildID, text, winners)
	return winners, true
}

// grantPrizeRole hands the winners the giveaway's role, and says so for the announcement.
func grantPrizeRole(s *discordgo.Session, guildID string, g giveawayDoc, winners []string) string {
	if g.PrizeRoleID == "" {
		return ""
	}
	given := 0
	for _, w := range winners {
		if err := s.GuildMemberRoleAdd(guildID, w, g.PrizeRoleID); err != nil {
			panelLogger.Warn(fmt.Sprintf("giving the prize role of giveaway %s in %s to %s: %v", g.ID, guildID, w, err))
			continue
		}
		given++
	}
	if given == 0 {
		return ""
	}
	return fmt.Sprintf("\nThe winners now have <@&%s>.", g.PrizeRoleID)
}

func announce(s *discordgo.Session, channelID, messageID, guildID, text string, pings []string) {
	_, _ = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:         text,
		Reference:       &discordgo.MessageReference{MessageID: messageID, ChannelID: channelID, GuildID: guildID},
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: pings},
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
	rowID, err := strconv.ParseUint(strings.TrimPrefix(customID, giveawayIDPrefix), 10, 64)
	if err != nil {
		say("This giveaway is no longer available.")
		return true
	}
	userID := i.Member.User.ID

	row, err := database.GiveawayByID(i.GuildID, uint(rowID))
	if err != nil {
		say("Something went wrong. Please try again in a moment.")
		return true
	}
	if row == nil || row.Status == "cancelled" {
		say("This giveaway is no longer available.")
		return true
	}
	if row.Status == "ended" || !time.Now().Before(row.EndsAt) {
		say("This giveaway has ended.")
		return true
	}
	g := docOf(*row)
	g.ref = commandRefs(s, i.GuildID)

	who, rankOrder, err := gatherEntrant(i.GuildID, userID, i.Member, g, row.ID)
	if err != nil {
		say("Something went wrong. Please try again in a moment.")
		return true
	}
	if reason := g.check(who, rankOrder); reason != "" {
		say(reason)
		return true
	}

	weight := g.weight(who)
	entered, count, err := database.EnterGiveaway(i.GuildID, row.ID, userID, weight)
	switch {
	case err != nil:
		if apiErr, ok := err.(*database.APIStatusError); ok && apiErr.Status == 400 {
			say("This giveaway has ended.")
		} else {
			say("Something went wrong. Please try again in a moment.")
		}
	case !entered:
		say("You have already entered this giveaway.")
	default:
		text := fmt.Sprintf("You are in! 🎉 (%d %s so far)", count, map[bool]string{true: "entry", false: "entries"}[count == 1])
		if weight > 1 {
			text += fmt.Sprintf("\nYou hold **%d entries**, a better chance than most.", weight)
		}
		text += fmt.Sprintf("\nIt ends <t:%d:R>. Winners are checked against the rules again when it is drawn.", row.EndsAt.Unix())
		say(text)
	}
	return true
}

// ============================================================================
// For staff (the /giveaway command)
// ============================================================================

// CreateInput is a giveaway a staff member makes with /giveaway create.
type CreateInput struct {
	Prize, Description, ChannelID, HostID string
	Winners                               int
	Duration                              time.Duration
	Rules                                 GiveawayRules
}

// ErrGiveaway is a refusal staff can read: what was wrong with the request.
var ErrGiveaway = errors.New("giveaway")

func refuse(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrGiveaway, fmt.Sprintf(format, a...))
}

// Message is the refusal's text without its prefix.
func Message(err error) string { return strings.TrimPrefix(err.Error(), ErrGiveaway.Error()+": ") }

// CreateGiveaway saves a giveaway and posts it at once.
func CreateGiveaway(s *discordgo.Session, guildID string, in CreateInput) (*database.Giveaway, error) {
	in.Prize = strings.TrimSpace(in.Prize)
	switch {
	case in.Prize == "" || len(in.Prize) > 200:
		return nil, refuse("the prize must be 1 to 200 characters")
	case in.Winners < 1 || in.Winners > 50:
		return nil, refuse("winners must be between 1 and 50")
	case in.Duration < time.Minute || in.Duration > 90*24*time.Hour:
		return nil, refuse("the duration must be between 1 minute and 90 days")
	}
	if names, err := rankNames(guildID); err == nil {
		for _, rank := range []*string{&in.Rules.MinRank, &in.Rules.MaxRank} {
			if *rank == "" {
				continue
			}
			i := rankIndex(names, *rank)
			if i < 0 {
				return nil, refuse("there is no rank called %q here (see the ranks command)", *rank)
			}
			*rank = names[i]
		}
	}
	raw, _ := json.Marshal(in.Rules)
	row, err := database.CreateGiveaway(guildID, database.NewGiveaway{
		Prize: in.Prize, Description: in.Description, Winners: in.Winners, ChannelID: in.ChannelID,
		EndsAt: time.Now().Add(in.Duration), Rules: raw, CreatedBy: in.HostID,
	})
	if err != nil {
		return nil, err
	}
	postGiveaway(s, guildID, *row, docOf(*row))
	return row, nil
}

func rankNames(guildID string) ([]string, error) {
	ranks, err := helpers.GuildMetaRanks(guildID)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, r := range helpers.SortedRanks(ranks) {
		names = append(names, r.RankName)
	}
	return names, nil
}

// EndGiveaway draws a running giveaway now.
func EndGiveaway(s *discordgo.Session, guildID string, id uint) ([]string, error) {
	row, err := database.GiveawayByID(guildID, id)
	if err != nil {
		return nil, err
	}
	switch {
	case row == nil:
		return nil, refuse("there is no giveaway #%d", id)
	case row.Status == "ended":
		return nil, refuse("giveaway #%d has already ended (use reroll to draw again)", id)
	case row.Status == "cancelled":
		return nil, refuse("giveaway #%d was cancelled", id)
	case row.MessageID == "":
		return nil, refuse("giveaway #%d has not been posted yet: it is posted within half a minute of being made", id)
	}
	winners, ended := drawGiveaway(s, guildID, *row, docOf(*row))
	if !ended {
		return nil, refuse("giveaway #%d could not be ended just now; try again in a moment", id)
	}
	return winners, nil
}

// RerollGiveaway draws count more winners of an ended giveaway, from entries that did not win, and announces them.
func RerollGiveaway(s *discordgo.Session, guildID string, id uint, count int) ([]string, error) {
	row, err := database.GiveawayByID(guildID, id)
	if err != nil {
		return nil, err
	}
	switch {
	case row == nil:
		return nil, refuse("there is no giveaway #%d", id)
	case row.Status != "ended":
		return nil, refuse("giveaway #%d has not ended yet (use end to draw it now)", id)
	}
	entries, err := database.GiveawayEntries(guildID, id)
	if err != nil {
		return nil, err
	}
	won := map[string]bool{}
	for _, e := range entries {
		if e.IsWinner {
			won[e.UserID] = true
		}
	}
	g := docOf(*row)
	g.ref = commandRefs(s, guildID)
	winners := chooseWinners(s, guildID, *row, g, entries, min(max(count, 1), 50), won)
	if len(winners) == 0 {
		return nil, refuse("no other entry of giveaway #%d still qualifies", id)
	}
	if _, err := database.SetGiveawayWinners(guildID, id, winners, false); err != nil {
		return nil, err
	}
	text := fmt.Sprintf("🎉 A new draw for **%s**: congratulations %s!", g.Prize, mentions(winners)) + grantPrizeRole(s, guildID, g, winners)
	announce(s, row.ChannelID, row.MessageID, guildID, text, winners)
	return winners, nil
}

// CancelGiveaway cancels a giveaway that has not ended and marks its post.
func CancelGiveaway(s *discordgo.Session, guildID string, id uint) error {
	row, err := database.GiveawayByID(guildID, id)
	if err != nil {
		return err
	}
	switch {
	case row == nil:
		return refuse("there is no giveaway #%d", id)
	case row.Status == "ended":
		return refuse("giveaway #%d has already ended", id)
	case row.Status == "cancelled":
		return refuse("giveaway #%d is already cancelled", id)
	}
	if err := database.UpdateGiveaway(guildID, id, map[string]any{"status": "cancelled"}); err != nil {
		return err
	}
	if row.MessageID != "" {
		g := docOf(*row)
		g.ref = commandRefs(s, guildID)
		_ = helpers.EditPayload(s, row.ChannelID, row.MessageID, map[string]any{
			"embeds":     []map[string]any{giveawayEmbed(g, time.Time{}, giveawayView{state: giveawayCancelled})},
			"components": giveawayButton(g.ID, true), "allowed_mentions": map[string]any{"parse": []string{}},
		})
	}
	return nil
}

// Summary is a giveaway as staff see it in a list.
type Summary struct {
	ID      uint
	Prize   string
	Status  string
	EndsAt  time.Time
	Winners int
	Entries int64
	Channel string
}

// ListGiveaways lists the guild's most recent giveaways with how many entries each holds.
func ListGiveaways(guildID string) ([]Summary, error) {
	rows, err := database.RecentGiveaways(guildID)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, len(rows))
	var wg sync.WaitGroup
	for i, r := range rows {
		out[i] = Summary{ID: r.ID, Prize: r.Prize, Status: r.Status, EndsAt: r.EndsAt, Winners: r.Winners, Channel: r.ChannelID}
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i].Entries = database.GiveawayEntryCount(guildID, r.ID)
		}()
	}
	wg.Wait()
	return out, nil
}
