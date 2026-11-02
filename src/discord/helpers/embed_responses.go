package helpers

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers/banner"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/types"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// scripts/gen_responses (run by `go generate ./src/discord/helpers`) reads the command sources and writes every built-in
// response as it was written, with each part the command fills in (a name, a number) shown as "…": "**…** received **…**
// starting ELO.". That file is embedded here and is the only copy: each time the bot syncs a guild's commands
// (SeedCommandConfigs) it writes a command's responses into the command's entry in the guild config, where the dashboard
// reads them. The bot also uses the file to read the changing parts back out of a response it is about to send and hands
// them to the message as
// {{response.value_1}}, {{response.value_2}}... (and {{response.title_1}}... for the title), so a rewritten message
// keeps the live values.

//go:generate go run ../../../scripts/gen_responses ../../..

//go:embed response_catalog.json
var responseCatalogJSON []byte

// changingPart marks a value the command fills in. No command source contains the character itself.
const changingPart = "…"

// scannedCommand is a command as the generator wrote it to response_catalog.json.
type scannedCommand struct {
	Name      string                  `json:"name"`
	Responses []types.CommandResponse `json:"responses"`
}

var (
	scanOnce      sync.Once
	scannedByName map[string][]types.CommandResponse
	originalByKey map[string]types.CommandResponse
)

// scanned is what the generator found in the sources: the responses by command name, and by response key.
func scanned() (map[string][]types.CommandResponse, map[string]types.CommandResponse) {
	scanOnce.Do(func() {
		scannedByName, originalByKey = map[string][]types.CommandResponse{}, map[string]types.CommandResponse{}
		var file struct {
			Commands []scannedCommand `json:"commands"`
		}
		if json.Unmarshal(responseCatalogJSON, &file) != nil {
			return
		}
		for _, c := range file.Commands {
			scannedByName[c.Name] = c.Responses
			for _, r := range c.Responses {
				originalByKey[r.Key] = r
			}
		}
	})
	return scannedByName, originalByKey
}

// responsesOf is what the command with this name says, as found in the sources (nil when nothing was found).
func responsesOf(command string) []types.CommandResponse {
	byName, _ := scanned()
	return byName[command]
}

// originalOf is the original text of the response "command.slug", if it is known.
func originalOf(key string) (types.CommandResponse, bool) {
	_, byKey := scanned()
	r, ok := byKey[key]
	return r, ok
}

// extractParts reads the values of format's changing parts out of text. Without a match the values are empty, so a
// message never shows a raw {{variable}}.
func extractParts(format, text string) []string {
	parts := strings.Split(format, changingPart)
	if len(parts) == 1 {
		return nil
	}
	var re strings.Builder
	re.WriteString("(?s)^")
	for i, p := range parts {
		re.WriteString(regexp.QuoteMeta(p))
		if i < len(parts)-1 {
			re.WriteString("(.*?)")
		}
	}
	re.WriteString("$")
	if m := regexp.MustCompile(re.String()).FindStringSubmatch(text); m != nil {
		return m[1:]
	}
	return make([]string, len(parts)-1)
}

// addOriginalParts sets {{response.title_N}} and {{response.value_N}} for the response being replaced.
func addOriginalParts(vars map[string]string, key string, emb *discordgo.MessageEmbed) {
	entry, ok := originalOf(key)
	if !ok {
		return
	}
	for i, v := range extractParts(entry.Title, emb.Title) {
		vars["response.title_"+strconv.Itoa(i+1)] = v
	}
	for i, v := range extractParts(entry.Description, emb.Description) {
		vars["response.value_"+strconv.Itoa(i+1)] = v
	}
}

// ============================================================================
// Customised command responses
// ============================================================================

// Every built-in command answers through EditInteractionEmbed. A guild can replace any of those answers with
// one of its saved Embed Builder messages (classic embeds or Components V2), chosen on the dashboard's
// Commands → Responses tab and stored in the guild config's panels.responses:
//
//	{ "defaults":  { "error": {"message_id": "msg-ab12"}, ... },        // every response that looks like this
//	  "overrides": { "register.username-taken": {...}, "register.*": {...} } }   // one response / a whole command
//
// The most specific rule wins: command.response, then command.*, then the look's default. The saved message
// reads what the command would have said as {{response.title}}, {{response.description}}, {{response.fields}} ...
// so a restyle keeps the command's own wording (and numbers) while changing everything around it.

