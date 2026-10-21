package features

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
	baseutility "ranked-bedwars/src/utility"
)

// ============================================================================
// Running a command
// ============================================================================

// Steps run in order; a failing or stopping step answers with its message and ends the command. Then the first
// reply whose condition holds (or the default reply) is sent with {{variables}} filled in.

type runner struct {
	s       *discordgo.Session
	i       *discordgo.InteractionCreate
	e       *entry
	name    string
	invoker *discordgo.User
	vars    map[string]string
	users   map[string]*discordgo.User // user and player options by option name

	// ephemeral is what the reply was deferred as: the called subcommand's default reply decides it before any step runs.
	ephemeral bool
}

func runCustomCommand(s *discordgo.Session, i *discordgo.InteractionCreate, name string) error {
	e := entries(i.GuildID)[name]
	if e == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("This command no longer exists."))
	}
	data := i.ApplicationCommandData()
	opts, sub := data.Options, ""
	if len(e.b.SubcommandGroups) > 0 {
		if len(opts) == 0 || len(opts[0].Options) == 0 {
			return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("Pick a subcommand to run."))
		}
		sub, opts = opts[0].Name+" "+opts[0].Options[0].Name, opts[0].Options[0].Options
	} else if len(e.b.Subcommands) > 0 {
		if len(opts) == 0 {
			return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("Pick a subcommand to run."))
		}
		sub, opts = opts[0].Name, opts[0].Options
	}
	b := e.b.bodyFor(sub)
	if b == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("That subcommand no longer exists."))
	}

	invoker := i.User
	if i.Member != nil && i.Member.User != nil {
		invoker = i.Member.User
	}
	r := &runner{s: s, i: i, e: e, name: name, invoker: invoker, users: map[string]*discordgo.User{}, ephemeral: b.Reply.Ephemeral}
	r.vars = r.baseVars(sub)
	r.readOptions(b, opts, data)

	for _, st := range b.Steps {
		if st.When != nil && !r.holds(*st.When) {
			continue
		}
		if msg, stopped := r.exec(st); stopped {
			return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed(r.fill(msg)))
		}
	}

	chosen := b.Reply
	for _, rule := range b.ReplyRules {
		if r.holds(rule.When) {
			chosen = rule.Reply
			break
		}
	}
	return r.send(chosen)
}

// ============================================================================
// Variables
// ============================================================================

func (r *runner) fill(text string) string { return helpers.FillVars(text, r.vars) }

func (r *runner) baseVars(sub string) map[string]string {
	v := map[string]string(helpers.GuildVars(r.s, r.i.GuildID, r.invoker))
	now := time.Now().UTC()
	v["time.date"], v["time.time"] = now.Format("2006-01-02"), now.Format("15:04")
	v["time.unix"], v["time.relative"] = itoa(int(now.Unix())), fmt.Sprintf("<t:%d:R>", now.Unix())
	v["channel.id"], v["channel.mention"] = r.i.ChannelID, "<#"+r.i.ChannelID+">"
	if ch, err := r.s.State.Channel(r.i.ChannelID); err == nil && ch != nil {
		v["channel.name"] = ch.Name
	}
	v["command.name"], v["command.subcommand"] = r.name, sub
	v["command.prefix"] = helpers.LoadGuildContext(r.i.GuildID).Settings.Prefix
	if r.invoker != nil {
		v["user.id"] = r.invoker.ID
	}
	return v
}

func (r *runner) readOptions(b *body, given []*discordgo.ApplicationCommandInteractionDataOption, data discordgo.ApplicationCommandInteractionData) {
	byName := map[string]*discordgo.ApplicationCommandInteractionDataOption{}
	for _, o := range given {
		byName[o.Name] = o
	}
	for _, def := range b.Options {
		o := byName[def.Name]
		key := "opt." + def.Name
		if o == nil {
			r.vars[key] = ""
			continue
		}
		switch o.Type {
		case discordgo.ApplicationCommandOptionUser, discordgo.ApplicationCommandOptionMentionable:
			u := o.UserValue(r.s)
			r.users[def.Name] = u
			r.vars[key], r.vars[key+".id"], r.vars[key+".mention"] = u.Username, u.ID, "<@"+u.ID+">"
			if def.Type == "player" {
				if view, err := database.Must().Player.Get(r.i.GuildID, u.ID); err == nil && view.Config != nil {
					r.vars[key] = view.Config.Username
				}
			}
		case discordgo.ApplicationCommandOptionRole:
			id := fmt.Sprint(o.Value)
			r.vars[key], r.vars[key+".id"], r.vars[key+".mention"] = "<@&"+id+">", id, "<@&"+id+">"
		case discordgo.ApplicationCommandOptionChannel:
			id := fmt.Sprint(o.Value)
			r.vars[key], r.vars[key+".id"], r.vars[key+".mention"] = "<#"+id+">", id, "<#"+id+">"
		case discordgo.ApplicationCommandOptionAttachment:
			id := fmt.Sprint(o.Value)
			if data.Resolved != nil && data.Resolved.Attachments[id] != nil {
				r.vars[key] = data.Resolved.Attachments[id].URL
			}
		case discordgo.ApplicationCommandOptionNumber:
			r.vars[key] = ftoa(o.FloatValue())
		case discordgo.ApplicationCommandOptionInteger:
			r.vars[key] = itoa(int(o.IntValue()))
		default:
			r.vars[key] = fmt.Sprint(o.Value)
		}
	}
}