// ResponseRule is defined in package types (embeds.go).
type ResponseRule = types.ResponseRule

// ResponseConfig is panels.responses.
type ResponseConfig struct {
	Defaults  map[string]ResponseRule `json:"defaults"`
	Overrides map[string]ResponseRule `json:"overrides"`
	// Layouts are the guild's rewrites of the bot's list and row text, by layout key (see DefineLayout). An empty one is the default.
	Layouts map[string]string `json:"layouts"`
}

// ResponseTitleSlug is the stable part of an embed title that names a response: what comes before a " — " or
// " · " (the variable tail of titles like "Leaderboard — Season 4"), lower-cased, with runs of other characters
// turned into one "-". The dashboard's catalogue is built with the same rule.
func ResponseTitleSlug(title string) string {
	for _, sep := range []string{" — ", " · "} {
		if cut := strings.Index(title, sep); cut >= 0 {
			title = title[:cut]
		}
	}
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "response"
	}
	return slug
}

// responseLook names how an embed looks (its preset), from its colour. Info, neutral and transparent share one.
func responseLook(color int) string {
	switch color {
	case ColorSuccess:
		return "success"
	case ColorError:
		return "error"
	case ColorCaution:
		return "caution"
	case ColorLoading:
		return "loading"
	case ColorInfo:
		return "info"
	default:
		return "default"
	}
}

// Resolve finds the rule that answers a response of `command` whose title slug is `slug` and look is `look`.
func (c ResponseConfig) Resolve(command, slug, look string) (ResponseRule, bool) {
	for _, r := range []ResponseRule{c.Overrides[command+"."+slug], c.Overrides[command+".*"], c.Defaults[look]} {
		if r.Usable() {
			return r, true
		}
	}
	return ResponseRule{}, false
}

// ============================================================================
// Loading (cached: every command answer asks)
// ============================================================================

const responseCacheTTL = 15 * time.Second

type responseState struct {
	cfg      ResponseConfig
	messages map[string]*MessageDoc
	at       time.Time
}

var responseCache sync.Map // guild id -> *responseState

// loadResponseState reads the guild's rules and saved messages; a variable so tests can fake it.
var loadResponseState = func(guildID string) *responseState {
	st := &responseState{messages: map[string]*MessageDoc{}, at: time.Now()}
	svc := database.Get()
	if svc == nil {
		return st
	}
	cfg, err := svc.GuildConfig.Get(guildID)
	if err != nil || cfg == nil || cfg.Panels.IsEmpty() {
		return st
	}
	var panels struct {
		Responses *ResponseConfig `json:"responses"`
		Messages  []MessageDoc    `json:"messages"`
	}
	if cfg.Panels.Unmarshal(&panels) != nil || panels.Responses == nil {
		return st
	}
	st.cfg = *panels.Responses
	for idx := range panels.Messages {
		st.messages[panels.Messages[idx].ID] = &panels.Messages[idx]
	}
	return st
}

func responseStateOf(guildID string) *responseState {
	if v, ok := responseCache.Load(guildID); ok {
		if st := v.(*responseState); time.Since(st.at) < responseCacheTTL {
			return st
		}
	}
	st := loadResponseState(guildID)
	st.at = time.Now()
	responseCache.Store(guildID, st)
	return st
}

// ============================================================================
// Answering
// ============================================================================

// responseVars are what a saved message can read: the response it replaces, who asked and where.
func responseVars(vars map[string]string, command, sub, slug, look string, emb *discordgo.MessageEmbed, layout *FieldsLayout) map[string]string {
	v := vars
	v["command.name"], v["command.subcommand"] = command, sub
	v["response.key"], v["response.type"] = command+"."+slug, look
	v["response.title"], v["response.description"] = emb.Title, emb.Description
	v["response.color"] = HexColorString(emb.Color)
	if emb.Footer != nil {
		v["response.footer"], v["response.footer_icon"] = emb.Footer.Text, emb.Footer.IconURL
	}
	if emb.Author != nil {
		v["response.author"] = emb.Author.Name
	}
	if emb.Image != nil {
		v["response.image"] = emb.Image.URL
	}
	if emb.Thumbnail != nil {
		v["response.thumbnail"] = emb.Thumbnail.URL
	}
	names, values := make([]string, 0, len(emb.Fields)), make([]string, 0, len(emb.Fields))
	for _, fld := range emb.Fields {
		names, values = append(names, fld.Name), append(values, fld.Value)
		slug := ResponseTitleSlug(fld.Name)
		v["field."+slug], v["field."+slug+".name"] = fld.Value, fld.Name
	}
	v["field.count"] = strconv.Itoa(len(names))
	v["response.fields"] = layout.Render(names, values)
	addOriginalParts(v, command+"."+slug, emb)
	return v
}