// who resolves a step's target: the invoker, or a user/player option.
func (r *runner) who(target string) *discordgo.User {
	if target == "" || target == "@self" {
		return r.invoker
	}
	return r.users[strings.TrimPrefix(target, "opt:")]
}

// ============================================================================
// Conditions
// ============================================================================

func (r *runner) holds(c condition) bool {
	left, right := strings.TrimSpace(r.fill(c.Left)), strings.TrimSpace(r.fill(c.Right))
	ln, lerr := strconv.ParseFloat(left, 64)
	rn, rerr := strconv.ParseFloat(right, 64)
	numeric := lerr == nil && rerr == nil
	switch c.Op {
	case "eq":
		if numeric {
			return ln == rn
		}
		return strings.EqualFold(left, right)
	case "ne":
		if numeric {
			return ln != rn
		}
		return !strings.EqualFold(left, right)
	case "gt":
		return numeric && ln > rn
	case "gte":
		return numeric && ln >= rn
	case "lt":
		return numeric && ln < rn
	case "lte":
		return numeric && ln <= rn
	case "contains":
		return strings.Contains(strings.ToLower(left), strings.ToLower(right))
	case "not_empty":
		return left != ""
	case "empty":
		return left == ""
	}
	return false
}

// ============================================================================
// Steps
// ============================================================================

// exec runs one step. It reports true with the message to answer when the step stops the command.
func (r *runner) exec(st step) (string, bool) {
	stop := func(def string) (string, bool) {
		if st.Message != "" {
			return st.Message, true
		}
		return def, true
	}
	guildID := r.i.GuildID

	switch st.Type {
	case "require_player", "load_stats":
		u := r.who(st.Target)
		if u == nil {
			return stop("I could not tell who that is.")
		}
		view, err := database.Must().Player.Get(guildID, u.ID)
		if err != nil {
			if sdk.IsNotFound(err) {
				return stop(u.Mention() + " is not registered.")
			}
			return stop("Something went wrong. Please try again later.")
		}
		r.vars["player.name"], r.vars["player.id"], r.vars["player.mention"] = view.Config.Username, u.ID, u.Mention()
		r.vars["player.avatar"], r.vars["player.head"], r.vars["player.body"] = helpers.AvatarOf(u), helpers.HeadURL(view.Config.Username), helpers.BodyURL(view.Config.Username)
		r.vars["player.joined"] = view.Config.CreatedAt.Format("2006-01-02")
		if st.Type == "load_stats" {
			seasonKey, seasonID, err := helpers.ActiveSeasonKey(guildID)
			if err != nil || seasonKey == "" {
				return stop("There is no active season.")
			}
			playerID := sdk.UintToString(view.Player.ID)
			stats, err := database.Must().Stats.GetOrCreate(guildID, playerID, seasonID)
			if err != nil {
				return stop("Something went wrong. Please try again later.")
			}
			position := 0
			if rank, err := database.Must().Stats.Rank(guildID, playerID, seasonID); err == nil {
				position = rank.Position
			}
			for k, v := range helpers.StatsVars(u, view.Config, stats, position, seasonKey, seasonID) {
				r.vars[k] = v
			}
		}

	case "load_leaderboard":
		seasonKey, seasonID, err := helpers.ActiveSeasonKey(guildID)
		if err != nil || seasonKey == "" {
			return stop("There is no active season.")
		}
		limit := min(max(st.Limit, 1), 10)
		rows, err := database.Must().Stats.Leaderboard(guildID, seasonID, sdk.Page{Limit: limit})
		if err != nil {
			return stop("Something went wrong. Please try again later.")
		}
		vars := helpers.Vars{}
		helpers.LeaderboardRowVars(r.s, guildID, rows.Data, vars)
		for k, v := range vars {
			r.vars[k] = v
		}
		r.vars["lb.title"], r.vars["season.name"], r.vars["season.id"] = seasonKey+" Leaderboard", seasonKey, itoa(int(seasonID))

	case "load_game":
		number := atoi(strings.TrimSpace(r.fill(st.Left)))
		game, ok := helpers.Games.ByNumber(guildID, number)
		if !ok {
			return stop(fmt.Sprintf("Game #%d was not found.", number))
		}
		r.vars["game.number"], r.vars["game.status"], r.vars["game.map"] = itoa(game.Number), string(game.State), game.Map
		r.vars["game.mode"], r.vars["game.bot"] = game.Mode.Name, game.Bot
		r.vars["game.started_at"], r.vars["game.ended_at"] = "", ""
		if !game.StartedAt.IsZero() {
			r.vars["game.started_at"] = fmt.Sprintf("<t:%d:R>", game.StartedAt.Unix())
		}
		if (game.State == helpers.GameCleanup || game.State == helpers.GameVoided) && !game.StateAt.IsZero() {
			r.vars["game.ended_at"] = fmt.Sprintf("<t:%d:R>", game.StateAt.Unix())
		}

	case "load_recent_games":
		u := r.who(st.Target)
		if u == nil {
			return stop("I could not tell who that is.")
		}
		view, err := database.Must().Player.Get(guildID, u.ID)
		if err != nil {
			return stop(u.Mention() + " is not registered.")
		}
		_, seasonID, _ := helpers.ActiveSeasonKey(guildID)
		all, err := database.Must().Player.AllGames(guildID, sdk.UintToString(view.Player.ID))
		if err != nil {
			return stop("Something went wrong. Please try again later.")
		}
		for n := 1; n <= 5; n++ { // rows the player does not have are blank, not left as {{recent.N.*}}
			k := fmt.Sprintf("recent.%d.", n)
			r.vars[k+"number"], r.vars[k+"result"], r.vars[k+"map"] = "", "", ""
		}
		for n, h := range helpers.FilterGameHistoryEntries(all, seasonID, 0, false) {
			if n >= min(max(st.Limit, 1), 5) {
				break
			}
			result := "Loss"
			if h.IsWinner {
				result = "Win"
			}
			k := fmt.Sprintf("recent.%d.", n+1)
			r.vars[k+"number"], r.vars[k+"result"], r.vars[k+"map"] = itoa(h.Game.GameNumber), result, h.Game.MapName
		}

	case "load_commands":
		r.vars["commands.list"], r.vars["commands.count"] = r.commandList(strings.TrimSpace(st.Value))

	case "check":
		if !r.holds(condition{Left: st.Left, Op: st.Op, Right: st.Right}) {
			return stop("That is not allowed right now.")
		}
	case "stop":
		return stop("Stopped.")

	case "set_var":
		r.vars["var."+st.Name] = r.fill(st.Value)
	case "math":
		a, b := atof(strings.TrimSpace(r.fill(st.Left))), atof(strings.TrimSpace(r.fill(st.Right)))
		var out float64
		switch st.MathOp {
		case "+":
			out = a + b
		case "-":
			out = a - b
		case "*":
			out = a * b
		case "/", "%":
			if b == 0 {
				return stop("You cannot divide by zero.")
			}
			out = a / b
			if st.MathOp == "%" {
				out = math.Mod(a, b)
			}
		}
		r.vars["var."+st.Name] = ftoa(math.Round(out*1e6) / 1e6)
	case "random_pick":
		var lines []string
		for _, l := range strings.Split(st.Value, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) > 0 {
			r.vars["var."+st.Name] = r.fill(lines[rand.Intn(len(lines))])
		}
	case "random_number":
		lo, hi := atoi(strings.TrimSpace(r.fill(st.Left))), atoi(strings.TrimSpace(r.fill(st.Right)))
		if hi < lo {
			lo, hi = hi, lo
		}
		r.vars["var."+st.Name] = itoa(lo + rand.Intn(hi-lo+1))

	case "require_role":
		id := r.e.cfg.RequiredRoles[st.Name].ID
		if id == "" || !hasRole(r.i.Member, id) {
			if id == "" {
				return stop("That role requirement is not configured.")
			}
			return stop("You need the <@&" + id + "> role for this.")
		}
	case "check_role":
		u := r.who(st.Target)
		id := r.e.cfg.RequiredRoles[st.Name].ID
		if u == nil || id == "" {
			return stop("I could not check that user's role. Check the user option and configured role.")
		}
		member := r.i.Member
		if member == nil || member.User == nil || member.User.ID != u.ID {
			var err error
			member, err = helpers.FetchGuildMember(r.s, guildID, u.ID)
			if err != nil || member == nil {
				return stop("I could not find that member in this server.")
			}
		}
		if !hasRole(member, id) {
			return stop("That user does not have the <@&" + id + "> role.")
		}
	case "add_role", "remove_role":
		u := r.who(st.Target)
		id := r.e.cfg.RequiredRoles[st.Name].ID
		if u == nil || id == "" {
			return stop("I could not change that role.")
		}
		var err error
		if st.Type == "add_role" {
			err = r.s.GuildMemberRoleAdd(r.i.GuildID, u.ID, id)
		} else {
			err = r.s.GuildMemberRoleRemove(r.i.GuildID, u.ID, id)
		}
		if err != nil {
			return stop("I could not change that role. Check my permissions and role order.")
		}
	case "send_message":
		if id := r.e.cfg.RequiredChannels[st.Name].ID; id != "" {
			if _, err := r.s.ChannelMessageSendComplex(id, &discordgo.MessageSend{Content: r.fill(st.Value), AllowedMentions: &discordgo.MessageAllowedMentions{}}); err != nil {
				logger.Warn(fmt.Sprintf("sending custom command message in %s: %v", id, err))
				return stop("I could not send the message. Check the bot's channel permissions.")
			}
		}
	case "send_payload":
		id := r.e.cfg.RequiredChannels[st.Name].ID
		if id == "" {
			return stop("That channel is not configured.")
		}
		var payload map[string]any
		if st.Reply.MessageID != "" {
			payload = r.savedMessage(st.Reply.MessageID)
		} else {
			payload = r.replyPayload(st.Reply)
		}
		if payload == nil {
			return stop("I could not load the selected rich message.")
		}
		if _, err := helpers.SendPayload(r.s, id, payload); err != nil {
			logger.Warn(fmt.Sprintf("sending custom command rich message in %s: %v", id, err))
			return stop("I could not send the rich message. Check the bot's channel permissions.")
		}
	case "dm_user":
		u := r.who(st.Target)
		if u == nil {
			return stop("I could not find the user to message.")
		}
		dm, err := r.s.UserChannelCreate(u.ID)
		if err == nil {
			_, err = r.s.ChannelMessageSendComplex(dm.ID, &discordgo.MessageSend{Content: r.fill(st.Value), AllowedMentions: &discordgo.MessageAllowedMentions{}})
		}
		if err != nil {
			logger.Warn(fmt.Sprintf("sending custom command DM to %s: %v", u.ID, err))
			return stop("I could not send that DM. The user may have DMs disabled.")
		}
	}
	return "", false
}