// WithGuildFooterIcon gives each embed that has a footer the server's icon as its footer icon, unless it already has
// one: every command answer then carries the server's icon without each builder having to know the guild.
func WithGuildFooterIcon(s *discordgo.Session, i *discordgo.InteractionCreate, embeds ...*discordgo.MessageEmbed) {
	if i == nil || i.Interaction == nil {
		return
	}
	WithGuildFooterIconForGuild(s, i.GuildID, embeds...)
}

// embedBranding is which default decorations a guild keeps on the bot's embeds; both are on until the guild turns
// them off in the dashboard (discord_profile.hide_embed_footer / hide_embed_thumbnail).
type embedBranding struct{ footer, thumbnail, timestamp bool }

var embedBrandingCache = base.NewTTLCache[embedBranding](15 * time.Second)

func guildEmbedBranding(guildID string) embedBranding {
	b, _ := embedBrandingCache.Get(guildID, func() (embedBranding, error) {
		on := embedBranding{footer: true, thumbnail: true, timestamp: true}
		svc := database.Get()
		if svc == nil {
			return on, nil
		}
		raw, err := svc.GuildConfig.Field(guildID, "discord_profile")
		if err != nil {
			return on, nil
		}
		var p struct {
			HideFooter    bool `json:"hide_embed_footer"`
			HideThumbnail bool `json:"hide_embed_thumbnail"`
			HideTimestamp bool `json:"hide_embed_timestamp"`
		}
		_ = json.Unmarshal(raw, &p)
		return embedBranding{footer: !p.HideFooter, thumbnail: !p.HideThumbnail, timestamp: !p.HideTimestamp}, nil
	})
	return b
}

var guildIconCache = base.NewTTLCache[string](10 * time.Minute)

// guildIconURL is the server's icon: from the session's cache, else fetched from Discord (the cache can lack it right
// after a reconnect). "" when the server has none.
func guildIconURL(s *discordgo.Session, guildID string) string {
	if s == nil || guildID == "" {
		return ""
	}
	if s.State != nil {
		if g, err := s.State.Guild(guildID); err == nil && g != nil && g.Icon != "" {
			return g.IconURL("128")
		}
	}
	icon, _ := guildIconCache.Get(guildID, func() (string, error) {
		g, err := s.Guild(guildID)
		if err != nil || g == nil || g.Icon == "" {
			return "", err
		}
		return g.IconURL("128"), nil
	})
	return icon
}

// withSavedFooterIcon gives the footer of a saved message the server's icon when the message sets none, like the built-in
// answers get, unless the server switched the footer branding off.
func withSavedFooterIcon(s *discordgo.Session, guildID string, payload map[string]any) {
	if !guildEmbedBranding(guildID).footer {
		return
	}
	embeds, _ := payload["embeds"].([]map[string]any)
	for _, e := range embeds {
		if ft, ok := e["footer"].(map[string]any); ok {
			if icon, _ := ft["icon_url"].(string); icon == "" {
				if icon = guildIconURL(s, guildID); icon != "" {
					ft["icon_url"] = icon
				}
			}
		}
	}
}

// WithGuildFooterIconForGuild brands embeds the way the guild chose: every embed gets a footer carrying the server's
// icon, a timestamp and a thumbnail unless it already has them, all on by default (the server can switch each off). Plain
// "blend" embeds (no colour bar, made that way on purpose) get no new footer or thumbnail.
func WithGuildFooterIconForGuild(s *discordgo.Session, guildID string, embeds ...*discordgo.MessageEmbed) {
	if guildID == "" {
		return
	}
	b := guildEmbedBranding(guildID)
	var icon string
	for _, emb := range embeds {
		if emb == nil {
			continue
		}
		if emb.Color == EmbedBlendColor { // an embed the server made plain on purpose
			// A plain embed gets no new footer or thumbnail, but a footer it already has still carries the server's icon.
			if b.footer && emb.Footer != nil && emb.Footer.IconURL == "" {
				if icon == "" {
					icon = guildIconURL(s, guildID)
				}
				emb.Footer.IconURL = icon
			}
			continue
		}
		if b.thumbnail && emb.Thumbnail == nil {
			emb.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: ThumbBot}
		}
		if !b.footer {
			continue
		}
		if b.timestamp && emb.Timestamp == "" {
			emb.Timestamp = time.Now().UTC().Format(time.RFC3339)
		}
		if emb.Footer == nil {
			emb.Footer = &discordgo.MessageEmbedFooter{Text: "Ranked Bedwars"}
		}
		if emb.Footer.IconURL != "" {
			continue
		}
		if icon == "" {
			icon = guildIconURL(s, guildID)
		}
		emb.Footer.IconURL = icon
	}
}