func hasRole(m *discordgo.Member, id string) bool {
	if m == nil {
		return false
	}
	for _, r := range m.Roles {
		if r == id {
			return true
		}
	}
	return false
}

// commandList is every enabled command the invoker may use, one line each, and how many. category filters by
// the command's category (or its start, so "general" takes "general/game" too).
func (r *runner) commandList(category string) (string, string) {
	gc := helpers.LoadGuildContext(r.i.GuildID)
	admin := helpers.MemberIsAdministrator(r.s, r.i)
	all := map[string]*types.Command{}
	for _, c := range helpers.CommandRegistry {
		if c != nil && c.Data != nil && !c.Disabled {
			all[c.Data.Name] = c
		}
	}
	for name, c := range customCommandsFor(r.i.GuildID) {
		all[name] = c
	}
	var lines []string
	for name, c := range all {
		if category != "" && !strings.HasPrefix(c.Category, category) {
			continue
		}
		if perm, ok := gc.Settings.Commands[name]; ok && perm.Disabled != nil && *perm.Disabled {
			continue
		}
		if !helpers.CheckSlashAccess(gc.Settings, name, "", r.i.ChannelID, r.i.Member, admin).Allowed {
			continue
		}
		lines = append(lines, fmt.Sprintf("`/%s` %s", name, c.Description))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), itoa(len(lines))
}

// ============================================================================
// Replies
// ============================================================================

func (r *runner) buttonRow(rep reply) []discordgo.MessageComponent {
	var row []discordgo.MessageComponent
	for _, b := range rep.Buttons {
		if url := r.fill(b.URL); url != "" {
			button := discordgo.Button{Style: discordgo.LinkButton, Label: r.fill(b.Label), URL: url, Disabled: b.Disabled}
			if b.Emoji != "" {
				button.Emoji = &discordgo.ComponentEmoji{Name: b.Emoji}
			}
			row = append(row, button)
		}
	}
	if len(row) == 0 {
		return nil
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: row}}
}

func (r *runner) replyPayload(rep reply) map[string]any {
	doc := &helpers.MessageDoc{AllowMentions: false}
	switch rep.Type {
	case "component":
		doc.Mode = "v2"
		doc.V2 = rep.Components
	case "embed":
		doc.Mode = "embeds"
		doc.Content = rep.Content
		doc.Embeds = rep.Embeds
		if len(doc.Embeds) == 0 {
			legacy := helpers.Embed{Title: rep.Title, Description: rep.Text, Color: rep.Color, Thumbnail: rep.Thumbnail}
			legacy.Footer.Text = rep.Footer
			doc.Embeds = []helpers.Embed{legacy}
		}
		doc.Buttons = rep.Buttons
	default:
		doc.Mode = "embeds"
		doc.Content = rep.Content
		if doc.Content == "" {
			doc.Content = rep.Text
		}
		doc.Buttons = rep.Buttons
	}
	return doc.Payload(r.fill)
}

// embedOf is the embed an "embed" reply describes, with its variables filled in.
func (r *runner) embedOf(rep reply) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{Title: r.fill(rep.Title), Description: r.fill(rep.Text)}
	if len(rep.Color) == 7 && rep.Color[0] == '#' {
		if n, err := strconv.ParseInt(rep.Color[1:], 16, 32); err == nil {
			e.Color = int(n)
		}
	}
	if rep.Footer != "" {
		e.Footer = &discordgo.MessageEmbedFooter{Text: r.fill(rep.Footer)}
	}
	if rep.Thumbnail != "" {
		e.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: r.fill(rep.Thumbnail)}
	}
	return e
}