// HexColorString formats an embed colour as #rrggbb.
func HexColorString(color int) string { return fmt.Sprintf("#%06x", color&0xffffff) }

// buildResponsePayload is the saved message that replaces `emb`, with its variables filled in, or false when no
// rule (or no usable message) applies.
func buildResponsePayload(st *responseState, command, sub string, emb *discordgo.MessageEmbed, vars map[string]string) (map[string]any, bool) {
	slug, look := ResponseTitleSlug(emb.Title), responseLook(emb.Color)
	rule, ok := st.cfg.Resolve(command, slug, look)
	if !ok {
		return nil, false
	}
	var doc *MessageDoc
	if rule.Reply != nil {
		doc = ReplyDoc(rule.Reply)
	} else if doc = st.messages[rule.MessageID]; doc == nil {
		return nil, false // the message was deleted: keep the built-in answer
	}
	return doc.Payload(VarFiller(responseVars(vars, command, sub, slug, look, emb, doc.ResponseFields))), true
}

// ReplyDoc is the message a dashboard-built reply describes (the same reading custom commands give a reply).
func ReplyDoc(r *types.ResponseReply) *MessageDoc {
	doc := &MessageDoc{Mode: "embeds", Buttons: r.Buttons}
	switch r.Type {
	case "component":
		doc.Mode, doc.Buttons, doc.V2 = "v2", nil, r.Components
	case "embed":
		doc.Content, doc.Embeds = r.Content, r.Embeds
	default:
		if doc.Content = r.Content; doc.Content == "" {
			doc.Content = r.Text
		}
	}
	return doc
}

// sendCustomResponse answers with the guild's saved message instead of `emb`, if it set one up. It reports
// whether it did; on false (or an error) the caller sends the built-in embed. The answer's own components (pagination
// buttons, menus) stay on the saved message, below its content.
func sendCustomResponse(s *discordgo.Session, i *discordgo.InteractionCreate, emb *discordgo.MessageEmbed, components ...discordgo.MessageComponent) (bool, error) {
	if i == nil || i.Interaction == nil || i.GuildID == "" || emb == nil {
		return false, nil
	}
	data, ok := i.Data.(discordgo.ApplicationCommandInteractionData)
	if !ok {
		return false, nil
	}
	prefix := IsPrefixInteraction(i)
	if !prefix && !IsDeferredSlashInteraction(i) {
		return false, nil // an answer that is not an edit of a deferred reply can't carry a saved message
	}
	st := responseStateOf(i.GuildID)
	if len(st.cfg.Defaults) == 0 && len(st.cfg.Overrides) == 0 {
		return false, nil
	}

	vars := InteractionVars(s, i)

	payload, ok := buildResponsePayload(st, data.Name, SubcommandPathFromInteraction(data), emb, vars)
	if !ok {
		return false, nil
	}
	withSavedFooterIcon(s, i.GuildID, payload)
	if len(components) > 0 {
		payload["components"] = append(componentList(payload["components"]), componentsAny(components)...)
	}
	if prefix {
		_, err := SendPayload(s, i.ChannelID, payload)
		return err == nil, err
	}
	err := EditOriginalPayload(s, i.Interaction, payload)
	return err == nil, err
}

// componentList is a payload's components as a slice that more can be appended to.
func componentList(v any) []any {
	switch c := v.(type) {
	case []map[string]any:
		out := make([]any, len(c))
		for n := range c {
			out[n] = c[n]
		}
		return out
	case []any:
		return c
	}
	return nil
}

func componentsAny(cs []discordgo.MessageComponent) []any {
	out := make([]any, len(cs))
	for n := range cs {
		out[n] = cs[n]
	}
	return out
}

// ============================================================================
// What an interaction offers a saved message
// ============================================================================