// sendAs answers with a reply whose privacy differs from the one the response was deferred with (a reply rule that is
// private while the default is public, or the other way round): Discord cannot change that afterwards, so the deferred
// message is removed and the reply goes out as a follow-up with the privacy it asked for.
func (r *runner) sendAs(rep reply) error {
	s, i := r.s, r.i
	var flags discordgo.MessageFlags
	if rep.Ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}

	if rep.Type == "message" {
		if payload := r.savedMessage(rep.MessageID); payload != nil {
			bits, _ := payload["flags"].(int)
			payload["flags"] = bits | int(flags)
			_ = s.InteractionResponseDelete(i.Interaction)
			_, err := s.RequestWithBucketID("POST", discordgo.EndpointWebhookToken(i.AppID, i.Token), payload, discordgo.EndpointWebhookToken(i.AppID, ""))
			return err
		}
	}
	if rep.Type == "message" || rep.Type == "embed" || rep.Type == "component" {
		payload := r.replyPayload(rep)
		if flags != 0 {
			bits, _ := payload["flags"].(int)
			payload["flags"] = bits | int(flags)
		}
		_ = s.InteractionResponseDelete(i.Interaction)
		_, err := s.RequestWithBucketID("POST", discordgo.EndpointWebhookToken(i.AppID, i.Token), payload, discordgo.EndpointWebhookToken(i.AppID, ""))
		return err
	}

	params := &discordgo.WebhookParams{Flags: flags, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	switch {
	case rep.Type == "message":
		params.Embeds = []*discordgo.MessageEmbed{helpers.SimpleErrorEmbed("The saved message for this reply was deleted.")}
	case rep.Type == "embed":
		params.Embeds = []*discordgo.MessageEmbed{r.embedOf(rep)}
	default:
		if img := r.bannerIf(rep); img != nil {
			params.Content = r.fill(rep.Text)
			params.Files = []*discordgo.File{{Name: r.name + ".png", ContentType: "image/png", Reader: bytes.NewReader(img)}}
		} else if params.Content = r.fill(rep.Text); strings.TrimSpace(params.Content) == "" {
			params.Content = "Done."
		}
	}
	if row := r.buttonRow(rep); row != nil && params.Files == nil {
		params.Components = row
	}
	_ = s.InteractionResponseDelete(i.Interaction)
	_, err := s.FollowupMessageCreate(i.Interaction, true, params)
	return err
}

// bannerIf is the banner of a "banner" reply, or nil for any other kind of reply.
func (r *runner) bannerIf(rep reply) []byte {
	if rep.Type != "banner" {
		return nil
	}
	return r.banner()
}