// InteractionVars is everything a saved message can read about the interaction that triggered the answer, as
// {{path}} variables: who asked (user.*, member.*), where (guild.*, channel.*), what they ran (command.*) and
// every option they gave (option.<name>, and for a user, channel or role option also .id, .mention, .name ...).
func InteractionVars(s *discordgo.Session, i *discordgo.InteractionCreate) map[string]string {
	v := map[string]string{}
	if i == nil || i.Interaction == nil {
		return v
	}
	invoker := i.User
	if i.Member != nil && i.Member.User != nil {
		invoker = i.Member.User
	}
	if s != nil && s.State != nil {
		for k, val := range banner.GuildVars(s, i.GuildID, invoker) {
			v[k] = val
		}
	} else if invoker != nil {
		v["user.name"], v["user.mention"], v["user.avatar"] = invoker.Username, invoker.Mention(), banner.AvatarOf(invoker)
	}
	now := time.Now().UTC()
	v["time.date"], v["time.time"] = now.Format("2006-01-02"), now.Format("15:04")
	v["time.unix"], v["time.relative"] = fmt.Sprint(now.Unix()), fmt.Sprintf("<t:%d:R>", now.Unix())

	v["interaction.id"], v["interaction.type"] = i.ID, i.Type.String()
	v["interaction.locale"], v["interaction.guild_locale"] = string(i.Locale), ""
	if i.GuildLocale != nil {
		v["interaction.guild_locale"] = string(*i.GuildLocale)
	}
	v["guild.id"] = i.GuildID
	v["channel.id"], v["channel.mention"] = i.ChannelID, "<#"+i.ChannelID+">"
	if s != nil && s.State != nil {
		if ch, err := s.State.Channel(i.ChannelID); err == nil && ch != nil {
			v["channel.name"] = ch.Name
		}
		if g, err := s.State.Guild(i.GuildID); err == nil && g != nil && g.OwnerID != "" {
			v["guild.owner"], v["guild.owner_mention"] = g.OwnerID, "<@"+g.OwnerID+">"
		}
	}
	if invoker != nil {
		v["user.id"], v["user.tag"] = invoker.ID, invoker.Username
		v["user.display_name"] = invoker.Username
		if invoker.GlobalName != "" {
			v["user.display_name"] = invoker.GlobalName
		}
		v["user.created"] = fmt.Sprintf("<t:%d:R>", snowflakeUnix(invoker.ID))
	}
	if m := i.Member; m != nil {
		v["member.nick"] = m.Nick
		v["member.display_name"] = v["user.display_name"]
		if m.Nick != "" {
			v["member.display_name"] = m.Nick
		}
		v["member.joined"] = fmt.Sprintf("<t:%d:R>", m.JoinedAt.Unix())
		v["member.role_count"] = fmt.Sprint(len(m.Roles))
		mentions := make([]string, len(m.Roles))
		for n, id := range m.Roles {
			mentions[n] = "<@&" + id + ">"
		}
		v["member.roles"] = strings.Join(mentions, " ")
	}
	if i.Message != nil {
		v["message.id"], v["message.content"] = i.Message.ID, i.Message.Content
		v["message.url"] = fmt.Sprintf("https://discord.com/channels/%s/%s/%s", i.GuildID, i.ChannelID, i.Message.ID)
	}
	if data, ok := i.Data.(discordgo.ApplicationCommandInteractionData); ok {
		sub := SubcommandPathFromInteraction(data)
		v["command.name"], v["command.subcommand"] = data.Name, sub
		v["command.full"] = strings.TrimSpace(data.Name + " " + strings.ReplaceAll(sub, ".", " "))
		v["command.prefix_used"] = fmt.Sprint(IsPrefixInteraction(i))
		addOptionVars(v, data.Options, data.Resolved)
	}
	return v
}

// addOptionVars sets option.<name> for every option, reaching into subcommands.
func addOptionVars(v map[string]string, opts []*discordgo.ApplicationCommandInteractionDataOption, res *discordgo.ApplicationCommandInteractionDataResolved) {
	for _, o := range opts {
		if o == nil {
			continue
		}
		key := "option." + o.Name
		switch o.Type {
		case discordgo.ApplicationCommandOptionSubCommand, discordgo.ApplicationCommandOptionSubCommandGroup:
			addOptionVars(v, o.Options, res)
		case discordgo.ApplicationCommandOptionUser:
			id := fmt.Sprint(o.Value)
			v[key], v[key+".id"], v[key+".mention"] = "<@"+id+">", id, "<@"+id+">"
			if res != nil && res.Users[id] != nil {
				u := res.Users[id]
				v[key+".name"], v[key+".tag"], v[key+".avatar"] = u.Username, u.Username, banner.AvatarOf(u)
				if u.GlobalName != "" {
					v[key+".name"] = u.GlobalName
				}
			}
		case discordgo.ApplicationCommandOptionChannel:
			id := fmt.Sprint(o.Value)
			v[key], v[key+".id"], v[key+".mention"] = "<#"+id+">", id, "<#"+id+">"
			if res != nil && res.Channels[id] != nil {
				v[key+".name"] = res.Channels[id].Name
			}
		case discordgo.ApplicationCommandOptionRole:
			id := fmt.Sprint(o.Value)
			v[key], v[key+".id"], v[key+".mention"] = "<@&"+id+">", id, "<@&"+id+">"
			if res != nil && res.Roles[id] != nil {
				v[key+".name"] = res.Roles[id].Name
			}
		default:
			v[key] = fmt.Sprint(o.Value)
		}
	}
}

// snowflakeUnix is when a Discord id was created.
func snowflakeUnix(id string) int64 {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0
	}
	return (n>>22 + 1420070400000) / 1000
}

// ============================================================================
// Layouts: the text lists and rows are built from
// ============================================================================

// A layout is one piece of text an embed builds from a list or a row (a bot's line, a queue's heading) with {var}
// placeholders. The code owns the default; a guild rewrites it on the dashboard (panels.responses.layouts[key]) and an
// empty rewrite means the default. A key is "<command>.<name>": the command's name is how the bot hands the dashboard
// the layouts of each command (see layoutsOf).

var layoutRegistry = map[string]types.CommandLayout{}

// DefineLayout registers a layout at start-up and returns its key, so a package-level var can hold it:
//
//	var botLine = DefineLayout("bots.line", "One bot", "`{name}`", "name", "tier")
func DefineLayout(key, label, def string, vars ...string) string {
	layoutRegistry[key] = types.CommandLayout{Key: key, Label: label, Default: def, Vars: vars}
	return key
}

// DefineRepeatedLayout is DefineLayout for a layout written once per row of a list.
func DefineRepeatedLayout(key, label, def string, vars ...string) string {
	DefineLayout(key, label, def, vars...)
	l := layoutRegistry[key]
	l.Repeats = true
	layoutRegistry[key] = l
	return key
}

// layoutsOf are the layouts whose key starts with "<command>.", in key order.
func layoutsOf(command string) []types.CommandLayout {
	var out []types.CommandLayout
	for key, l := range layoutRegistry {
		if strings.HasPrefix(key, command+".") {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Key < out[b].Key })
	return out
}

// LayoutText is the layout's text for a guild: its rewrite, or the default.
func LayoutText(guildID, key string) string {
	if guildID != "" {
		if t := responseStateOf(guildID).cfg.Layouts[key]; strings.TrimSpace(t) != "" {
			return t
		}
	}
	return layoutRegistry[key].Default
}

// FillLayout writes a layout with its variables. A line whose variables are all empty is left out (so "{note}" on a
// line of its own disappears with no note), and a name that is not in vars is empty.
func FillLayout(guildID, key string, vars map[string]string) string {
	var out []string
	for _, line := range strings.Split(LayoutText(guildID, key), "\n") {
		found, filled := false, false
		text := banner.VarPattern.ReplaceAllStringFunc(line, func(whole string) string {
			m := banner.VarPattern.FindStringSubmatch(whole)
			found = true
			val := vars[m[1]+m[2]]
			if val != "" {
				filled = true
			}
			return val
		})
		if found && !filled {
			continue
		}
		out = append(out, text)
	}
	return strings.Join(out, "\n")
}

// GuildOf is the guild an interaction came from ("" for none), so an embed builder can name its layouts' guild.
func GuildOf(i *discordgo.InteractionCreate) string {
	if i == nil || i.Interaction == nil {
		return ""
	}
	return i.GuildID
}

// LayoutSeparator is a layout that goes between the items of a list: "\n" in it means a new line.
func LayoutSeparator(guildID, key string) string {
	return strings.ReplaceAll(LayoutText(guildID, key), `\n`, "\n")
}

// ============================================================================
// Answer Guidance