func (r *runner) send(rep reply) error {
	s, i := r.s, r.i
	if rep.Ephemeral != r.ephemeral && !helpers.IsPrefixInteraction(i) {
		return r.sendAs(rep)
	}
	switch rep.Type {
	case "message":
		if payload := r.savedMessage(rep.MessageID); payload != nil {
			if helpers.IsPrefixInteraction(i) {
				_, err := helpers.SendPayload(s, i.ChannelID, payload)
				return err
			}
			return helpers.EditOriginalPayload(s, i.Interaction, payload)
		}
		if rep.MessageID != "" {
			return helpers.EditInteractionEmbed(s, i, helpers.SimpleErrorEmbed("The saved message for this reply was deleted."))
		}
		payload := r.replyPayload(rep)
		if helpers.IsPrefixInteraction(i) {
			_, err := helpers.SendPayload(s, i.ChannelID, payload)
			return err
		}
		return helpers.EditOriginalPayload(s, i.Interaction, payload)

	case "embed", "component":
		payload := r.replyPayload(rep)
		if helpers.IsPrefixInteraction(i) {
			_, err := helpers.SendPayload(s, i.ChannelID, payload)
			return err
		}
		return helpers.EditOriginalPayload(s, i.Interaction, payload)

	case "banner":
		if img := r.banner(); img != nil {
			return helpers.EditInteractionFile(s, i, r.fill(rep.Text), r.name+".png", img)
		}
	}
	text := r.fill(rep.Text)
	if strings.TrimSpace(text) == "" {
		text = "Done."
	}
	edit := &discordgo.WebhookEdit{Content: &text, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	if row := r.buttonRow(rep); row != nil {
		edit.Components = &row
	}
	return helpers.EditInteractionResponse(s, i, edit)
}

// banner draws the guild's banner style for this command, or nil when there is none or drawing fails.
func (r *runner) banner() []byte {
	doc := helpers.LoadBanners(r.i.GuildID)
	if doc == nil {
		return nil
	}
	viewer := helpers.BannerViewer(r.invoker.ID, r.i.Member)
	b := doc.Choose("custom:"+r.name, viewer, "")
	if b == nil {
		return nil
	}
	img, err := helpers.RenderBanner(b, helpers.Vars(r.vars))
	if err != nil {
		logger.Warn(fmt.Sprintf("banner of /%s in %s: %v", r.name, r.i.GuildID, err))
		return nil
	}
	return img
}

// savedMessage is the Embed Builder message with the id, as a payload with the command's variables filled in.
func (r *runner) savedMessage(id string) map[string]any {
	cfg, err := database.Must().GuildConfig.Get(r.i.GuildID)
	if err != nil {
		return nil
	}
	var sections struct {
		Messages []helpers.MessageDoc `json:"messages"`
	}
	if cfg.Panels.IsEmpty() || cfg.Panels.Unmarshal(&sections) != nil {
		return nil
	}
	for idx := range sections.Messages {
		if sections.Messages[idx].ID == id {
			return sections.Messages[idx].Payload(helpers.VarFiller(r.vars))
		}
	}
	return nil
}

type option struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Choices     []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"choices"`
	Min *float64 `json:"min"`
	Max *float64 `json:"max"`
}

type condition struct {
	Left  string `json:"left"`
	Op    string `json:"op"`
	Right string `json:"right"`
}

type step struct {
	ID      string     `json:"id"`
	Type    string     `json:"type"`
	Target  string     `json:"target"`
	Limit   int        `json:"limit"`
	Message string     `json:"message"`
	Left    string     `json:"left"`
	Op      string     `json:"op"`
	Right   string     `json:"right"`
	MathOp  string     `json:"mathOp"`
	Name    string     `json:"name"`
	Value   string     `json:"value"`
	When    *condition `json:"when"`
	Reply   reply      `json:"reply"`
}

type replyButton = types.LinkButton

type reply struct {
	Type       string         `json:"type"`
	Text       string         `json:"text"`
	Content    string         `json:"content"`
	MessageID  string         `json:"messageId"`
	Ephemeral  bool           `json:"ephemeral"`
	Title      string         `json:"title"`
	Color      string         `json:"color"`
	Footer     string         `json:"footer"`
	Thumbnail  string         `json:"thumbnail"`
	Buttons    []replyButton  `json:"buttons"`
	Embeds     []types.Embed  `json:"embeds"`
	Components []types.V2Item `json:"components"`
}

type replyRule struct {
	When  condition `json:"when"`
	Reply reply     `json:"reply"`
}

// body is everything a command, or one subcommand of it, does when run.
type body struct {
	Options    []option    `json:"options"`
	Steps      []step      `json:"steps"`
	Reply      reply       `json:"reply"`
	ReplyRules []replyRule `json:"replyRules"`
}

type subcommand struct {
	body
	Name        string `json:"name"`
	Description string `json:"description"`
}

type builder struct {
	body
	Requirements []struct {
		Type     string `json:"type"`
		Key      string `json:"key"`
		Optional bool   `json:"optional"`
	} `json:"requirements"`
	Subcommands      []subcommand      `json:"subcommands"`
	SubcommandGroups []subcommandGroup `json:"subcommandGroups"`
}

type subcommandGroup struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Subcommands []subcommand `json:"subcommands"`
}

// bodyFor is the body a call runs: the named subcommand's, or the command's own.
func (b *builder) bodyFor(sub string) *body {
	if strings.Contains(sub, " ") {
		parts := strings.SplitN(sub, " ", 2)
		for groupIndex := range b.SubcommandGroups {
			group := &b.SubcommandGroups[groupIndex]
			if group.Name != parts[0] {
				continue
			}
			for commandIndex := range group.Subcommands {
				if group.Subcommands[commandIndex].Name == parts[1] {
					return &group.Subcommands[commandIndex].body
				}
			}
			return nil
		}
		return nil
	}
	if len(b.Subcommands) == 0 {
		if len(b.SubcommandGroups) == 0 {
			return &b.body
		}
		return nil
	}
	for idx := range b.Subcommands {
		if b.Subcommands[idx].Name == sub {
			return &b.Subcommands[idx].body
		}
	}
	return nil
}

// defaultReply is the one whose ephemerality Discord is told when the reply is deferred.
func (b *builder) defaultReply() reply {
	if len(b.SubcommandGroups) > 0 && len(b.SubcommandGroups[0].Subcommands) > 0 {
		return b.SubcommandGroups[0].Subcommands[0].Reply
	}
	if len(b.Subcommands) > 0 {
		return b.Subcommands[0].Reply
	}
	return b.Reply
}

func optionType(t string) (discordgo.ApplicationCommandOptionType, bool) {
	switch t {
	case "player", "user":
		return discordgo.ApplicationCommandOptionUser, true
	case "string":
		return discordgo.ApplicationCommandOptionString, true
	case "integer":
		return discordgo.ApplicationCommandOptionInteger, true
	case "number":
		return discordgo.ApplicationCommandOptionNumber, true
	case "boolean":
		return discordgo.ApplicationCommandOptionBoolean, true
	case "role":
		return discordgo.ApplicationCommandOptionRole, true
	case "channel":
		return discordgo.ApplicationCommandOptionChannel, true
	case "mentionable":
		return discordgo.ApplicationCommandOptionMentionable, true
	case "attachment":
		return discordgo.ApplicationCommandOptionAttachment, true
	}
	return 0, false
}

func describe(text, fallback string) string {
	if t := strings.TrimSpace(text); t != "" {
		if r := []rune(t); len(r) > 100 {
			return string(r[:100])
		}
		return t
	}
	return fallback
}

// slashOptions turns builder options into Discord's.
func slashOptions(opts []option) []*discordgo.ApplicationCommandOption {
	out := make([]*discordgo.ApplicationCommandOption, 0, len(opts))
	for _, o := range opts {
		t, ok := optionType(o.Type)
		if !ok || o.Name == "" {
			continue
		}
		d := &discordgo.ApplicationCommandOption{Name: o.Name, Description: describe(o.Description, o.Name), Type: t, Required: o.Required}
		for _, c := range o.Choices {
			ch := &discordgo.ApplicationCommandOptionChoice{Name: c.Name, Value: c.Value}
			switch t {
			case discordgo.ApplicationCommandOptionInteger:
				ch.Value = atoi(c.Value)
			case discordgo.ApplicationCommandOptionNumber:
				ch.Value = atof(c.Value)
			}
			d.Choices = append(d.Choices, ch)
		}
		switch t {
		case discordgo.ApplicationCommandOptionString:
			if o.Min != nil {
				m := int(*o.Min)
				d.MinLength = &m
			}
			if o.Max != nil {
				d.MaxLength = int(*o.Max)
			}
		case discordgo.ApplicationCommandOptionInteger, discordgo.ApplicationCommandOptionNumber:
			d.MinValue = o.Min
			if o.Max != nil {
				d.MaxValue = *o.Max
			}
		}
		out = append(out, d)
	}
	return out
}

// applicationOptions is the command's option list: its own, or one sub-command per subcommand.
func (b *builder) applicationOptions() []*discordgo.ApplicationCommandOption {
	if len(b.SubcommandGroups) > 0 {
		out := make([]*discordgo.ApplicationCommandOption, 0, len(b.SubcommandGroups))
		for _, group := range b.SubcommandGroups {
			groupOption := &discordgo.ApplicationCommandOption{
				Name: group.Name, Description: describe(group.Description, group.Name),
				Type: discordgo.ApplicationCommandOptionSubCommandGroup,
			}
			for _, sc := range group.Subcommands {
				groupOption.Options = append(groupOption.Options, &discordgo.ApplicationCommandOption{
					Name: sc.Name, Description: describe(sc.Description, sc.Name),
					Type: discordgo.ApplicationCommandOptionSubCommand, Options: slashOptions(sc.Options),
				})
			}
			out = append(out, groupOption)
		}
		return out
	}
	if len(b.Subcommands) == 0 {
		return slashOptions(b.Options)
	}
	out := make([]*discordgo.ApplicationCommandOption, 0, len(b.Subcommands))
	for _, sc := range b.Subcommands {
		out = append(out, &discordgo.ApplicationCommandOption{
			Name: sc.Name, Description: describe(sc.Description, sc.Name), Type: discordgo.ApplicationCommandOptionSubCommand, Options: slashOptions(sc.Options),
		})
	}
	return out
}

var logger = baseutility.CreateModuleLogger("CUSTOM_COMMANDS", "discord")

func atoi(s string) int     { n, _ := strconv.Atoi(s); return n }
func atof(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
func itoa(n int) string     { return strconv.Itoa(n) }
func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// cacheFor is how long a guild's commands are reused before the config is read again.
const cacheFor = 15 * time.Second

type entry struct {
	cfg types.CommandConfig
	b   builder
	cmd *types.Command
}

type guildCommands struct {
	at   time.Time
	byID map[string]*entry
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*guildCommands{}
)

// entries returns the guild's custom commands by name. When the config cannot be read the last known set is used.
func entries(guildID string) map[string]*entry {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if c := cache[guildID]; c != nil && time.Since(c.at) < cacheFor {
		return c.byID
	}
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		if c := cache[guildID]; c != nil {
			return c.byID
		}
		return nil
	}
	raw := map[string]types.CommandConfig{}
	if !cfg.Commands.IsEmpty() {
		_ = cfg.Commands.Unmarshal(&raw)
	}
	out := map[string]*entry{}
	for name, c := range raw {
		if len(c.Builder) == 0 {
			continue
		}
		var b builder
		if err := json.Unmarshal(c.Builder, &b); err != nil {
			logger.Warn(fmt.Sprintf("custom command %q in %s: %v", name, guildID, err))
			continue
		}
		if c.Name == "" {
			c.Name = name
		}
		out[name] = &entry{cfg: c, b: b, cmd: build(name, c, &b)}
	}
	cache[guildID] = &guildCommands{at: time.Now(), byID: out}
	return out
}

// findCustomCommand returns the guild's custom command called name, or nil.
func findCustomCommand(guildID, name string) *types.Command {
	if e := entries(guildID)[name]; e != nil && helpers.FindCommand(name) == nil {
		return e.cmd
	}
	return nil
}

// customCommandsFor returns every custom command of the guild by name.
func customCommandsFor(guildID string) map[string]*types.Command {
	out := map[string]*types.Command{}
	for name, e := range entries(guildID) {
		if helpers.FindCommand(name) == nil {
			out[name] = e.cmd
		}
	}
	return out
}

func build(name string, c types.CommandConfig, b *builder) *types.Command {
	cmd := &types.Command{
		Name:        name,
		Description: describe(c.Description, "Custom command"),
		Category:    "custom",
		Ephemeral:   b.defaultReply().Ephemeral,
		Data:        &discordgo.ApplicationCommand{Name: name, Description: describe(c.Description, "Custom command"), Options: b.applicationOptions(), DefaultMemberPermissions: c.DefaultMemberPermissions},
	}
	for _, r := range b.Requirements {
		cmd.Requirements = append(cmd.Requirements, types.Requirement{Type: types.RequirementType(r.Type), Key: r.Key, Optional: r.Optional})
	}
	cmd.Execute = func(s *discordgo.Session, i *discordgo.InteractionCreate) error { return runCustomCommand(s, i, name) }
	helpers.EnsureCommandLogs(cmd)
	return cmd
}

func customCommandPath(data discordgo.ApplicationCommandInteractionData, b *builder) (string, []*discordgo.ApplicationCommandInteractionDataOption) {
	options := data.Options
	if len(options) == 0 {
		return "", nil
	}
	if len(b.SubcommandGroups) > 0 && options[0].Type == discordgo.ApplicationCommandOptionSubCommandGroup {
		if len(options[0].Options) == 0 {
			return options[0].Name, nil
		}
		command := options[0].Options[0]
		return options[0].Name + " " + command.Name, command.Options
	}
	if len(b.Subcommands) > 0 && options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		return options[0].Name, options[0].Options
	}
	return "", options
}

// ============================================================================
// Registering with Discord
// ============================================================================

// syncEvery is how often changes made on the dashboard reach Discord. Nothing is sent when a guild's list is unchanged
// (see the hash in syncGuild), so a short period only costs a config read per guild.
const syncEvery = 30 * time.Second

var (
	syncOnce sync.Once
	syncMu   sync.Mutex
	synced   = map[string][32]byte{} // guildID -> hash of the command list last sent
)

// StartCustomCommandSync registers every guild's custom commands with Discord now and then keeps them current. base is the
// built-in command list: with a development guild those are registered there too, and a guild-level overwrite
// there must not drop them.
func StartCustomCommandSync(s *discordgo.Session, appID, devGuildID string, base []*discordgo.ApplicationCommand) {
	run := func() {
		for _, g := range s.State.Guilds {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error(fmt.Sprintf("custom command sync panic in %s: %v", g.ID, r))
					}
				}()
				syncGuild(s, appID, g.ID, devGuildID, base)
			}()
		}
	}
	go run()
	syncOnce.Do(func() {
		go func() {
			for range time.Tick(syncEvery) {
				run()
			}
		}()
	})
}

func syncGuild(s *discordgo.Session, appID, guildID, devGuildID string, base []*discordgo.ApplicationCommand) {
	var desired []*discordgo.ApplicationCommand
	customs := 0
	for name, e := range entries(guildID) {
		if helpers.FindCommand(name) != nil || !e.cfg.IsEnabled || !e.cfg.IsSlashEnabled || e.cmd.Data == nil {
			continue
		}
		desired = append(desired, e.cmd.Data)
		customs++
	}
	if guildID == devGuildID {
		desired = append(append([]*discordgo.ApplicationCommand{}, base...), desired...)
	}
	raw, _ := json.Marshal(desired)
	hash := sha256.Sum256(raw)

	syncMu.Lock()
	last, seen := synced[guildID]
	syncMu.Unlock()
	if seen && last == hash {
		return
	}
	if !seen && customs == 0 && guildID != devGuildID {
		// Nothing to register; only clear what an earlier run left behind.
		if existing, err := s.ApplicationCommands(appID, guildID); err != nil || len(existing) == 0 {
			if err == nil {
				syncMu.Lock()
				synced[guildID] = hash
				syncMu.Unlock()
			}
			return
		}
	}
	if desired == nil {
		desired = []*discordgo.ApplicationCommand{}
	}
	if _, err := s.ApplicationCommandBulkOverwrite(appID, guildID, desired); err != nil {
		logger.Warn(fmt.Sprintf("registering custom commands in %s: %v", guildID, err))
		return
	}
	syncMu.Lock()
	synced[guildID] = hash
	syncMu.Unlock()
}

func init() {
	helpers.FindCustomCommand = findCustomCommand
	helpers.CustomCommandsFor = customCommandsFor
	helpers.CustomCommandEphemeral = func(i *discordgo.InteractionCreate, cmd *types.Command) (bool, bool) {
		e := entries(i.GuildID)[cmd.Name]
		if e == nil || e.cmd != cmd {
			return false, false
		}
		// A custom command is private or public by the called subcommand's own default reply.
		sub, _ := customCommandPath(i.ApplicationCommandData(), &e.b)
		if b := e.b.bodyFor(sub); b != nil {
			return b.Reply.Ephemeral, true
		}
		return false, false
	}
}
