package helpers

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	apputil "ranked-bedwars/src/utility"
)

// ============================================================================
// Embed Color Constants
// ============================================================================

const (
	ColorDefault     = 0x55FFFF
	ColorSuccess     = 0x69f569
	ColorCaution     = 0xe78432
	ColorInfo        = 0x393a41
	ColorError       = 0xdc4d4d
	ColorLoading     = 0xe7e74c
	ColorNeutral     = 0x393a41
	ColorTransparent = 0x393a41

	// EmbedBlendColor is the background of an embed in Discord's dark theme: a bar of this colour is not seen, so the embed
	// looks like one plain block. (A Components V2 container needs no such colour: without an accent it has no bar.)
	EmbedBlendColor = 0x2b2d31
)

var presetColors = map[string]int{
	"success":     ColorSuccess,
	"caution":     ColorCaution,
	"info":        ColorInfo,
	"error":       ColorError,
	"loading":     ColorLoading,
	"neutral":     ColorNeutral,
	"transparent": ColorTransparent,
}

// ============================================================================
// General Embed Preset Functions
// ============================================================================

// Preset generates a discordgo embed from a named preset style.
func Preset(presetType string, options types.PresetOptions) *discordgo.MessageEmbed {
	resolvedColor, exists := presetColors[presetType]
	if !exists {
		resolvedColor = ColorDefault
	}

	if options.Color != 0 {
		resolvedColor = options.Color
	}

	embed := &discordgo.MessageEmbed{Color: resolvedColor}

	// Apply Title & Description
	if title, ok := parseOptionalString(options.Title); ok {
		embed.Title = title
	}
	if desc, ok := parseOptionalString(options.Description); ok {
		embed.Description = desc
	}

	// Author configuration
	if options.Author != nil {
		name, showAuthor := parseOptionalString(options.Author.Name)
		if showAuthor && name != "" {
			embed.Author = &discordgo.MessageEmbedAuthor{Name: name}
			if icon, ok := parseOptionalString(options.Author.IconURL); ok {
				embed.Author.IconURL = icon
			}
		}
	}

	// Footer configuration
	footerText, showFooter := parseOptionalString(options.FooterText)
	if showFooter {
		if footerText == "" {
			footerText = "Ranked Bedwars"
		}
		embed.Footer = &discordgo.MessageEmbedFooter{Text: footerText}
		if icon, ok := parseOptionalString(options.FooterIcon); ok {
			embed.Footer.IconURL = icon
		}
	}

	// Timestamp handling
	if presetType != "transparent" {
		switch ts := options.Timestamp.(type) {
		case bool:
			if !ts {
				embed.Timestamp = ""
			}
		case time.Time:
			embed.Timestamp = ts.Format(time.RFC3339)
		case int64:
			embed.Timestamp = time.Unix(ts, 0).Format(time.RFC3339)
		default:
			embed.Timestamp = time.Now().Format(time.RFC3339)
		}
	}

	// Fields, Image, and Thumbnail
	if len(options.Fields) > 0 {
		embed.Fields = options.Fields
	} else {
		withGuidance(embed, presetType, embed.Title)
	}

	if img, ok := parseOptionalString(options.Image); ok && img != "" {
		embed.Image = &discordgo.MessageEmbedImage{URL: img}
	}

	if thumb, ok := parseOptionalString(options.Thumbnail); ok && thumb != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: thumb}
	}

	return embed
}

// Helper to consolidate preset wrapper functions
func withTitleDesc(presetType, title, description string, opts types.PresetOptions) *discordgo.MessageEmbed {
	if opts.Title == nil {
		opts.Title = title
	}
	if opts.Description == nil {
		opts.Description = description
	}
	return Preset(presetType, opts)
}

// Preset Short-hands
func Default(title, description string, opts types.PresetOptions) *discordgo.MessageEmbed {
	return withTitleDesc("default", title, description, opts)
}

func Success(title, description string, opts types.PresetOptions) *discordgo.MessageEmbed {
	return withTitleDesc("success", title, description, opts)
}

func Caution(title, description string, opts types.PresetOptions) *discordgo.MessageEmbed {
	return withTitleDesc("caution", title, description, opts)
}

func Info(title, description string, opts types.PresetOptions) *discordgo.MessageEmbed {
	return withTitleDesc("info", title, description, opts)
}

func Error(title, description string, opts types.PresetOptions) *discordgo.MessageEmbed {
	return withTitleDesc("error", title, description, opts)
}

func Loading(title, description string, opts types.PresetOptions) *discordgo.MessageEmbed {
	return withTitleDesc("loading", title, description, opts)
}

func Neutral(title, description string, opts types.PresetOptions) *discordgo.MessageEmbed {
	return withTitleDesc("neutral", title, description, opts)
}

func Transparent(title, description string, opts types.PresetOptions) *discordgo.MessageEmbed {
	return withTitleDesc("transparent", title, description, opts)
}

// Utility to cleanly parse string OR bool false options
func parseOptionalString(val any) (string, bool) {
	switch v := val.(type) {
	case string:
		return v, true
	case bool:
		return "", v
	default:
		return "", true
	}
}

// ============================================================================
// Middleware-Specific Embed Functions
// ============================================================================

// GuildNotRegisteredEmbed creates an embed for guild not registered errors.
func GuildNotRegisteredEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Guild Not Registered",
		Description: "This guild is not registered with Ranked Bedwars yet. An administrator must run `/setup init` to initialize it.",
		Thumbnail:   ThumbFlag,
	})
}

// SimpleErrorEmbed creates a simple error embed.
func SimpleErrorEmbed(message string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{Title: "Error", Description: message, Thumbnail: ThumbFlag})
}

// CommandErrorEmbed creates an embed for command execution errors.
func CommandErrorEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Command Error",
		Description: "An unexpected error occurred while executing this command. Please try again later or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

// CommandErrorSlashEmbed creates an embed for slash command execution errors.
func CommandErrorSlashEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Command Error",
		Description: "An unexpected error occurred while executing this command. Please try again later or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

// ConfigurationErrorEmbed creates an embed for configuration errors.
func ConfigurationErrorEmbed(description string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{Title: "Configuration Error", Description: description, Thumbnail: ThumbFlag})
}

// AccessDeniedEmbed creates an embed for access denied errors.
func AccessDeniedEmbed(title, message string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{Title: title, Description: message, Thumbnail: ThumbFlag})
}

// ============================================================================
// Error Formatting Functions
// ============================================================================

// FormatConfigurationErrorAdmin formats configuration errors for
// administrators, listing both what's blocking the command (missing) and
// what's merely available (optional) — either may be empty.
func FormatConfigurationErrorAdmin(cmdName string, missing, optional []types.Requirement) string {
	var description string
	if len(missing) > 0 {
		description += fmt.Sprintf("The command `%s` requires the following resources to be configured:\n", cmdName)
		for _, req := range missing {
			description += fmt.Sprintf("- %s: %s\n", req.Type, req.Key)
		}
	}
	if len(optional) > 0 {
		if description != "" {
			description += "\n"
		}
		description += fmt.Sprintf("`%s` also has these optional resources available:\n", cmdName)
		for _, req := range optional {
			description += fmt.Sprintf("- %s: %s\n", req.Type, req.Key)
		}
	}
	description += fmt.Sprintf("\nYou're able to configure these at any time using `/config role|channel|category set command:%s`.", cmdName)
	return description
}

// FormatConfigurationErrorUser formats configuration errors for regular users.
func FormatConfigurationErrorUser(cmdName string, missing []types.Requirement) string {
	description := fmt.Sprintf("The command `%s` cannot be used because required resources are not configured.\n", cmdName)
	description += "Please contact a server administrator to resolve this issue."
	return description
}

// ============================================================================
// Component Building Functions
// ============================================================================

// MinecraftBustAvatarURL returns the bust render URL used for player thumbnails.
func MinecraftBustAvatarURL(username string) string {
	name := strings.TrimSpace(username)
	if name == "" {
		name = "Steve"
	}
	return "https://nmsr.nickac.dev/bust/" + url.PathEscape(name)
}

// The old bot's thumbnail icons that earn their place (white glyphs, made for Discord's dark theme). Its candle icons,
// the clock/timer duplicate, the yellow/green/red logo tiles and the empty `bot` fallback were left out: they say
// nothing a command could use. The doubleSwords, timer, tada, sprout, flag and trashCan constants are in game_helper.go.
const (
	ThumbHammer  = "https://i.imgur.com/2dLIqF6.png"
	ThumbClock   = "https://i.imgur.com/i2JnqZm.png"
	ThumbLoad    = "https://i.imgur.com/KowXZbQ.png"
	ThumbPlayer  = "https://i.imgur.com/E0l2g2j.png"
	ThumbPlayers = "https://i.imgur.com/5yrykSY.png"
	ThumbBed     = "https://i.imgur.com/BBlmpYu.png"
	ThumbGuild   = "https://i.imgur.com/LvOizEZ.png"
	ThumbBot     = "https://i.imgur.com/U0dRZnV.png"
)

// DiscordRelativeTimestamp formats a Discord relative timestamp (`<t:unix:R>`).
func DiscordRelativeTimestamp(t time.Time) string {
	return fmt.Sprintf("<t:%d:R>", t.Unix())
}

// ============================================================================
// The catalogue of the built-in responses
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

// WithGuildFooterIconForGuild gives embeds with a footer the server's icon unless they already have one.
func WithGuildFooterIconForGuild(s *discordgo.Session, guildID string, embeds ...*discordgo.MessageEmbed) {
	if s == nil || s.State == nil || guildID == "" {
		return
	}
	var icon string
	for _, emb := range embeds {
		if emb == nil || emb.Footer == nil || emb.Footer.IconURL != "" {
			continue
		}
		if icon == "" {
			g, err := s.State.Guild(guildID)
			if err != nil || g == nil || g.Icon == "" {
				return
			}
			icon = g.IconURL("128")
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
		doc = replyDoc(rule.Reply)
	} else if doc = st.messages[rule.MessageID]; doc == nil {
		return nil, false // the message was deleted: keep the built-in answer
	}
	return doc.Payload(VarFiller(responseVars(vars, command, sub, slug, look, emb, doc.ResponseFields))), true
}

// replyDoc is the message a dashboard-built reply describes (the same reading custom commands give a reply).
func replyDoc(r *types.ResponseReply) *MessageDoc {
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
		for k, val := range GuildVars(s, i.GuildID, invoker) {
			v[k] = val
		}
	} else if invoker != nil {
		v["user.name"], v["user.mention"], v["user.avatar"] = invoker.Username, invoker.Mention(), AvatarOf(invoker)
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
				v[key+".name"], v[key+".tag"], v[key+".avatar"] = u.Username, u.Username, AvatarOf(u)
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
		text := varPattern.ReplaceAllStringFunc(line, func(whole string) string {
			m := varPattern.FindStringSubmatch(whole)
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
// ============================================================================

// answerGuidance is the closing block of a built-in answer: what the reader can do about it. It is keyed by the embed
// title, so every command that answers "Invalid Target" or "Unexpected Error" gives the same, specific help, and a
// short answer never ends up as one line squeezed beside its thumbnail. Each entry is a list of short lines.
var answerGuidance = map[string][]string{
	// Players and registration
	"Registration Required":    {"Run `/register` to link your Minecraft account.", "Then run this command again."},
	"Not Registered":           {"Run `/register` to link your Minecraft account first."},
	"Registration Unavailable": {"A guild administrator must add at least one rank in the dashboard under Matchmaking › Rank Thresholds."},
	"Already Registered":       {"Your profile already exists in this guild.", "Use `/rename` to change the linked username, or `/profile` to view it."},
	"Already Linked":           {"That account is already attached to the profile.", "Use `/profile` to review the linked username."},
	"Player Not Registered":    {"They must run `/register` to link a Minecraft account first.", "Staff can use `/forceregister` to register them."},
	"Player Not Found":         {"Check the spelling, or mention the player instead of typing the name."},
	"Profile Not Found":        {"Run `/sync` (or `/forcesync` for another player) to rebuild the profile.", "Contact staff if it keeps happening."},
	"Member Not Found":         {"Check that the person is still in this guild.", "They may have left or been removed."},
	"Invalid Target":           {"Select a member from the command's user option.", "Start typing a name, or paste their Discord ID, to find them."},
	"Invalid User":             {"Select a member who is in this guild.", "Paste a valid Discord ID if they are not in the list."},
	"Invalid User ID":          {"Turn on Developer Mode in Discord, then right-click the user and choose **Copy User ID**.", "IDs are 17 to 20 digits long."},
	"Invalid Username":         {"Minecraft usernames are 3 to 16 characters long.", "Only letters, numbers and underscores are allowed."},
	"Username Taken":           {"Another player in this guild already uses that username.", "Contact staff if the account is yours."},
	"Account Not Found":        {"Check the spelling of the Minecraft username.", "Use the exact name shown in your Minecraft launcher."},
	"Verification Unavailable": {"The account verification service is not reachable right now.", "Try again in a few minutes."},
	"Invalid Nickname":         {"Nicknames are 3 to 16 characters long.", "Only letters, numbers and underscores are allowed."},
	"Nickname Taken":           {"Another player in this guild already uses that nickname.", "Try a different one."},
	"Nickname Already Set":     {"Your nickname already has that value.", "Choose a different nickname if you want a change."},
	"No Nickname Set":          {"Set one with `/nickname` first.", "Then you can remove it again."},
	"Premium Username":         {"Choose a nickname that is not a registered premium Minecraft name."},
	"Profile Hidden":           {"Only the owner can see this profile.", "They can change it with `/settings toggle`."},
	"Stats Hidden":             {"Only the owner can see these stats.", "They can change it with `/settings toggle`."},
	"Stats Private":            {"Only the owner can see these stats.", "They can change it with `/settings toggle`."},
	"No Stats Found":           {"The player needs to finish a ranked game this season first.", "Check again after their next game."},
	"Already Claimed":          {"Starting ELO can only be claimed once per season.", "Use `/stats` to see your current ELO."},
	"Claiming Disabled":        {"This guild has switched off starting ELO claims.", "Ask staff if you think this is a mistake."},
	"No Starting ELO Perk":     {"Starting ELO comes from perks tied to roles.", "Check that you hold the role the perk needs, then run `/claim` again."},
	"ELO Decay Incoming":       {"Play a ranked game before the timer ends to keep your ELO.", "Use `/decays` to review your decay history."},
	"ELO Decayed":              {"Play ranked games regularly to avoid further decay.", "Use `/decays` to review your decay history."},
	"No Games To Wipe":         {"Pick an earlier game number, or wipe the whole season instead."},
	"No Wipe Found":            {"Only wiped stats can be restored.", "Check the wipe ID and try again."},
	"Already Restored":         {"That wipe was already undone, so nothing changed.", "Use `/stats` to review the player's current numbers."},

	// Seasons, ranks and guild setup
	"No Active Season":                 {"A guild administrator must start a season in the dashboard under Matchmaking › Season Periods.", "Until then, stats and ELO changes are unavailable."},
	"No Configured Ranks":              {"Add rank tiers in the dashboard under Matchmaking › Rank Thresholds."},
	"No Ranks Configured":              {"Add rank tiers in the dashboard under Matchmaking › Rank Thresholds."},
	"No Registered Bots":               {"Add game bots in the dashboard under Matchmaking › Automated Bots."},
	"No Registered Maps":               {"Add maps in the dashboard under Matchmaking › Map Selections."},
	"LFG Not Configured":               {"A guild administrator must set the LFG channel and role in the dashboard.", "Until then, ask for games in your usual queue channel."},
	"Ladder Not Configured":            {"A guild administrator must add ladder steps in the dashboard under Moderation."},
	"Guild Setup Failed":               {"Give the bot Manage Roles, Manage Channels and Manage Server.", "Then run `/setup init` again."},
	"Guild Sync Failed":                {"Give the bot Manage Roles, Manage Channels and Manage Server.", "Then run `/setup sync` again."},
	"Command Not Set Up":               {"Run `/config setup` to create the command's stored configuration.", "Then try again."},
	"Optional Configuration Available": {"The command works without it.", "Set the optional items in the dashboard under Commands to unlock more."},
	"Unknown Command":                  {"Check the command name in the slash menu.", "Run `/config` without options to see what can be configured."},
	"Unknown Requirement Key":          {"Run `/config` without options to see the keys the command accepts."},
	"Unknown Subcommand":               {"Run `/config` without arguments to see the available routes."},
	"Unknown Setting":                  {"Choose one of the settings from the command's option list."},
	"Unknown Statistic":                {"Choose a statistic from the command's option list."},
	"Invalid Key":                      {"Provide a key with at least one character.", "Run `/config` without options to see the valid keys."},
	"Invalid Prefix":                   {"Choose a prefix of 1 to 5 characters, for example `!` or `rb!`."},
	"Missing Value":                    {"Provide a value for the option you chose.", "Run the command again with every required option filled in."},
	"Nothing To Change":                {"The new value matches the current one.", "Change it before saving."},

	// Games, queues and parties
	"Game Not Found":           {"Check the game number and try again.", "Use `/games` to list recent game numbers."},
	"Game Not Voided":          {"Only voided games can be restored.", "Use `/viewgame` to check the game's current state."},
	"Hosted Game Not Found":    {"Use `/host list` to see the games that are running.", "Check the game number and try again."},
	"No Game":                  {"Run the command inside a game channel, or provide a game number.", "Use `/games` to look up recent game numbers."},
	"No Game or Queue":         {"Run it in a game text channel to see that game.", "Or join a queue voice channel to see its status."},
	"Not a Game Channel":       {"Run it in the text channel of your game.", "Game channels are created when the game starts."},
	"Not a Queue Channel":      {"Join one of the guild's queue voice channels.", "Then run the command again."},
	"Not in Match":             {"Only players in the game can do this.", "Ask staff to help if you should be in it."},
	"Not in Game":              {"Join your team voice channel while a game is running.", "Then run the command again."},
	"Not in Voice":             {"Connect to your team voice channel first.", "Then run the command again."},
	"Wrong Channel":            {"Join your assigned team voice channel and try again."},
	"Already in Match":         {"That player is already part of this game.", "Pick someone who is not playing in it."},
	"Not In Progress":          {"Results can only be submitted while a game is in progress.", "Check the game's status with `/viewgame`."},
	"Already Submitted":        {"A result is already waiting for staff to score.", "Staff will review the proof and finish the game."},
	"Already Voided":           {"That game is already voided, so no ELO or stats are recorded for it.", "Use `/unvoid` if it was voided by mistake."},
	"Too Early":                {"Wait until the minimum time since the game started has passed.", "The time left is shown above."},
	"No Team":                  {"Only players assigned to a team can submit a result.", "Ask staff if you were substituted in."},
	"Missing Attachment":       {"Attach a screenshot of the win screen to your message.", "You can also paste a direct link to the image."},
	"Invalid Attachment":       {"Attach an image file: PNG, JPG, GIF or WebP.", "Videos and other file types are not accepted."},
	"Invalid MVP":              {"Select the MVP from the command's player option.", "The MVP must be a player in the game."},
	"Invalid State":            {"The game must be finished (completed or voided) first.", "Use `/score` for games that are still in progress."},
	"Invalid Selection":        {"Pick a player who is still in the pool.", "Players already drafted cannot be picked again."},
	"No Active Draft":          {"A draft only runs while captains are picking teams.", "Check the game channel for the draft panel."},
	"Draft Complete":           {"Every pick has been made, so nothing more can be drafted."},
	"Queue Entry Denied":       {"Check the reason listed above and fix it.", "Then rejoin the queue."},
	"Queues Closed":            {"Staff have closed ranked queues for now.", "Games start again when queues reopen."},
	"Queues Paused":            {"Staff have paused ranked queues for now.", "Games start again when queues reopen."},
	"Party ELO Limit":          {"Party members must be within the guild's ELO gap.", "Invite players closer to your ELO."},
	"Not in a Party":           {"Create one with `/party create`.", "Or accept a pending invitation."},
	"Not Your Invitation":      {"Only the invited player can answer an invitation.", "Ask the leader to send you your own."},
	"Removed from Party":       {"Create or join another party to keep queueing together."},
	"Party Disbanded":          {"Create a new party any time with `/party create`."},
	"Invitation Declined":      {"Ask the leader for a fresh invitation if you change your mind."},
	"Invites Closed":           {"Reopen invites with `/party invites` when you want more players."},
	"Invites Open":             {"Invite players with `/party invite`."},
	"Left Party":               {"Create a new party with `/party create`, or accept another invitation."},
	"You Are the Party Leader": {"Invite players with `/party invite`.", "Disband the party with `/party disband`."},

	// Moderation and staff
	"Reason Required":            {"Add a clear explanation in the `reason` option.", "Reasons are recorded in the player's history."},
	"Hierarchy Error":            {"Move the bot's role above the member's highest role in Server Settings › Roles.", "Or ask a higher-ranked moderator to handle it."},
	"Punishment Already Active":  {"Check the existing punishment with `/punishments`.", "Clear it first if a new one is needed."},
	"Punishment Not Active":      {"Verify the player's status with `/punishments`."},
	"Punishment Not Found":       {"Double-check the ID from `/punishments`."},
	"No Active Punishments":      {"Their full history is available with `/punishments`."},
	"Strike Not Found":           {"Double-check the ID from `/strikes`."},
	"No Strikes Found":           {"Review the player's history with `/strikes`."},
	"Role Already Assigned":      {"No change is needed.", "Use `/role list` to see their roles."},
	"Role Not Assigned":          {"Use `/role list` to see their current roles."},
	"Role Not Manageable":        {"Move the bot's role above that role in Server Settings › Roles."},
	"No Members Found":           {"Mention at least one member, paste their IDs or pick a role filter."},
	"No Messages Found":          {"Adjust or remove the filters.", "Or pick a channel with recent messages."},
	"Messages Partially Cleared": {"Messages older than 14 days cannot be bulk deleted.", "Run it again with `override_14days: True` to remove them one by one."},
	"Invalid Time Filter":        {"Use values like `7d`, `2w` or `1m`, or leave the filter empty to see everything."},
	"Page Empty":                 {"Try a lower page number.", "Use the buttons under the list to move between pages."},
	"Invalid Link":               {"Right-click the message and choose **Copy Message Link**.", "Paste the full link, starting with `https://discord.com/channels/`."},
	"Restricted Destination":     {"Only links from this guild work.", "Copy the message link from a channel in this guild."},
	"Invalid Channel":            {"Choose a text channel the bot can see and write in."},
	"Invalid Code":               {"Check the code and enter it exactly as shown.", "Codes expire, so request a fresh one if it has been a while."},
	"Nothing To Verify":          {"Nothing needs checking right now.", "Run the command again after something changes."},

	// Tickets
	"Not A Ticket":         {"Run this command inside an open ticket thread."},
	"Not Claimed":          {"Claim the ticket first with `/ticket claim`."},
	"Panel Not Found":      {"Ask staff to publish a fresh panel with `/ticket panel`.", "Then open your ticket from the new panel."},
	"Panel Outdated":       {"Ask staff to publish a fresh panel with `/ticket panel`.", "Then open your ticket from the new panel."},
	"Ticket Limit Reached": {"Continue in one of your open tickets, or close one before opening another."},
	"Ticket Not Created":   {"Give the bot **Create Private Threads** in the panel's channel.", "Then try again, or contact staff."},

	// Permissions and general failures
	"Permission Denied":        {"You need a role that is allowed to use this command.", "Ask a guild administrator to review the command's roles in the dashboard."},
	"Staff Only":               {"Ask a staff member to run this for you."},
	"Missing Permissions":      {"Grant the bot the permission named above in Server Settings.", "Then run the command again."},
	"Insufficient Permissions": {"Give the bot **Send Messages** and **Embed Links** in that channel.", "Then run the command again."},
	"Slash Command Required":   {"Use the slash version of this command.", "Type `/` and pick it from the menu."},
	"Invalid Subcommand":       {"Choose one of the subcommands from the command's option list.", "Type `/` to browse the available options."},
	"Missing Option":           {"Fill in one of the command's options.", "Mention a player or type their username."},
	"Slow Down":                {"Wait a few seconds before trying again."},
	"Too Many Attempts":        {"Wait a few minutes, then try again."},
	"Not Yet Implemented":      {"This feature is still being built.", "Watch the guild's announcements for updates."},
	"Guild Not Registered":     {"An administrator must run `/setup init` to register the guild.", "Once it is registered, every command works."},
	"Command Error":            {"Wait a moment, then run the command again.", "If it keeps failing, tell a guild administrator what you ran and when."},
	"Configuration Error":      {"A guild administrator must finish the command's setup in the dashboard under Commands.", "Commands marked **Needs setup** list what is missing."},
	"Command Disabled":         {"A guild administrator turned this command off.", "They can enable it in the dashboard under Commands."},
	"Slash Disabled":           {"This command is switched off for slash usage.", "Try the prefix version, or ask a guild administrator to enable it."},
	"Prefix Disabled":          {"This command is switched off for prefix usage.", "Use the slash version, or ask a guild administrator to enable it."},
	"Slash Commands Disabled":  {"A guild administrator switched slash commands off.", "Try the prefix version of the command instead."},
	"Prefix Commands Disabled": {"A guild administrator switched prefix commands off.", "Use the slash version of the command instead."},
	"Subcommand Disabled":      {"A guild administrator turned this subcommand off.", "Try a different subcommand, or ask them to enable it in the dashboard."},
	"Channel Not Allowed":      {"Run the command in a channel where it is allowed.", "A guild administrator sets the allowed channels in the dashboard under Commands."},
	"Plan Inactive":            {"A guild administrator can request a plan or a renewal in the dashboard.", "Settings and setup commands keep working meanwhile."},
	"Guild Disabled":           {"Ranked Bedwars has been switched off for this guild.", "Contact support if you think this is a mistake."},
	"Unexpected Error":         {"Wait a moment, then run the command again.", "If it keeps failing, tell a guild administrator what you ran and when."},
}

// guidanceFallback is the closing block of an error that has no entry of its own.
var guidanceFallback = []string{"Check the details above and try again.", "If it keeps happening, ask staff to look into it."}

// withGuidance adds the closing "How to Fix" (errors) or "Next Step" (everything else) block to a built-in answer that
// has no fields of its own. Embeds without an entry only get the block when they are errors.
func withGuidance(embed *discordgo.MessageEmbed, presetType, title string) {
	if embed == nil || len(embed.Fields) > 0 || title == "" || presetType == "transparent" || presetType == "loading" {
		return
	}
	lines, ok := answerGuidance[title]
	if !ok {
		if presetType != "error" {
			return
		}
		lines = guidanceFallback
	}
	name := "Next Step"
	if presetType == "error" || presetType == "caution" {
		name = "How to Fix"
	}
	quoted := make([]string, len(lines))
	for n, line := range lines {
		quoted[n] = "> " + line
	}
	embed.Fields = []*discordgo.MessageEmbedField{{Name: name, Value: strings.Join(quoted, "\n")}}
}

// ============================================================================
// Command Embeds
// Every answer a slash or prefix command gives lives here, grouped by the command file that sends it.
// ============================================================================

// ============================================================================
// admin/server_config.go
// ============================================================================

func RequirementMention(resourceType types.RequirementType, id string) string {
	if resourceType == types.ReqRole {
		return fmt.Sprintf("<@&%s>", id)
	}
	return fmt.Sprintf("<#%s>", id)
}

func EnabledLabel(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}

func ToggleLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func UnexpectedErrorServerConfigEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to update the configuration for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigUnknownCommandEmbed(i *discordgo.InteractionCreate, name string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unknown Command",
		Description: fmt.Sprintf("No registered command named **%s**.", name),
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigCommandNotSetUpEmbed(i *discordgo.InteractionCreate, name string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Command Not Set Up",
		Description: fmt.Sprintf("**%s** has no configuration yet. Run `/config setup` first.", name),
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigUnknownRequirementKeyEmbed(i *discordgo.InteractionCreate, cmdName, key string, slots map[string]types.RequirementSlot) *discordgo.MessageEmbed {
	valid := make([]string, 0, len(slots))
	for k := range slots {
		valid = append(valid, "`"+k+"`")
	}
	sort.Strings(valid)
	desc := fmt.Sprintf("**%s** has no requirement key **%s**.", cmdName, key)
	if len(valid) > 0 {
		desc += " Valid keys: " + strings.Join(valid, ", ")
	} else {
		desc += " This command declares no requirements of this type."
	}
	return Preset("error", types.PresetOptions{Title: "Unknown Requirement Key", Description: desc, Thumbnail: ThumbFlag})
}

func ServerConfigRequirementListEmbed(i *discordgo.InteractionCreate, resourceType types.RequirementType, cmdName string, slots map[string]types.RequirementSlot) *discordgo.MessageEmbed {
	title := fmt.Sprintf("%s Requirements · /%s", strings.ToUpper(string(resourceType[:1]))+string(resourceType[1:]), cmdName)
	if len(slots) == 0 {
		return Preset("info", types.PresetOptions{
			Title:       title,
			Description: fmt.Sprintf("**%s** declares no %s requirements.", cmdName, resourceType),
			Thumbnail:   ThumbGuild,
		})
	}

	keys := make([]string, 0, len(slots))
	for key := range slots {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	fields := make([]*discordgo.MessageEmbedField, 0, len(keys))
	for _, key := range keys {
		slot := slots[key]
		value := "`Not configured`"
		if slot.ID != "" {
			value = RequirementMention(resourceType, slot.ID)
		}
		state := "optional"
		if slot.Required {
			state = "required"
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%s (%s)", key, state),
			Value:  value,
			Inline: true,
		})
	}
	return Preset("info", types.PresetOptions{
		Title:       title,
		Description: "Configured requirement keys for this command.",
		Fields:      fields,
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigRequirementSetEmbed(i *discordgo.InteractionCreate, resourceType types.RequirementType, cmdName, key, label string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Requirement Updated",
		Description: fmt.Sprintf("Successfully updated the command requirement.\n\n> **Command:** `/%s`\n> **Key:** `%s`\n> **Type:** `%s`\n> **Value:** %s", cmdName, key, resourceType, label),
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigRequirementGetEmbed(i *discordgo.InteractionCreate, resourceType types.RequirementType, cmdName, key string, slot types.RequirementSlot) *discordgo.MessageEmbed {
	value := "`Not configured`"
	if slot.ID != "" {
		value = RequirementMention(resourceType, slot.ID)
	}
	state := "optional"
	if slot.Required {
		state = "required"
	}
	return Preset("info", types.PresetOptions{
		Title:       "Command Requirement",
		Description: fmt.Sprintf("> **Command:** `/%s`\n> **Key:** `%s`\n> **Type:** `%s`\n> **State:** `%s`\n> **Value:** %s", cmdName, key, resourceType, state, value),
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigRequirementClearedEmbed(i *discordgo.InteractionCreate, resourceType types.RequirementType, cmdName, key string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Requirement Cleared",
		Description: fmt.Sprintf("Successfully cleared the command requirement.\n\n> **Command:** `/%s`\n> **Key:** `%s`\n> **Type:** `%s`", cmdName, key, resourceType),
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigUnknownRouteEmbed(i *discordgo.InteractionCreate, group, sub string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unknown Subcommand",
		Description: fmt.Sprintf("Unknown config route `%s %s`. Use `/config` without arguments to see available options.", group, sub),
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigInvalidKeyEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Key",
		Description: "You must provide a non-empty key.",
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigMissingValueEmbed(i *discordgo.InteractionCreate, kind string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Missing Value",
		Description: fmt.Sprintf("You must provide a %s.", kind),
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigBotViewEmbed(i *discordgo.InteractionCreate, prefix string, prefixEnabled, slashEnabled bool) *discordgo.MessageEmbed {
	lines := []string{
		fmt.Sprintf("> **Prefix:** `%s` (`%s`)", prefix, EnabledLabel(prefixEnabled)),
		fmt.Sprintf("> **Slash Commands:** `%s`", EnabledLabel(slashEnabled)),
	}
	return Preset("info", types.PresetOptions{
		Title:       "Bot Settings",
		Description: strings.Join(lines, "\n"),
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigInvalidPrefixEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Prefix",
		Description: "Please provide a valid prefix of 1-5 characters.",
		Thumbnail:   ThumbFlag,
	})
}

func ServerConfigPrefixUpdatedEmbed(i *discordgo.InteractionCreate, prefix string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Prefix Updated",
		Description: fmt.Sprintf("Successfully updated the command prefix.\n\n> **Prefix:** `%s`", prefix),
		Thumbnail:   ThumbGuild,
	})
}

func ServerConfigBotToggleEmbed(i *discordgo.InteractionCreate, target string, enabled bool) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Bot Setting Updated",
		Description: fmt.Sprintf("Successfully updated the command mode.\n\n> **Mode:** `%s`\n> **State:** `%s`", target, ToggleLabel(enabled)),
		Thumbnail:   ThumbGuild,
	})
}

// ============================================================================
// admin/server_setup.go
// ============================================================================

func SetupResultLines(result *types.SetupResult) []string {
	lines := []string{}
	if result.GuildCreated {
		lines = append(lines, "> Guild database record")
	}
	if result.ConfigCreated {
		lines = append(lines, "> Guild configuration scaffold")
	}
	if result.SnapshotCreated {
		lines = append(lines, "> Guild snapshot scaffold")
	}
	if result.SnapshotSynced {
		lines = append(lines, "> Discord role and channel snapshot sync")
	}
	if result.MetaCreated {
		lines = append(lines, "> Game meta scaffold")
	}
	if result.GameConfigCreated {
		lines = append(lines, "> Game configuration scaffold")
	}
	if len(lines) == 0 {
		lines = append(lines, "> Nothing new was required; existing records were refreshed")
	}
	return lines
}

func ServerSetupInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "Use `/setup init` or `/setup sync`.",
		Thumbnail:   ThumbFlag,
	})
}

func ServerSetupFailedEmbed(i *discordgo.InteractionCreate, err error) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Guild Setup Failed",
		Description: fmt.Sprintf("Could not initialize this guild with Ranked Bedwars: %v. Verify the bot has the required permissions and try again.", err),
		Thumbnail:   ThumbFlag,
	})
}

func ServerSyncFailedEmbed(i *discordgo.InteractionCreate, err error) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Guild Sync Failed",
		Description: fmt.Sprintf("Could not sync this guild's data: %v. Verify the bot has the required permissions and try again.", err),
		Thumbnail:   ThumbFlag,
	})
}

func ServerSetupSuccessEmbed(i *discordgo.InteractionCreate, result *types.SetupResult) *discordgo.MessageEmbed {
	lines := []string{
		"This guild is now registered with Ranked Bedwars.",
		"",
		"**What was created:**",
	}
	lines = append(lines, SetupResultLines(result)...)
	lines = append(lines, "", "Use `/config` or the web dashboard to assign roles, channels, and bots.")

	return Preset("success", types.PresetOptions{
		Title:       "Guild Setup Complete",
		Description: strings.Join(lines, "\n"),
		Thumbnail:   ThumbGuild,
	})
}

func ServerSyncSuccessEmbed(i *discordgo.InteractionCreate, result *types.SetupResult) *discordgo.MessageEmbed {
	lines := []string{
		"This guild's data has been synced successfully.",
		"",
		"**What was synced:**",
	}
	lines = append(lines, SetupResultLines(result)...)
	lines = append(lines, "", "Use `/config` or the web dashboard to assign roles, channels, and bots.")

	return Preset("success", types.PresetOptions{
		Title:       "Guild Sync Complete",
		Description: strings.Join(lines, "\n"),
		Thumbnail:   ThumbGuild,
	})
}

// ============================================================================
// general/bot_status.go
// ============================================================================

func UnexpectedErrorBotsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the registered game bots for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func EmptyBotsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Registered Bots",
		Description: "This guild has no registered game bots. If you think this is an error, please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// general/player_compare.go
// ============================================================================

func UnexpectedErrorCompareEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to compare these players. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func CompareInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify an opponent to compare against. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func CompareSelfComparisonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You cannot compare a player against themselves. Select a different opponent.",
		Thumbnail:   ThumbFlag,
	})
}

func CompareBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have ranked stats. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func CompareNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// general/game_history.go
// ============================================================================

func UnexpectedErrorGameHistoryEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the game history. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func GameHistoryBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a game history. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func GameHistoryNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/register` to link a Minecraft account first.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func GameHistoryNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func GameHistoryEmptyEmbed(i *discordgo.InteractionCreate, username, filter string) *discordgo.MessageEmbed {
	description := "No game history was found for this player in the current season."
	if filter != "" {
		description = fmt.Sprintf("No game history was found for this player in the last **%s**. Try a longer filter or remove it.", filter)
	}
	return Preset("success", types.PresetOptions{
		Title:       "No Games Found",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/game_view.go
// ============================================================================

func OnOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func GameViewNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameViewNoGameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Game",
		Description: "You must either run this command in a game channel or specify a game number.",
		Thumbnail:   ThumbFlag,
	})
}

func GameViewOverviewEmbed(i *discordgo.InteractionCreate, game Game) *discordgo.MessageEmbed {
	var description strings.Builder

	// Basic info
	description.WriteString(fmt.Sprintf("**Game Number:** #%d\n", game.Number))
	description.WriteString(fmt.Sprintf("**Mode:** %s\n", game.Mode.Name))
	description.WriteString(fmt.Sprintf("**State:** %s\n", game.State))
	description.WriteString(fmt.Sprintf("**Map:** %s\n", game.Map))

	// Timing
	if !game.CreatedAt.IsZero() {
		description.WriteString(fmt.Sprintf("**Created:** %s\n", game.CreatedAt.UTC().Format("Jan 2, 2006 15:04")))
	}
	if !game.StartedAt.IsZero() {
		description.WriteString(fmt.Sprintf("**Started:** %s\n", game.StartedAt.UTC().Format("Jan 2, 2006 15:04")))
	}

	// Teams
	if len(game.Teams) > 0 {
		description.WriteString("\n**Teams:**\n")
		for _, teamName := range game.Teams {
			teamPlayers := game.TeamPlayers(teamName)
			playerList := make([]string, len(teamPlayers))
			for idx, p := range teamPlayers {
				captainMark := ""
				if p.Captain {
					captainMark = "👑 "
				}
				playerList[idx] = fmt.Sprintf("%s<@%s>", captainMark, p.UserID)
			}
			description.WriteString(fmt.Sprintf("> **%s:** %s\n", teamName, strings.Join(playerList, ", ")))
		}
	}

	// Custom game info
	if game.Custom != nil {
		description.WriteString("\n**Custom Game:**\n")
		description.WriteString(fmt.Sprintf("> **Ranked:** %s\n", OnOff(game.Custom.Ranked)))
		description.WriteString(fmt.Sprintf("> **Stats:** %s\n", OnOff(game.Custom.Stats)))
		if game.Custom.HostedBy != "" {
			description.WriteString(fmt.Sprintf("> **Host:** <@%s>\n", game.Custom.HostedBy))
		}
	}

	// Void info
	if game.State == GameVoided {
		description.WriteString("\n**Void Information:**\n")
		description.WriteString(fmt.Sprintf("> **Reason:** %s\n", game.VoidReason))
		if game.VoidedBy != "" {
			description.WriteString(fmt.Sprintf("> **Voided By:** <@%s>\n", game.VoidedBy))
		}
	}

	// Winner info
	if game.Winner != "" {
		description.WriteString(fmt.Sprintf("\n**Winner:** %s\n", game.Winner))
	}
	if game.MVP != "" {
		description.WriteString(fmt.Sprintf("**MVP:** <@%s>\n", game.MVP))
	}

	// Channels
	if game.Channels.Text != "" {
		description.WriteString("\n**Channels:**\n")
		description.WriteString(fmt.Sprintf("> **Text:** <#%s>\n", game.Channels.Text))
		if len(game.Channels.Voice) > 0 {
			for team, channelID := range game.Channels.Voice {
				description.WriteString(fmt.Sprintf("> **%s Voice:** <#%s>\n", team, channelID))
			}
		}
	}

	// Duration if in game or completed
	if !game.StartedAt.IsZero() {
		var endTime time.Time
		if game.State == GameCleanup || game.State == GameVoided {
			endTime = game.StateAt
		} else {
			endTime = time.Now()
		}
		duration := endTime.Sub(game.StartedAt)
		description.WriteString(fmt.Sprintf("\n**Duration:** %s\n", duration.Round(time.Second)))
	}

	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Game Information · #%d", game.Number),
		Description: description.String(),
		Thumbnail:   ThumbDoubleSwords,
	})
}

// ============================================================================
// general/player_leaderboard.go
// ============================================================================

func UnexpectedErrorLeaderboardEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the leaderboard for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func LeaderboardNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured.",
		Thumbnail:   ThumbFlag,
	})
}

func LeaderboardEmptyEmbed(i *discordgo.InteractionCreate, season string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("Leaderboard · %s", season),
		Description: "No players have stats for the active season yet.",
		Thumbnail:   ThumbPlayers,
	})
}

func LeaderboardEmptyPageEmbed(i *discordgo.InteractionCreate, page int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Page Empty",
		Description: fmt.Sprintf("There are no leaderboard entries on page `%d`. Try a lower page number.", page),
		Thumbnail:   ThumbFlag,
	})
}

func LeaderboardOverviewEmbed(i *discordgo.InteractionCreate, rows []sdk.LeaderboardEntry, season string, page, totalPages int) *discordgo.MessageEmbed {
	var b strings.Builder
	for _, row := range rows {
		total := row.Wins + row.Losses
		winRate := 0.0
		if total > 0 {
			winRate = float64(row.Wins) / float64(total) * 100
		}
		b.WriteString(FillLayout(GuildOf(i), LayoutLeaderboardRow, map[string]string{
			"position": fmt.Sprint(row.Position), "elo": fmt.Sprint(row.Elo), "wins": fmt.Sprint(row.Wins), "losses": fmt.Sprint(row.Losses),
			"games": fmt.Sprint(total), "win_rate": fmt.Sprintf("%.0f%%", winRate), "rank": row.Rank, "peak": fmt.Sprint(row.HighestElo),
			"kills": fmt.Sprint(row.Kills), "deaths": fmt.Sprint(row.Deaths), "final_kills": fmt.Sprint(row.FinalKills), "beds": fmt.Sprint(row.BedsDestroyed), "mvps": fmt.Sprint(row.MvpCount),
		}) + "\n")
	}

	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("Leaderboard · %s", season),
		Description: b.String(),
		FooterText:  fmt.Sprintf("Page %d/%d", page, totalPages),
		Thumbnail:   ThumbPlayers,
	})
}

// ============================================================================
// general/game_maps.go
// ============================================================================

const MaxEmbedDescriptionLen = 2000

func FormatMapDisplayLine(guildID string, mapRecord types.Map, modes []string) string {
	mapName := strings.TrimSpace(mapRecord.Name)
	if mapName == "" {
		mapName = FillLayout(guildID, LayoutMapsUnnamed, nil)
	}

	height := FillLayout(guildID, LayoutMapsNoHeight, nil)
	if mapRecord.MapHeight > 0 {
		height = fmt.Sprintf("%d", mapRecord.MapHeight)
	}

	// Each mode can have its own teams on a map; modes that share a list are shown once.
	var segments []string
	for _, mode := range modes {
		teams := mapRecord.TeamsFor(mode)
		list := FillLayout(guildID, LayoutMapsNoTeams, nil)
		if len(teams) > 0 {
			list = strings.Join(teams, FillLayout(guildID, LayoutMapsTeamSeparator, nil))
		}
		segments = append(segments, FillLayout(guildID, LayoutMapsMode, map[string]string{"mode": mode, "teams": list}))
	}
	teamsLine := FillLayout(guildID, LayoutMapsNoModes, nil)
	if len(segments) > 0 {
		teamsLine = strings.Join(segments, LayoutSeparator(guildID, LayoutMapsModeSeparator))
	}

	return FillLayout(guildID, LayoutMapsLine, map[string]string{"name": mapName, "height": height, "teams": teamsLine, "modes": strings.Join(modes, ", ")})
}

func UnexpectedErrorMapsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the registered game maps for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func EmptyMapsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Registered Maps",
		Description: "This guild has no registered game maps. If you think this is an error, please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func MapsRotationEmbed(i *discordgo.InteractionCreate, maps []types.Map, targetModes []string) *discordgo.MessageEmbed {
	lines := make([]string, 0, len(maps)+1)
	currentLength := 0

	for _, mapRecord := range maps {
		line := FormatMapDisplayLine(GuildOf(i), mapRecord, targetModes)
		if currentLength+len(line)+1 > MaxEmbedDescriptionLen {
			break
		}
		lines = append(lines, line)
		currentLength += len(line) + 1
	}

	modesLine := FillLayout(GuildOf(i), LayoutMapsFooter, map[string]string{"modes": strings.Join(targetModes, ", "), "count": fmt.Sprint(len(maps))})
	if currentLength+len(modesLine)+1 <= MaxEmbedDescriptionLen {
		lines = append(lines, modesLine)
	}

	return Preset("success", types.PresetOptions{
		Title:       "Registered Maps",
		Description: fmt.Sprintf("View the status of all (%d) registered game maps for this guild.\n\n%s", len(maps), strings.Join(lines, "\n")),
		Thumbnail:   ThumbBed,
	})
}

// ============================================================================
// general/queue_pugs.go
// ============================================================================

func PugsListUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not Yet Implemented",
		Description: "The PUGS division list requires additional infrastructure (snapshot system, role management) and is not available yet.",
		Thumbnail:   ThumbFlag,
	})
}

func PugsVoteUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not Yet Implemented",
		Description: "The PUGS voting system requires additional infrastructure (voting channels, role management) and is not available yet.",
		Thumbnail:   ThumbFlag,
	})
}

func PugsInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a real player to vote for. Select a member from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PugsInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "Use `/pugs list` or `/pugs vote`.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// general/game_ranks.go
// ============================================================================

func UnexpectedErrorRanksEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the registered ranks for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func EmptyRanksEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Configured Ranks",
		Description: "This guild has no configured rank tiers. If you think this is an error, please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func RanksOverviewEmbed(i *discordgo.InteractionCreate, content string) *discordgo.MessageEmbed {
	header := "View the status of all registered game ranks for this guild.\n\n"
	fullDescription := header + content

	if len(fullDescription) > 2000 {
		fullDescription = fullDescription[:1997] + "..."
	}

	return Preset("success", types.PresetOptions{
		Title:       "Registered Ranks",
		Description: fullDescription,
		Thumbnail:   ThumbGuild,
	})
}

// ============================================================================
// general/game_submit.go
// ============================================================================

func SubmitResultNotGameChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not a Game Channel",
		Description: "This command can only be used in a game text channel. Run it in the text channel of your match.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultNotInMatchEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in Match",
		Description: "You must be a participant in this game to submit a result.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultMissingAttachmentEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Missing Attachment",
		Description: "You must provide a screenshot of the win screen as an attachment or provide a valid proof URL.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultInvalidAttachmentEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Attachment",
		Description: "The attachment must be an image file (PNG, JPG, GIF or WebP).",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultNotInProgressEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not In Progress",
		Description: "This game is not currently in progress, so a result cannot be submitted for it.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultTooEarlyEmbed(i *discordgo.InteractionCreate, remaining time.Duration) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Too Early",
		Description: fmt.Sprintf("You must wait **%s** before submitting a result for this game.", remaining.Round(time.Second)),
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultAlreadySubmittedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Submitted",
		Description: "A result has already been submitted for this game and is waiting for staff to score it.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultNoTeamEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Team",
		Description: "You are not assigned to a team in this game, so you cannot submit a result.",
		Thumbnail:   ThumbFlag,
	})
}

func SubmitResultStaffScoringEmbed(i *discordgo.InteractionCreate, gameNumber int, team, screenshotURL string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Result Submitted for Staff Scoring",
		Description: fmt.Sprintf("Your win screenshot for game **#%d** has been submitted. Staff will review and score the match using the screenshot as proof.\n\n> **Your Team:** `%s`\n> **Proof:** %s", gameNumber, team, screenshotURL),
		Thumbnail:   ThumbSprout,
	})
}

func SubmitResultCasualEmbed(i *discordgo.InteractionCreate, gameNumber int, team, screenshotURL string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Casual Match Result Recorded",
		Description: fmt.Sprintf("Your win screenshot for game **#%d** has been recorded and scored.\n\n> **Your Team:** `%s`\n> **Proof:** %s", gameNumber, team, screenshotURL),
		Thumbnail:   ThumbSprout,
	})
}

// ============================================================================
// general/game_void.go
// ============================================================================

func VoidMatchNotGameChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not a Game Channel",
		Description: "This command can only be used in a game text channel. Run it in the text channel of the match you want to void.",
		Thumbnail:   ThumbFlag,
	})
}

func VoidMatchNotInMatchEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in Match",
		Description: "You must be a participant in this game to void it.",
		Thumbnail:   ThumbFlag,
	})
}

func VoidMatchStaffOnlyEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Staff Only",
		Description: "Only staff can void matches. Please contact a staff member for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func VoidMatchSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Game Voided",
		Description: fmt.Sprintf("Game **#%d** has been voided. No ELO or stats were recorded.", gameNumber),
		Thumbnail:   ThumbTrashCan,
	})
}

// ============================================================================
// general/player_claim.go
// ============================================================================

func UnexpectedErrorClaimEloEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while claiming your starting ELO. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func ClaimEloNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not Registered",
		Description: "You must register with `/register` before claiming ELO.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func ClaimEloNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func ClaimEloNoPerkEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Starting ELO Perk",
		Description: "None of your perks grant starting ELO. Perks come from roles, so check that you hold the role a perk requires.",
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func ClaimEloSuccessEmbed(i *discordgo.InteractionCreate, username string, c *Claim) *discordgo.MessageEmbed {
	rank := c.RankAfter
	if c.RankBefore != c.RankAfter {
		rank = fmt.Sprintf("%s → %s", c.RankBefore, c.RankAfter)
	}
	return Preset("success", types.PresetOptions{
		Title:       "Starting ELO Claimed",
		Description: fmt.Sprintf("**%s** received **%d** starting ELO.", username, c.Amount),
		Thumbnail:   MinecraftBustAvatarURL(username),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "ELO", Value: fmt.Sprintf("%d → %d", c.Before, c.After), Inline: true},
			{Name: "Rank", Value: rank, Inline: true},
			{Name: "Perks", Value: strings.Join(c.Perks, ", "), Inline: false},
		},
	})
}

// ============================================================================
// general/player_decays.go
// ============================================================================

func UnexpectedErrorDecaysEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the decay history. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func DecaysBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a decay history. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func DecaysNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/register` to link a Minecraft account first.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func DecaysNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func DecaysEmptyEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "No Decays Found",
		Description: "No ELO decay was found for this player this season. This player has stayed active.",
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func DecaysEmptyFilteredEmbed(i *discordgo.InteractionCreate, username, filter string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Decays Empty",
		Description: fmt.Sprintf("No ELO decay was found for this player in the last **%s**. Try a longer filter or remove it.", filter),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/player_nickname.go
// ============================================================================

func UnexpectedErrorNicknameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to update your nickname. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func NicknameRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before setting a nickname. Run `/register` to link your Minecraft account first.",
		Thumbnail:   ThumbFlag,
	})
}

func NicknameNoneActiveEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Nickname Set",
		Description: "It looks like you do not have an active nickname to remove. Provide a nickname with `/nickname` to assign one first.",
		Thumbnail:   ThumbFlag,
	})
}

func NicknameInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Nickname",
		Description: "That nickname format is not valid. Nicknames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func NicknameAlreadySetEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Nickname Already Set",
		Description: fmt.Sprintf("Your nickname is already set to **%s**. No further action is needed.", nickname),
		Thumbnail:   MinecraftBustAvatarURL(nickname),
	})
}

func NicknamePremiumNameEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Premium Username",
		Description: fmt.Sprintf("**%s** is a premium Minecraft username and cannot be used as a nickname. Choose a different nickname that is not a registered premium name.", nickname),
		Thumbnail:   MinecraftBustAvatarURL(nickname),
	})
}

func NicknameConflictEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Nickname Taken",
		Description: fmt.Sprintf("The nickname **%s** is already in use in this guild. Try a different nickname.", nickname),
		Thumbnail:   MinecraftBustAvatarURL(nickname),
	})
}

func NicknameAPIOutageEmbed(i *discordgo.InteractionCreate, label string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: fmt.Sprintf("The %s verification service is currently unavailable. Please try again later.", label),
		Thumbnail:   ThumbFlag,
	})
}

func NicknameRemovedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Nickname Removed",
		Description: "Your in-game nickname has been cleared. Use `/player_refresh` to refresh your displayed name.",
		Thumbnail:   ThumbLoad,
	})
}

func NicknameUpdatedEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Nickname Updated",
		Description: fmt.Sprintf(
			"Your in-game nickname has been updated to **%s**. Please wait a moment for your displayed name to update.",
			nickname,
		),
		Thumbnail: MinecraftBustAvatarURL(nickname),
	})
}

// ============================================================================
// general/player_premium.go
// ============================================================================

func PlayerPremiumUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Premium Features",
		Description: "Premium management features are currently under development. Check back soon for subscription benefits, perk overrides, and custom styling options.",
		Thumbnail:   ThumbSprout,
	})
}

// ============================================================================
// general/player_profile.go
// ============================================================================

func UnexpectedErrorPlayerProfileEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the profile. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerProfileBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have ranked profiles. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerProfileNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. They must run `/register` before a profile is available.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerProfilePrivacyEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Profile Hidden",
		Description: fmt.Sprintf("**%s** has chosen to hide their profile from other players. Only they can view their own profile.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerProfileOverviewEmbed(i *discordgo.InteractionCreate, user *discordgo.User, config *models.PlayerConfigsModel, stats *models.PlayerStatsModel, season string, showElo bool) *discordgo.MessageEmbed {
	nickname := "None"
	if config.Nickname != nil && strings.TrimSpace(*config.Nickname) != "" {
		nickname = *config.Nickname
	}

	rank := "Unranked"
	eloLine := "> **ELO:** `Hidden`"
	recordLine := "> **Record:** `No games played`"
	streakLine := "> **Win Streak:** `0`"

	if stats != nil {
		rank = stats.Rank
		if showElo {
			eloLine = fmt.Sprintf("> **ELO:** `%d` (Peak `%d`)", stats.Elo, stats.HighestElo)
		}

		total := stats.Wins + stats.Losses
		winRate := 0.0
		if total > 0 {
			winRate = float64(stats.Wins) / float64(total) * 100
		}
		recordLine = fmt.Sprintf("> **Record:** `%dW` / `%dL` (`%.1f%%` WR)", stats.Wins, stats.Losses, winRate)
		streakLine = fmt.Sprintf("> **Win Streak:** `%d` (Best `%d`)", stats.WinStreak, stats.HighestWinStreak)
	} else if showElo {
		eloLine = "> **ELO:** `N/A`"
	}

	seasonLine := "> **Season:** `None`"
	if strings.TrimSpace(season) != "" {
		seasonLine = fmt.Sprintf("> **Season:** `%s`", season)
	}

	registered := "Unknown"
	if !config.CreatedAt.IsZero() {
		registered = config.CreatedAt.UTC().Format("Jan 2, 2006")
	}

	description := strings.Join([]string{
		fmt.Sprintf("> **Discord:** <@%s>", user.ID),
		fmt.Sprintf("> **Username:** `%s`", config.Username),
		fmt.Sprintf("> **Nickname:** `%s`", nickname),
		fmt.Sprintf("> **Rank:** `%s`", rank),
		eloLine,
		recordLine,
		streakLine,
		seasonLine,
		fmt.Sprintf("> **Registered:** `%s`", registered),
	}, "\n")

	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Player Profile · %s", config.Username),
		Description: description + "\n\nUse `/stats` for a full seasonal breakdown.",
		Thumbnail:   MinecraftBustAvatarURL(config.Username),
	})
}

// ============================================================================
// general/moderation_punishments.go
// ============================================================================

func UnexpectedErrorModerationPunishmentsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the punishment history. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationPunishmentsBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a punishment history. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationPunishmentsNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/register` to link a Minecraft account first.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func ModerationPunishmentsEmptyEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "History Clear",
		Description: "No punishment history was found for this player. This player maintains a clean record.",
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func ModerationPunishmentsEmptyActiveEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "No Active Punishments",
		Description: "No active punishments were found for this player. This player maintains a clean record.",
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func ModerationPunishmentsEmptyFilteredEmbed(i *discordgo.InteractionCreate, username, filter string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "History Empty",
		Description: fmt.Sprintf("No punishment history was found for this player in the last **%s**. Try a longer filter or remove it.", filter),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/player_register.go
// ============================================================================

func UnexpectedErrorPlayerRegisterEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to process your registration. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRegisterNoRanksEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Unavailable",
		Description: "No ranks have been configured for this guild yet, so you cannot be registered. Please ask an administrator to set up at least one rank.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRegisterNoSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Unavailable",
		Description: "This guild has no active season yet, so you cannot be registered. Please ask an administrator to start one on the dashboard's Season Periods page.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRegisterConflictEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild. Try a different username or contact staff if this is your account.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerRegisterAlreadyRegisteredEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("You are already registered as **%s**. Use `/rename` to change your username.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerRegisterAPIOutageEmbed(i *discordgo.InteractionCreate, network string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: fmt.Sprintf("Could not verify that account on **%s** right now. Please try again in a few minutes.", network),
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRegisterAccountNotFoundEmbed(i *discordgo.InteractionCreate, username, network string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Account Not Found",
		Description: fmt.Sprintf("Could not find a Minecraft account named **%s** on **%s**. Double-check the spelling and try again.", username, network),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerRegisterInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRegisterSuccessEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Registration Complete",
		Description: fmt.Sprintf(
			"Your Discord account has been successfully linked. Please wait a moment for your roles and display name to update.\n\n> **Username:** `%s`",
			username,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/player_register_verify.go
// ============================================================================

func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func RegisterContactingEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Searching For Player",
		Description: fmt.Sprintf("A bot is inviting **%s** to a party on the Minecraft server. Make sure you are online and **accept the party invite**. This can take up to a minute or two.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func RegisterCodePromptEmbed(i *discordgo.InteractionCreate, username string, length int, cfg *types.InGameCode) *discordgo.MessageEmbed {
	minutes := 10
	if cfg != nil && cfg.ExpiresMinutes >= 1 && cfg.ExpiresMinutes <= 60 {
		minutes = cfg.ExpiresMinutes
	}
	return Preset("info", types.PresetOptions{
		Title:       "Check Your Party Chat",
		Description: fmt.Sprintf("A %d-character code was sent to **%s** in the Minecraft party chat. Press **Enter code** and type it in to finish registering. The code expires in %d minute%s.", length, username, minutes, Plural(minutes)),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func RegisterUseSlashEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Slash Command Required",
		Description: "This guild verifies Minecraft accounts with a code, which needs buttons. Please use `/register` instead of the prefix command.",
		Thumbnail:   ThumbFlag,
	})
}

func RegisterVerifyUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: "Account verification is not available right now. Please try again in a few minutes, or contact an administrator if it keeps happening.",
		Thumbnail:   ThumbFlag,
	})
}

func RegisterRateLimitedEmbed(i *discordgo.InteractionCreate, wait time.Duration) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Slow Down",
		Description: fmt.Sprintf("A verification code was requested very recently. Please try again in **%s**.", wait.Round(time.Second)),
		Thumbnail:   ThumbFlag,
	})
}

func RegisterNoPendingEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Nothing To Verify",
		Description: "You have no registration waiting for a code, or it expired. Run `/register` to start again.",
		Thumbnail:   ThumbFlag,
	})
}

func RegisterCodeRejectedEmbed(i *discordgo.InteractionCreate, result CodeResult, attemptsLeft int) *discordgo.MessageEmbed {
	switch result {
	case CodeWrong:
		return Preset("error", types.PresetOptions{
			Title:       "Invalid Code",
			Description: fmt.Sprintf("That code is not correct. You have **%d** attempt%s left.", attemptsLeft, Plural(attemptsLeft)),
			Thumbnail:   ThumbFlag,
		})
	case CodeInvalid:
		return Preset("error", types.PresetOptions{
			Title:       "Invalid Code",
			Description: "That is not the code format. Check the Minecraft party chat and try again.",
			Thumbnail:   ThumbFlag,
		})
	case CodeLocked:
		return Preset("error", types.PresetOptions{
			Title:       "Too Many Attempts",
			Description: "That was the last attempt. Run `/register` again to get a new code.",
			Thumbnail:   ThumbFlag,
		})
	}
	return RegisterNoPendingEmbed(i)
}

func RegisterVerifyFailedEmbed(i *discordgo.InteractionCreate, username, status string) *discordgo.MessageEmbed {
	title, description := "Verification Unavailable", "Account verification is not available right now. Please try again in a few minutes, or contact an administrator if it keeps happening."
	switch status {
	case "offline":
		title = "Join The Server First"
		description = fmt.Sprintf("**%s** does not appear to be online on the Minecraft server. Join the server, then run `/register` again.", username)
	case "not_joined":
		title = "Party Invite Not Accepted"
		description = fmt.Sprintf("A party invite was sent to **%s**, but you did not join it in time. Make sure you are on the server and accept the invite, then run `/register` again.", username)
	case "no_bot":
		title = "Verification Busy"
		description = "Every verification bot is busy right now. Please try again in a minute."
	}
	return Preset("error", types.PresetOptions{Title: title, Description: strings.TrimSpace(description), Thumbnail: ThumbFlag})
}

// ============================================================================
// general/player_rename.go
// ============================================================================

func UnexpectedErrorPlayerRenameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to update your username. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRenameRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before you can rename your linked account. Run `/register` to link your Minecraft account first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRenameProfileNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "Your guild player profile could not be found. Try running `/player_refresh` or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRenameAlreadyLinkedEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Linked",
		Description: fmt.Sprintf("Your account is already linked to **%s**. No further changes are needed.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerRenameInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRenameAPIOutageEmbed(i *discordgo.InteractionCreate, network string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: fmt.Sprintf("Could not verify that account on **%s** right now. Please try again in a few minutes.", network),
		Thumbnail:   ThumbFlag,
	})
}

func PlayerRenameAccountNotFoundEmbed(i *discordgo.InteractionCreate, username, network string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Account Not Found",
		Description: fmt.Sprintf("Could not find a Minecraft account named **%s** on **%s**. Double-check the spelling and try again.", username, network),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerRenameConflictEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild. Try a different username or contact staff if this is your account.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerRenameSuccessEmbed(i *discordgo.InteractionCreate, oldUsername, newUsername string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Username Updated",
		Description: fmt.Sprintf(
			"Your linked Minecraft username has been updated. Please wait a moment for your display name to update.\n\n> **Previous Username:** `%s`\n> **New Username:** `%s`",
			oldUsername, newUsername,
		),
		Thumbnail: MinecraftBustAvatarURL(newUsername),
	})
}

// ============================================================================
// general/moderation_screenshare.go
// ============================================================================

func ModerationScreenshareSubmittedEmbed(i *discordgo.InteractionCreate, target *discordgo.User, username, reason string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Your screenshare request has been submitted. Staff will review the request and contact you shortly.\n\n> **Player:** <@%s> (**%s**)",
		target.ID, username,
	)
	if reason != "" {
		description += fmt.Sprintf("\n> **Reason:** %s", reason)
	}

	return Preset("success", types.PresetOptions{
		Title:       "Screenshare Request Submitted",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func ModerationScreenshareDatabaseUnavailableEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to submit the screenshare request because the database service is unavailable. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationScreenshareInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to request a screenshare for. Select a member from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationScreenshareBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated application bot profiles cannot be screenshared. Target a real player instead.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// general/player_settings.go
// ============================================================================

func SettingsStateLabel(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}

func UnexpectedErrorPlayerSettingsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while updating your settings. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSettingsInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "Use `/settings view` or `/settings toggle`.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSettingsRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before you can manage your settings. Run `/register` to link your Minecraft account first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSettingsUnknownOptionEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unknown Setting",
		Description: "That is not a setting you can change. Pick one of the options listed in the command.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSettingsOverviewEmbed(i *discordgo.InteractionCreate, config *models.PlayerConfigsModel) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Player Settings · %s", config.Username),
		Description: "Your current ranked profile preferences. Use `/settings toggle` to change one.",
		Thumbnail:   MinecraftBustAvatarURL(config.Username),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "ELO Visibility", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsEloVisible)), Inline: true},
			{Name: "Stats Visibility", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsStatsVisible)), Inline: true},
			{Name: "Party Invites", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsPartyInvitesEnabled)), Inline: true},
			{Name: "Party Autowarp", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsPartyAutowarpEnabled)), Inline: true},
			{Name: "Ping On Results", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsPingOnResultsEnabled)), Inline: true},
			{Name: "Ping On Alerts", Value: fmt.Sprintf("**%s**", SettingsStateLabel(config.IsPingOnAlertsEnabled)), Inline: true},
		},
	})
}

func PlayerSettingsToggledEmbed(i *discordgo.InteractionCreate, username, label string, enabled bool) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Setting Updated",
		Description: fmt.Sprintf("Successfully updated your settings.\n\n> **Setting:** `%s`\n> **State:** `%s`", label, SettingsStateLabel(enabled)),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/player_stats.go
// ============================================================================

func UnexpectedErrorPlayerStatsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the seasonal statistics for this player. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerStatsInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a user to view stats for. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerStatsBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have stats. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerStatsNotRegisteredEmbed(i *discordgo.InteractionCreate, user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild. They must run `/register` before stats are available.", user.ID),
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerStatsPrivateEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Stats Private",
		Description: fmt.Sprintf("<@%s> has their stats set to private. Only they can view their own stats.", user.ID),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerStatsNoActiveSeasonEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: fmt.Sprintf("This guild does not have an active season configured for **%s**. Please contact an administrator for assistance.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerStatsOverviewEmbed(i *discordgo.InteractionCreate, username string, stats *models.PlayerStatsModel) *discordgo.MessageEmbed {
	totalGames := stats.Wins + stats.Losses
	winRate := 0.0
	if totalGames > 0 {
		winRate = float64(stats.Wins) / float64(totalGames) * 100
	}
	kdr := 0.0
	if stats.Deaths > 0 {
		kdr = float64(stats.Kills) / float64(stats.Deaths)
	}

	description := fmt.Sprintf(
		"Seasonal statistics for **%s**.\n\n> **ELO:** `%d`\n> **Rank:** `%s`\n> **Wins:** `%d`\n> **Losses:** `%d`\n> **Win Rate:** `%.1f%%`\n> **Kills:** `%d`\n> **Deaths:** `%d`\n> **KDR:** `%.2f`\n> **Beds Destroyed:** `%d`\n> **Final Kills:** `%d`",
		username, stats.Elo, stats.Rank, stats.Wins, stats.Losses, winRate, stats.Kills, stats.Deaths, kdr, stats.BedsDestroyed, stats.FinalKills,
	)

	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Player Stats · %s", username),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/player_sync.go
// ============================================================================

func UnexpectedErrorPlayerSyncEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while synchronizing your profile. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSyncRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before you can refresh your profile. Run `/register` to link your Minecraft account first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSyncMemberNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: "Your guild membership could not be found. Verify you are still a member of this guild and try again.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerSyncUpToDateEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Profile Synchronized",
		Description: fmt.Sprintf("Your profile for **%s** is already up to date. No further changes are needed.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerSyncSuccessEmbed(i *discordgo.InteractionCreate, username string, actions []string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Account Synchronized",
		Description: fmt.Sprintf(
			"Successfully refreshed your roles and nickname.\n\n> **Username:** `%s`\n\n%s",
			username, strings.Join(actions, "\n"),
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/queue_call.go
// ============================================================================

func UnexpectedErrorQueueCallEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to add the player to your team voice channel. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to add to your team voice channel. Select a member from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallSelfTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You cannot add yourself to your own team voice channel. Target another player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be added to voice channels. Target a real player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallNotInVoiceEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in Voice",
		Description: "You must be connected to your team voice channel to use this command. Join it and try again.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallNotInGameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in Game",
		Description: "The voice channel you are in does not belong to an active game. Join your team voice channel and try again.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallNotInMatchEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in Match",
		Description: "You are not a participant in this game, so you cannot add players to its voice channels.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallAlreadyInMatchEmbed(i *discordgo.InteractionCreate, target *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already in Match",
		Description: fmt.Sprintf("<@%s> is already a participant in this game and cannot be added again.", target.ID),
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallWrongChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Wrong Channel",
		Description: "You are not in your assigned team voice channel. Join it and try again.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallMissingPermissionsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Missing Permissions",
		Description: "The bot does not have permission to manage permissions in this voice channel. Please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueCallSuccessEmbed(i *discordgo.InteractionCreate, target *discordgo.User, gameNumber int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Voice Access Granted",
		Description: fmt.Sprintf("Successfully added the player to your team voice channel.\n\n> **Player:** <@%s>\n> **Game:** `#%d`", target.ID, gameNumber),
		Thumbnail:   ThumbPlayers,
	})
}

// ============================================================================
// general/queue_lfg.go
// ============================================================================

func UnexpectedErrorQueueLfgEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to post your looking-for-group request. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueLfgInvalidUserEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid User",
		Description: "Your user account could not be identified. Please try again later or contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueLfgNotConfiguredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "LFG Not Configured",
		Description: "The looking-for-group system is not configured in this guild. Please contact an administrator for assistance to configure the LFG channel and role settings.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueLfgQueuesPausedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Queues Paused",
		Description: "Looking-for-group posting is currently paused by an administrator. Please try again later when queues are reopened.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueLfgNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register with the ranked system to use the looking-for-group feature. Use `/register` to link your account and track your seasonal progress.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueLfgSuccessEmbed(i *discordgo.InteractionCreate, username, channelID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "LFG Posted",
		Description: fmt.Sprintf("Your looking-for-group post has been sent. Good luck finding a game!\n\n> **Player:** `%s`\n> **Channel:** <#%s>", username, channelID),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func QueueLfgPostEmbed(i *discordgo.InteractionCreate, username, userID string) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Looking for Group",
		Description: fmt.Sprintf("<@%s> (**%s**) is looking for a ranked game! React or message them to join their party.", userID, username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/queue_pick.go
// ============================================================================

func QueuePickNotGameChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not a Game Channel",
		Description: "This command can only be used in a game text channel. Run it in the text channel of the game you are drafting for.",
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickNoActiveDraftEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Draft",
		Description: "There is no active captain draft for this game. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickDraftCompleteEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Draft Complete",
		Description: "The captain draft for this game is already complete. No more players can be picked.",
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickMissingOptionEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Missing Option",
		Description: "You must provide either a player mention or a username to pick.",
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickPlayerNotFoundEmbed(i *discordgo.InteractionCreate, name string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: fmt.Sprintf("Could not find a player in the pick pool matching **%s**. Check the spelling or mention the player instead.", name),
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickInvalidSelectionEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Selection",
		Description: "The selected player is not in the available pick pool. Choose a player who has not been picked yet.",
		Thumbnail:   ThumbFlag,
	})
}

func QueuePickSuccessEmbed(i *discordgo.InteractionCreate, username, captainID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Player Picked",
		Description: fmt.Sprintf("Successfully drafted the player onto the team.\n\n> **Player:** `%s`\n> **Captain:** <@%s>", username, captainID),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/queue_status.go
// ============================================================================

func UnexpectedErrorQueueStatusEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the game or queue information. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueStatusNoGameOrQueueEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Game or Queue",
		Description: "Use this command in a game text channel to view game stats, or join a queue voice channel to check the queue status.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueStatusNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. Please contact an administrator to configure a season.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueStatusNotQueueChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not a Queue Channel",
		Description: "This voice channel is not configured as a queue channel. Join a queue voice channel to check its status.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueStatusClosedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Queues Closed",
		Description: "Ranked queues are currently **closed** by staff.\n-# Games will not start until queues are opened again.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueStatusGameStatsEmbed(i *discordgo.InteractionCreate, game Game, season string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Game Stats · #%d", game.Number),
		Description: fmt.Sprintf("> **Mode:** `%s`\n> **Season:** `%s`", game.Mode.Name, season),
		Fields:      fields,
		Thumbnail:   ThumbPlayers,
	})
}

func QueueStatusQueueEmbed(i *discordgo.InteractionCreate, modeName, description string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Queue Status · %s", modeName),
		Description: description,
		Fields:      fields,
		Thumbnail:   ThumbPlayers,
	})
}

// ============================================================================
// general/moderation_strikes.go
// ============================================================================

func UnexpectedErrorModerationStrikesEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the strike history. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationStrikesBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a strike history. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationStrikesNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/register` to link a Minecraft account first.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func ModerationStrikesEmptyEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Strikes Clear",
		Description: "No strike history was found for this player. This player maintains a clean record.",
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func ModerationStrikesEmptyFilteredEmbed(i *discordgo.InteractionCreate, username, filter string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Strikes Empty",
		Description: fmt.Sprintf("No strike history was found for this player in the last **%s**. Try a longer filter or remove it.", filter),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// general/moderation_strike_request.go
// ============================================================================

func UnexpectedErrorModerationStrikeRequestEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while submitting the strike review request. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationStrikeRequestInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to request a strike review for. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationStrikeRequestBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have strikes to review. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationStrikeRequestReasonRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Reason Required",
		Description: "You must explain why the strike should be reviewed. Include a clear explanation in the reason field.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationStrikeRequestNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can have a strike review requested.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func ModerationStrikeRequestSubmittedEmbed(i *discordgo.InteractionCreate, target *discordgo.User, username, reason string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Strike Review Requested",
		Description: fmt.Sprintf(
			"Your strike review request has been submitted. Staff will review the strike and respond on this ticket.\n\n> **Player:** <@%s> (**%s**)\n> **Reason:** %s",
			target.ID, username, reason,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/game_force_void.go
// ============================================================================

func GameForceVoidNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameForceVoidSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, reason string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Game Force Voided",
		Description: fmt.Sprintf("Game **#%d** has been force voided. This action has been logged for staff review.\n\n> **Reason:** %s", gameNumber, reason),
		Thumbnail:   ThumbTrashCan,
	})
}

// ============================================================================
// staff/game_host.go
// ============================================================================

func GameHostNotFoundEmbed(i *discordgo.InteractionCreate, number int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Hosted Game Not Found",
		Description: fmt.Sprintf("No live hosted game **#%d** was found. Use `/host list` to see the running ones.", number),
		Thumbnail:   ThumbFlag,
	})
}

func GameHostListEmbed(i *discordgo.InteractionCreate, games []Game) *discordgo.MessageEmbed {
	if len(games) == 0 {
		return Preset("info", types.PresetOptions{
			Title:       "Hosted Games",
			Description: "No hosted games are running. Start one with `/host create`.",
			Thumbnail:   ThumbDoubleSwords,
		})
	}
	lines := make([]string, len(games))
	for i, g := range games {
		sizes := make([]string, len(g.Custom.Teams))
		for t, team := range g.Custom.Teams {
			sizes[t] = strconv.Itoa(len(team))
		}
		lines[i] = fmt.Sprintf("> `#%d` · `%s` · `%s` · `%s` · ranks %s · stats %s · <@%s>", g.Number, g.Mode.Name, strings.Join(sizes, "v"), g.State, HostOnOff(g.Custom.Ranked), HostOnOff(g.Custom.Stats), g.Custom.HostedBy)
	}
	return Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Hosted Games (%d)", len(games)),
		Description: strings.Join(lines, "\n"),
		Thumbnail:   ThumbDoubleSwords,
	})
}

func GameHostViewEmbed(i *discordgo.InteractionCreate, g Game) *discordgo.MessageEmbed {
	var fields []*discordgo.MessageEmbedField
	for t, team := range g.Custom.Teams {
		value := "<@" + strings.Join(team, "> <@") + ">"
		if name := TeamName(t); g.Channels.Voice[name] != "" {
			value += "\n<#" + g.Channels.Voice[name] + ">"
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: TeamName(t), Value: value, Inline: true})
	}
	hosting := "by hand"
	if g.Bot != "" {
		hosting = "`" + g.Bot + "`"
	}
	return Preset("info", types.PresetOptions{
		Title: fmt.Sprintf("Hosted Game #%d", g.Number),
		Description: fmt.Sprintf("> **Mode:** `%s`\n> **State:** `%s`\n> **Map:** `%s`\n> **Ranks:** %s\n> **Stats:** %s\n> **Hosted:** %s\n> **By:** <@%s>",
			g.Mode.Name, g.State, g.Map, HostOnOff(g.Custom.Ranked), HostOnOff(g.Custom.Stats), hosting, g.Custom.HostedBy),
		Fields:    fields,
		Thumbnail: ThumbDoubleSwords,
	})
}

// ============================================================================
// staff/game_loss.go
// ============================================================================

func UnexpectedErrorGameLossEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while removing ELO from the player. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func GameLossInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to remove ELO from. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func GameLossBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive ELO removals. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func GameLossRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/forceregister` before removing ELO.",
		Thumbnail:   ThumbFlag,
	})
}

func GameLossNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. ELO can only be removed during an active season. Please contact an administrator to configure a season.",
		Thumbnail:   ThumbFlag,
	})
}

func GameLossSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string, lossCount, totalEloLoss, previousElo, newElo int, oldRank, newRank string, countStats bool, lossDetails []string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully removed ELO from <@%s> (**%s**). This action has been logged for staff review.\n\n> **Losses Applied:** `%d`\n> **Total ELO Loss:** `-%d`\n> **Previous ELO:** `%d`\n> **New ELO:** `%d`\n> **Previous Rank:** `%s`\n> **New Rank:** `%s`\n> **Counted Towards Stats:** `%t`",
		user.ID, username, lossCount, totalEloLoss, previousElo, newElo, oldRank, newRank, countStats,
	)

	// Add loss breakdown if there are rank changes
	if len(lossDetails) > 0 {
		description += "\n\n__**ELO Breakdown:**__\n"
		for _, detail := range lossDetails {
			description += fmt.Sprintf("> %s\n", detail)
		}
	}

	return Preset("success", types.PresetOptions{
		Title:       "ELO Removed",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_modify.go
// ============================================================================

func UnexpectedErrorPlayerModifyEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while modifying the player's statistics. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerModifyInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "You must specify a subcommand: `add`, `remove`, or `set`. Select one from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerModifyInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to modify. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerModifyBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot have their statistics modified. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerModifyUnknownStatEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unknown Statistic",
		Description: "That is not a valid statistic that can be modified. Please select a valid statistic from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerModifyRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/forceregister` before modifying their statistics.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerModifyNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. Statistics can only be modified during an active season. Please contact an administrator to configure a season.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerModifySuccessEmbed(i *discordgo.InteractionCreate, statLabel string, user *discordgo.User, username, operation string, newVal, currentVal int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Statistics Modified",
		Description: fmt.Sprintf(
			"Successfully adjusted the player's statistics. This action has been logged for staff review.\n\n> **Player:** <@%s> (**%s**)\n> **Statistic:** `%s`\n> **Operation:** `%s`\n> **Previous Value:** `%d`\n> **New Value:** `%d`",
			user.ID, username, statLabel, operation, currentVal, newVal,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/game_partial_void.go
// ============================================================================

func GamePartialVoidNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GamePartialVoidSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, loserIDs []string) *discordgo.MessageEmbed {
	mentions := make([]string, len(loserIDs))
	for idx, id := range loserIDs {
		mentions[idx] = fmt.Sprintf("<@%s>", id)
	}

	return Preset("success", types.PresetOptions{
		Title:       "Game Partially Voided",
		Description: fmt.Sprintf("Game **#%d** has been partially voided. The winning team's stats are preserved. This action has been logged for staff review.\n\n> **Affected Players:** %s", gameNumber, strings.Join(mentions, " ")),
		Thumbnail:   ThumbTrashCan,
	})
}

// ============================================================================
// staff/game_rescore.go
// ============================================================================

func GameRescoreInvalidMvpEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid MVP",
		Description: "You must specify the MVP of the game. Select a player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func GameRescoreBotMvpEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid MVP",
		Description: "Automated bot accounts cannot be the MVP. Select a real player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func GameRescoreNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameRescoreInvalidStateEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid State",
		Description: fmt.Sprintf("Game **#%d** has not finished yet. Only completed or voided games can be rescored; use `/score` for games that are still in progress.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameRescoreMvpNotInMatchEmbed(i *discordgo.InteractionCreate, mvp *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid MVP",
		Description: fmt.Sprintf("<@%s> was not a participant in this game. The MVP must be a player who took part in the match.", mvp.ID),
		Thumbnail:   ThumbFlag,
	})
}

func GameRescoreSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, winners, losers string, mvp *discordgo.User) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Game Rescored",
		Description: fmt.Sprintf("Game **#%d** has been rescored. This action has been logged for staff review.\n\n> **Winners:** %s\n> **Losers:** %s\n> **MVP:** <@%s>", gameNumber, winners, losers, mvp.ID),
		Thumbnail:   ThumbDoubleSwords,
	})
}

// ============================================================================
// staff/game_score.go
// ============================================================================

// SplitResultMentions lists the winners and losers of a scored game as mentions.
func SplitResultMentions(results []EloResult) (winners, losers string) {
	var won, lost []string
	for _, r := range results {
		if r.Win {
			won = append(won, fmt.Sprintf("<@%s>", r.UserID))
		} else {
			lost = append(lost, fmt.Sprintf("<@%s>", r.UserID))
		}
	}
	return strings.Join(won, " "), strings.Join(lost, " ")
}

func GameScoreNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameScoreSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, teamName string, results []EloResult) *discordgo.MessageEmbed {
	winners, losers := SplitResultMentions(results)

	return Preset("success", types.PresetOptions{
		Title:       "Game Scored",
		Description: fmt.Sprintf("Game **#%d** has been scored. This action has been logged for staff review.\n\n> **Winning Team:** `%s`\n> **Winners:** %s\n> **Losers:** %s", gameNumber, teamName, winners, losers),
		Thumbnail:   ThumbDoubleSwords,
	})
}

// ============================================================================
// staff/game_unvoid.go
// ============================================================================

func GameUnvoidNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameUnvoidNotVoidedEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Voided",
		Description: fmt.Sprintf("Game **#%d** has not been voided, so there is nothing to restore.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func GameUnvoidSuccessEmbed(i *discordgo.InteractionCreate, gameNumber, restored int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Game Restored",
		Description: fmt.Sprintf("Game **#%d** has been restored from its voided state. This action has been logged for staff review.\n\n> **Players Restored:** `%d`", gameNumber, restored),
		Thumbnail:   ThumbLoad,
	})
}

// ============================================================================
// staff/game_win.go
// ============================================================================

func UnexpectedErrorGameWinEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while granting ELO to the player. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func GameWinInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to grant ELO to. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func GameWinBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive ELO grants. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func GameWinRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/forceregister` before granting ELO.",
		Thumbnail:   ThumbFlag,
	})
}

func GameWinNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. ELO can only be granted during an active season. Please contact an administrator to configure a season.",
		Thumbnail:   ThumbFlag,
	})
}

func GameWinSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string, winCount, totalEloGain, previousElo, newElo int, oldRank, newRank string, countStats bool, winDetails []string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully granted ELO to <@%s> (**%s**). This action has been logged for staff review.\n\n> **Wins Granted:** `%d`\n> **Total ELO Gain:** `+%d`\n> **Previous ELO:** `%d`\n> **New ELO:** `%d`\n> **Previous Rank:** `%s`\n> **New Rank:** `%s`\n> **Counted Towards Stats:** `%t`",
		user.ID, username, winCount, totalEloGain, previousElo, newElo, oldRank, newRank, countStats,
	)

	// Add win breakdown if there are rank changes
	if len(winDetails) > 0 {
		description += "\n\n__**ELO Breakdown:**__\n"
		for _, detail := range winDetails {
			description += fmt.Sprintf("> %s\n", detail)
		}
	}

	return Preset("success", types.PresetOptions{
		Title:       "ELO Granted",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_force_nickname.go
// ============================================================================

func UnexpectedErrorPlayerForceNicknameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while force-updating the player's nickname. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to update. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/forceregister` first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameProfileNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "That player's guild profile could not be found. Run `/forcesync` or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameNoneActiveEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Nickname Set",
		Description: "That player does not have an active nickname to remove. Provide a nickname value to assign one.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Nickname",
		Description: "That nickname format is not valid. Nicknames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceNicknameAlreadySetEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Nickname Already Set",
		Description: fmt.Sprintf("That player's nickname is already set to **%s**. No further action is needed.", nickname),
		Thumbnail:   MinecraftBustAvatarURL(nickname),
	})
}

func PlayerForceNicknameConflictEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Nickname Taken",
		Description: fmt.Sprintf("The nickname **%s** is already in use in this guild. Try a different nickname.", nickname),
		Thumbnail:   MinecraftBustAvatarURL(nickname),
	})
}

func PlayerForceNicknameRemovedEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Nickname Cleared",
		Description: fmt.Sprintf("Successfully cleared the player's in-game nickname.\n\n> **User:** <@%s>\n> **Username:** `%s`", user.ID, username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceNicknameUpdatedEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username, nickname string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Nickname Force-Updated",
		Description: fmt.Sprintf(
			"Successfully updated the player's in-game nickname. This change may take a moment to propagate across all systems.\n\n> **User:** <@%s>\n> **Username:** `%s`\n> **Nickname:** `%s`",
			user.ID, username, nickname,
		),
		Thumbnail: MinecraftBustAvatarURL(nickname),
	})
}

// ============================================================================
// staff/player_force_register.go
// ============================================================================

func UnexpectedErrorPlayerForceRegisterEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing the force registration. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRegisterInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a user to register. Select a Discord member from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRegisterBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be registered. Target a real Discord member instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRegisterInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRegisterAlreadyRegisteredEmbed(i *discordgo.InteractionCreate, existingUsername string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("That user is already registered as **%s**. No further action is needed.", existingUsername),
		Thumbnail:   MinecraftBustAvatarURL(existingUsername),
	})
}

func PlayerForceRegisterNoRanksEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Ranks Configured",
		Description: "This guild has no ranks configured, so the player cannot be given a starting rank or rank role. Configure at least one rank first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRegisterConflictEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceRegisterSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Force Registration Complete",
		Description: fmt.Sprintf(
			"Successfully linked the player's account. This change may take a moment to propagate across all systems.\n\n> **User:** <@%s>\n> **Username:** `%s`",
			user.ID, username,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_force_rename.go
// ============================================================================

func UnexpectedErrorPlayerForceRenameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while force-renaming the player. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRenameInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to rename. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRenameInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRenameRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can be force-renamed.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRenameProfileNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "That player's guild profile could not be found. Run `/forcesync` or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceRenameAlreadyLinkedEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Linked",
		Description: fmt.Sprintf("That account is already linked to **%s**. No further action is needed.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceRenameConflictEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild. Try a different username.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceRenameSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, oldUsername, newUsername string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Username Force-Updated",
		Description: fmt.Sprintf(
			"Successfully updated the player's linked username. This change may take a moment to propagate across all systems.\n\n> **User:** <@%s>\n> **Previous Username:** `%s`\n> **New Username:** `%s`",
			user.ID, oldUsername, newUsername,
		),
		Thumbnail: MinecraftBustAvatarURL(newUsername),
	})
}

// ============================================================================
// staff/player_force_sync.go
// ============================================================================

func UnexpectedErrorPlayerForceSyncEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while force-synchronizing the player's profile. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceSyncInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to sync. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceSyncBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be force-synced. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceSyncRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/forceregister` first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceSyncMemberNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: "Could not find that player's guild membership to sync. Verify they are still a member of this guild.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceSyncUpToDateEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Profile Synchronized",
		Description: fmt.Sprintf("The profile for **%s** is already up to date. No further changes are needed.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceSyncSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string, actions []string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Profile Force-Synchronized",
		Description: fmt.Sprintf(
			"Successfully refreshed the player's roles and nickname. Run `/forcesync` again anytime their roles or nickname need refreshing.\n\n> **User:** <@%s>\n> **Username:** `%s`\n\n%s",
			user.ID, username, strings.Join(actions, "\n"),
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_force_transfer.go
// ============================================================================

func UnexpectedErrorPlayerForceTransferEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while transferring the account link. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify both the current linked user and the destination user.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferSameUserEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "The source and destination users must be different Discord accounts.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferSourceNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "The source user is not registered in this guild. Only registered accounts can be transferred.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferSourceProfileNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "The source user's guild profile could not be found. Run `/forcesync` or contact staff if the issue persists.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferDestinationAlreadyRegisteredEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("<@%s> is already linked to **%s**. Unregister them first or choose a different target user.", user.ID, username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerForceTransferConflictEmbed(i *discordgo.InteractionCreate, user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("<@%s> already has a linked player record, so the transfer cannot overwrite it.", user.ID),
		Thumbnail:   ThumbFlag,
	})
}

func PlayerForceTransferSuccessEmbed(i *discordgo.InteractionCreate, oldUser, newUser *discordgo.User, username string, actions []string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully transferred the linked profile. This change may take a moment to propagate across all systems.\n\n> **From:** <@%s>\n> **To:** <@%s>\n> **Username:** `%s`",
		oldUser.ID, newUser.ID, username,
	)
	if len(actions) > 0 {
		description += fmt.Sprintf("\n\n**Actions Taken:**\n%s", strings.Join(actions, "\n"))
	}

	return Preset("success", types.PresetOptions{
		Title:       "Account Transferred",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_unregister.go
// ============================================================================

func UnexpectedErrorPlayerUnregisterEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while unregistering the player. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnregisterInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to unregister. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnregisterBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be unregistered. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnregisterNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can be unregistered.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerUnregisterSuccessEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Player Unregistered",
		Description: fmt.Sprintf(
			"Successfully removed the player's registration. Their seasonal stats and profile data for this guild have been cleared.\n\n> **Player:** <@%s>\n> **Username:** `%s`",
			user.ID, username,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_unwipe.go
// ============================================================================

func UnexpectedErrorPlayerUnwipeEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while restoring the stats. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnwipeInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to unwipe. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnwipeBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have stats to restore. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerUnwipeNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can have stats restored.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerUnwipeNothingEmbed(i *discordgo.InteractionCreate, username string, wipeID int) *discordgo.MessageEmbed {
	description := fmt.Sprintf("**%s** has no wipe to undo.", username)
	if wipeID > 0 {
		description = fmt.Sprintf("**%s** has no wipe with ID **%d**.", username, wipeID)
	}
	return Preset("error", types.PresetOptions{
		Title:       "No Wipe Found",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerUnwipeAlreadyEmbed(i *discordgo.InteractionCreate, username string, wipeID uint) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Restored",
		Description: fmt.Sprintf("Wipe **%d** of **%s** was already undone.", wipeID, username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerUnwipeSuccessEmbed(i *discordgo.InteractionCreate, username string, r *sdk.WipeResult, rank string) *discordgo.MessageEmbed {
	scope := "All stats this season"
	if r.Wipe.FromGameNumber > 0 {
		scope = fmt.Sprintf("Game **#%d** onwards (**%d** games)", r.Wipe.FromGameNumber, r.Wipe.GamesAffected)
	}
	return Preset("success", types.PresetOptions{
		Title: "Stats Restored",
		Description: fmt.Sprintf(
			"Successfully undid the wipe. This action has been logged for staff review.\n\n> **Player:** `%s`\n> **Restored:** %s\n> **ELO:** `%d`\n> **Rank:** `%s`\n> **Wipe ID:** `%d`",
			username, scope, r.Stats.Elo, rank, r.Wipe.ID,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_verify.go
// ============================================================================

func UnexpectedErrorPlayerVerifyEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while verifying the player. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerVerifyInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to verify. Select a Discord member from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerVerifyBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be verified. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerVerifyNotRegisteredEmbed(i *discordgo.InteractionCreate, user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild, so they cannot queue. Register them with `/forceregister`.", user.ID),
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerVerifyReportEmbed(i *discordgo.InteractionCreate, user *discordgo.User, username string, ageDays int, whitelisted bool, restrictions []string, inGame bool, problems []string) *discordgo.MessageEmbed {
	preset, verdict := "success", "Eligible"
	if len(problems) > 0 {
		preset, verdict = "error", "Not Eligible"
	}

	age := fmt.Sprintf("**%d** days", ageDays)
	if whitelisted {
		age += " (whitelisted)"
	}
	active := "`None`"
	if len(restrictions) > 0 {
		active = "`" + strings.Join(restrictions, "`, `") + "`"
	}
	state := "`Available`"
	if inGame {
		state = "`In a game`"
	}

	description := fmt.Sprintf(
		"Eligibility report for <@%s>.\n\n> **Username:** `%s`\n> **Verdict:** `%s`\n> **Account Age:** %s\n> **Active Punishments:** %s\n> **Game State:** %s",
		user.ID, username, verdict, age, active, state,
	)
	if len(problems) > 0 {
		description += "\n\n**Blocking:**\n> " + strings.Join(problems, "\n> ")
	}

	return Preset(preset, types.PresetOptions{
		Title:       fmt.Sprintf("Player Verification · %s", username),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/player_wipe.go
// ============================================================================

func UnexpectedErrorPlayerWipeEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while wiping the stats. Please contact an administrator for assistance or try again later.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerWipeInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to wipe. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerWipeBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot have stats wiped. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerWipeNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players have seasonal stats to wipe.",
		Thumbnail:   MinecraftBustAvatarURL(""),
	})
}

func PlayerWipeNoSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. An administrator should configure a season first.",
		Thumbnail:   ThumbFlag,
	})
}

func PlayerWipeNoStatsEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Stats Found",
		Description: fmt.Sprintf("**%s** does not have stats for the active season. The player may need to play a game first.", username),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerWipeNoGamesEmbed(i *discordgo.InteractionCreate, username string, fromGame int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Games To Wipe",
		Description: fmt.Sprintf("**%s** has no scored games from game **#%d** onwards this season, so there is nothing to wipe.", username, fromGame),
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

func PlayerWipeSuccessEmbed(i *discordgo.InteractionCreate, username, season string, fromGame int, r *sdk.WipeResult, rank string) *discordgo.MessageEmbed {
	scope := "All of the player's stats this season"
	if fromGame > 0 {
		scope = fmt.Sprintf("Game **#%d** onwards (**%d** games)", fromGame, r.Wipe.GamesAffected)
	}
	return Preset("success", types.PresetOptions{
		Title: "Stats Wiped",
		Description: fmt.Sprintf(
			"Successfully wiped the player's seasonal stats. This action has been logged for staff review. Use `/unwipe` to undo it.\n\n> **Player:** `%s`\n> **Season:** `%s`\n> **Wiped:** %s\n> **ELO:** `%d` → `%d`\n> **Rank:** `%s`\n> **Wipe ID:** `%d`",
			username, season, scope, r.Wipe.EloBefore, r.Stats.Elo, rank, r.Wipe.ID,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/moderation_clear_punishment.go
// ============================================================================

func UnexpectedErrorModerationClearPunishmentEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while clearing the punishment. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationClearPunishmentInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to clear a punishment from. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationClearPunishmentBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive punishments. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationClearPunishmentNoneActiveEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Active Punishments",
		Description: "This player has no active punishments to clear. Their history can be reviewed with `/punishments`.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationClearPunishmentNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Punishment Not Found",
		Description: "No punishment with that ID exists for this player in this guild. Double-check the ID from `/punishments`.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationClearPunishmentSuccessEmbed(i *discordgo.InteractionCreate, id uint, punishType string, user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Punishment Cleared",
		Description: fmt.Sprintf(
			"Successfully removed the punishment record. This action has been logged for staff review.\n\n> **Punishment ID:** `#%d`\n> **Player:** <@%s>\n> **Type:** `%s`",
			id, user.ID, punishType,
		),
		Thumbnail: ThumbHammer,
	})
}

// ============================================================================
// staff/queue_substitute.go
// ============================================================================

func QueueSubstituteInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify both the player leaving and the substitute taking their place.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueSubstituteBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot play in games. Select real players instead.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueSubstituteSamePlayerEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "The outgoing player and the substitute must be different players.",
		Thumbnail:   ThumbFlag,
	})
}

func QueueSubstituteGameNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   ThumbFlag,
	})
}

func QueueSubstituteSuccessEmbed(i *discordgo.InteractionCreate, gameNumber int, outgoing, incoming *discordgo.User) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Substitute Assigned",
		Description: fmt.Sprintf(
			"Successfully swapped the players. This action has been logged for staff review.\n\n> **Game:** `#%d`\n> **Leaving:** <@%s>\n> **Substitute:** <@%s>",
			gameNumber, outgoing.ID, incoming.ID,
		),
		Thumbnail: ThumbPlayers,
	})
}

// ============================================================================
// staff/server_clear.go
// ============================================================================

func UnexpectedErrorClearEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while clearing messages. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func ClearMissingPermissionsEmbed(i *discordgo.InteractionCreate, channelID string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Missing Permissions",
		Description: fmt.Sprintf("I do not have permission to manage messages in <#%s>. Grant **Manage Messages** to the bot role and try again.", channelID),
		Thumbnail:   ThumbFlag,
	})
}

func ClearNoMessagesEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Messages Found",
		Description: "No messages matched your filters in that channel. Adjust the filters or clear a different channel.",
		Thumbnail:   ThumbFlag,
	})
}

func ClearPartialEmbed(i *discordgo.InteractionCreate, channelID string, deleted, skipped int) *discordgo.MessageEmbed {
	return Preset("caution", types.PresetOptions{
		Title: "Messages Partially Cleared",
		Description: fmt.Sprintf(
			"Successfully removed **%d** message(s) from <#%s>. **%d** message(s) older than 14 days were skipped. Re-run with `override_14days: True` to delete them individually.",
			deleted, channelID, skipped,
		),
		Thumbnail: ThumbTrashCan,
	})
}

func ClearSuccessEmbed(i *discordgo.InteractionCreate, channelID string, deleted int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Messages Cleared",
		Description: fmt.Sprintf("Successfully removed **%d** message(s) from <#%s>. Deleted messages cannot be recovered.", deleted, channelID),
		Thumbnail:   ThumbTrashCan,
	})
}

// ============================================================================
// staff/server_echo.go
// ============================================================================

func UnexpectedErrorEchoEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing the echo command. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func EchoSuccessEmbed(i *discordgo.InteractionCreate, channelID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Message Dispatched",
		Description: fmt.Sprintf(
			"Your message was sent successfully. The message is now visible in the target channel <#%s>.",
			channelID,
		),
		Thumbnail: ThumbGuild,
	})
}

func EchoInvalidLinkEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Link",
		Description: "That does not look like a valid Discord message link. Right-click a message and choose **Copy Message Link**.",
		Thumbnail:   ThumbFlag,
	})
}

func EchoRestrictedDestinationEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Restricted Destination",
		Description: "That message link points outside this guild. Only links from this guild can be used.",
		Thumbnail:   ThumbFlag,
	})
}

func EchoInvalidChannelEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Channel",
		Description: "The selected channel could not be used. Choose a text channel the bot can access.",
		Thumbnail:   ThumbFlag,
	})
}

func EchoInsufficientPermissionsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Insufficient Permissions",
		Description: "The bot does not have permission to send messages in that channel. Grant **Send Messages** to the bot role and try again.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// staff/server_role.go
// ============================================================================

func UnexpectedErrorRoleEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while modifying member roles. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func RoleInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "That member could not be updated. Verify the user is in this guild and try again.",
		Thumbnail:   ThumbFlag,
	})
}

func RoleMemberNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: "That member is not in this guild. They may have left or been removed.",
		Thumbnail:   ThumbFlag,
	})
}

func RoleNoMembersEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Members Found",
		Description: "You must provide at least one valid user mention, snowflake ID, or target role filter.",
		Thumbnail:   ThumbFlag,
	})
}

func RoleAlreadyAssignedEmbed(i *discordgo.InteractionCreate, user *discordgo.User, roleName string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Role Already Assigned",
		Description: fmt.Sprintf("<@%s> already has the **%s** role. No further action is needed.", user.ID, roleName),
		Thumbnail:   ThumbFlag,
	})
}

func RoleNotAssignedEmbed(i *discordgo.InteractionCreate, user *discordgo.User, roleName string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Role Not Assigned",
		Description: fmt.Sprintf("<@%s> does not currently have the **%s** role. Use `/role list` to view their current roles.", user.ID, roleName),
		Thumbnail:   ThumbFlag,
	})
}

func RoleUpdatedEmbed(i *discordgo.InteractionCreate, action string, user *discordgo.User, roleName string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Role Updated",
		Description: fmt.Sprintf(
			"Successfully updated the member's roles. Changes take effect immediately.\n\n> **Role:** `%s`\n> **Action:** %s\n> **Member:** <@%s>",
			roleName, action, user.ID,
		),
		Thumbnail: ThumbGuild,
	})
}

func RoleMassResultEmbed(i *discordgo.InteractionCreate, roleName, action string, success, skipped int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Bulk Role Update",
		Description: fmt.Sprintf(
			"Completed the bulk role operation. Skipped members may lack permissions or have hierarchy conflicts.\n\n> **Role:** `%s`\n> **Action:** %s\n> **Updated:** `%d` member(s)\n> **Skipped:** `%d`",
			roleName, action, success, skipped,
		),
		Thumbnail: ThumbGuild,
	})
}

func RoleListEmbed(i *discordgo.InteractionCreate, user *discordgo.User, roles string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("Roles · %s", user.Username),
		Description: roles + "\n\nUse `/role add` or `/role remove` to manage roles.",
		Thumbnail:   ThumbGuild,
	})
}

func RoleListEmptyEmbed(i *discordgo.InteractionCreate, user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       fmt.Sprintf("Roles · %s", user.Username),
		Description: "It looks like that member has no assignable roles. Roles may be managed by integrations or higher hierarchy.",
		Thumbnail:   ThumbFlag,
	})
}

func RoleSyncEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Role Synchronization Started",
		Description: "Queued background role synchronization for verified guild members.",
		Thumbnail:   ThumbGuild,
	})
}

func RoleHierarchyEmbed(i *discordgo.InteractionCreate, issue RoleHierarchyIssue) *discordgo.MessageEmbed {
	description := "That role cannot be managed due to hierarchy or permissions."
	switch issue {
	case RoleHierarchyManaged:
		description = "That role is managed by an integration and cannot be assigned manually."
	case RoleHierarchyDangerous:
		description = "Administrator roles cannot be assigned through this command."
	case RoleHierarchyExecutor:
		description = "Your highest role must be above the target role."
	case RoleHierarchyBot:
		description = "My highest role must be above the target role."
	}
	return Preset("error", types.PresetOptions{
		Title:       "Role Not Manageable",
		Description: description + " Adjust the role hierarchy to resolve this.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// staff/moderation_unban.go
// ============================================================================

func UnexpectedErrorModerationUnbanEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing the server unban. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationUnbanInvalidUserIDEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid User ID",
		Description: "That is not a valid Discord user ID. Provide the 17-20 digit ID of the user to unban.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationUnbanNotActiveEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Punishment Not Active",
		Description: "That player is not currently **server banned**. Verify their status with `/punishments`.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationUnbanSuccessEmbed(i *discordgo.InteractionCreate, targetID, username, reason string, unbanned bool) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully processed the server unban. This action has been logged for staff review.\n\n> **User ID:** `%s`\n> **Username:** `%s`\n> **Reason:** %s",
		targetID, username, reason,
	)
	if !unbanned {
		description += "\n\nThe Discord unban could not be applied due to missing permissions."
	}
	return Preset("success", types.PresetOptions{
		Title:       "Server Unban Applied",
		Description: description,
		Thumbnail:   ThumbHammer,
	})
}

// ============================================================================
// staff/moderation_strike.go
// ============================================================================

func UnexpectedErrorModerationStrikeEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while issuing the strike. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationStrikeInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to strike. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationStrikeBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive strikes. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationStrikeReasonRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Reason Required",
		Description: "You must provide a reason when issuing a strike. Include a clear explanation in the reason field.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationStrikeSuccessEmbed(i *discordgo.InteractionCreate, id uint, user *discordgo.User, username, strikeType, reason string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully issued a strike to the player. This action has been logged for staff review.\n\n> **Strike ID:** `#%d`\n> **Player:** <@%s> (**%s**)\n> **Reason:** %s",
		id, user.ID, username, reason,
	)
	if strikeType != "" {
		description += fmt.Sprintf("\n> **Type:** `%s`", strikeType)
	}
	return Preset("success", types.PresetOptions{
		Title:       "Strike Issued",
		Description: description,
		Thumbnail:   ThumbHammer,
	})
}

// ============================================================================
// staff/moderation_unstrike.go
// ============================================================================

func UnexpectedErrorModerationUnstrikeEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while voiding the strike. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationUnstrikeInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to remove a strike from. Select a registered player from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationUnstrikeBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive strikes. Target a registered player instead.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationUnstrikePlayerNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can receive strikes.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationUnstrikeNoStrikesEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "No Strikes Found",
		Description: "This player has no active strikes to void. Their strike history can be reviewed with `/strikes`.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationUnstrikeNotFoundEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Strike Not Found",
		Description: "No strike with that ID exists for this player in this guild. Double-check the ID from `/strikes`.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationUnstrikeAlreadyVoidedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Already Voided",
		Description: "That strike has already been voided. No further action is needed.",
		Thumbnail:   ThumbFlag,
	})
}

func ModerationUnstrikeSuccessEmbed(i *discordgo.InteractionCreate, id uint, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: "Strike Voided",
		Description: fmt.Sprintf(
			"Successfully voided the strike. This action has been logged for staff review.\n\n> **Strike ID:** `#%d`\n> **Player:** <@%s> (**%s**)",
			id, user.ID, username,
		),
		Thumbnail: MinecraftBustAvatarURL(username),
	})
}

// ============================================================================
// staff/ticket_manage.go
// ============================================================================

func TicketManageInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "That is not a valid ticket action. Choose one of `panel`, `leaderboard`, `stats`, `logs`, `claim`, `close`, `add`, or `remove`.",
		Thumbnail:   ThumbFlag,
	})
}

// ============================================================================
// Layouts: the pieces of text the lists below are built from
// ============================================================================

// Each one is a default a guild can rewrite on the dashboard (Commands → the command → Layouts). {variables} are
// filled per row; a line whose variables are all empty is left out. Add a layout here, use it with FillLayout.
var (
	// /queues in a game channel
	LayoutQueuesTeamTitle   = DefineLayout("queues.team_title", "Team heading", "👥 {team} · Avg ELO {avg_elo}", "team", "avg_elo", "players")
	LayoutQueuesPoolTitle   = DefineLayout("queues.pool_title", "Draft pool heading", "📦 Draft Pool · Avg ELO {avg_elo}", "avg_elo", "players")
	LayoutQueuesPlayer      = DefineRepeatedLayout("queues.player_row", "One player of a team or the pool", "> {mention} · ELO: **{elo}**", "mention", "id", "name", "elo", "team")
	LayoutQueuesDraftTitle  = DefineLayout("queues.draft_title", "Draft turn heading", "👑 Draft Turn")
	LayoutQueuesDraftTurn   = DefineLayout("queues.draft_turn", "Draft turn text", "It is **{captain}**'s turn to pick.", "captain", "captain_mention")
	LayoutQueuesGameDetails = DefineLayout("queues.game_details", "Game stats description", "> **Mode:** `{mode}`\n> **Season:** `{season}`", "game", "mode", "season", "players")

	// /queues in a queue voice channel
	LayoutQueuesDetails       = DefineLayout("queues.queue_details", "Queue status description", "> **Channel:** {channel}\n> **Mode:** `{mode}` (`{teams}v{per_team}`)\n> **Total Slots:** `{slots}`\n> **Eligible:** `{eligible}/{present}` in VC", "channel", "mode", "teams", "per_team", "slots", "eligible", "present")
	LayoutQueuesBlockingTitle = DefineLayout("queues.blocking_title", "Not enough players heading", "⛔ Blocking Queue")
	LayoutQueuesBlockingText  = DefineLayout("queues.blocking_text", "Not enough players text", "Not enough eligible players (`{eligible}/{slots}`)", "eligible", "slots")
	LayoutQueuesBlockedTitle  = DefineLayout("queues.blocked_title", "Blocked players heading", "⛔ Blocked Players", "count")
	LayoutQueuesBlockedRow    = DefineRepeatedLayout("queues.blocked_row", "One blocked player", "> {mention} · {reason}", "mention", "id", "reason")
	LayoutQueuesReasonInGame  = DefineLayout("queues.reason_in_game", "Blocked: already in a game", "currently in a game")
	LayoutQueuesReasonNoReg   = DefineLayout("queues.reason_unregistered", "Blocked: not registered", "not registered")
	LayoutQueuesReasonBanned  = DefineLayout("queues.reason_restricted", "Blocked: has a restriction", "has active restriction")
	LayoutQueuesReadyTitle    = DefineLayout("queues.ready_title", "Ready heading", "✅ Ready to Start")
	LayoutQueuesReadyText     = DefineLayout("queues.ready_text", "Ready text", "`{eligible}/{slots}` eligible players present", "eligible", "slots")

	// /bots
	LayoutBotsGroupTitle = DefineLayout("bots.group_title", "Status heading", "{emoji} {label} ({count})", "emoji", "label", "count", "status")
	LayoutBotsBot        = DefineRepeatedLayout("bots.bot", "One bot", "{name}{note}", "name", "tier", "status", "note")
	LayoutBotsDisabled   = DefineLayout("bots.disabled_note", "Note after a disabled bot", " · disabled")
	LayoutBotsSeparator  = DefineLayout("bots.separator", "Between bots (\\n is a new line)", ", ")
)

var (
	// /maps
	LayoutMapsIntro         = DefineLayout("maps.intro", "Text above the list", "View the status of all ({count}) registered game maps for this guild.\n\n{maps}", "count", "maps", "modes")
	LayoutMapsLine          = DefineRepeatedLayout("maps.line", "One map", "**{name}** • Max Height: `{height}`\n> {teams}", "name", "height", "teams", "modes")
	LayoutMapsMode          = DefineRepeatedLayout("maps.mode", "One mode of a map", "**{mode}** • `{teams}`", "mode", "teams")
	LayoutMapsModeSeparator = DefineLayout("maps.mode_separator", "Between a map's modes (\\n is a new line)", ", ")
	LayoutMapsTeamSeparator = DefineLayout("maps.team_separator", "Between a mode's teams", " & ")
	LayoutMapsUnnamed       = DefineLayout("maps.unnamed", "Name of a map with none", "Unnamed Map")
	LayoutMapsNoHeight      = DefineLayout("maps.no_height", "Height of a map with none", "N/A")
	LayoutMapsNoTeams       = DefineLayout("maps.no_teams", "Teams of a mode not set up", "Not configured")
	LayoutMapsNoModes       = DefineLayout("maps.no_modes", "A map with no modes", "Teams: `Not configured`")
	LayoutMapsFooter        = DefineLayout("maps.footer", "Line under the list", "*Active modes: `{modes}`*", "modes", "count")

	// /leaderboard
	LayoutLeaderboardRow = DefineRepeatedLayout("leaderboard.row", "One player's line", "> **#{position}** · `{elo}` ELO · {wins}W/{losses}L (`{win_rate}`)", "position", "elo", "rank", "peak", "wins", "losses", "games", "win_rate", "kills", "deaths", "final_kills", "beds", "mvps")
)

var (
	// /ranks
	LayoutRanksProgress    = DefineLayout("ranks.progress", "Your standing, with a rank to go", "**Your ELO:** `{elo}`\n**Your Rank:** `{rank}` **Next Rank:** `{next_rank}` (in `{next_in}` ELO)", "elo", "rank", "next_rank", "next_min_elo", "next_in")
	LayoutRanksProgressMax = DefineLayout("ranks.progress_max", "Your standing, at the top rank", "**Your ELO:** `{elo}`\n**Your Rank:** `{rank}`\nYou have achieved maximum competitive rank status!", "elo", "rank")
	LayoutRanksGuest       = DefineLayout("ranks.guest", "Shown to someone not registered", "Use `/register` to track your seasonal elo and rank progression!")
	LayoutRanksHeading     = DefineLayout("ranks.heading", "Heading above the ranks", "__**Available Ranks:**__")
	LayoutRanksLine        = DefineRepeatedLayout("ranks.line", "One rank", "{role} · ELO: `{min_elo} - {max_elo}` | Win: `+{win}` | Loss: `-{loss}`", "role", "name", "min_elo", "max_elo", "win", "loss")
	LayoutRanksNoMax       = DefineLayout("ranks.no_max", "Top of the highest rank", "∞")
	LayoutRanksUnranked    = DefineLayout("ranks.unranked", "Name when you have no rank", "Unranked")
)

// ============================================================================
// Event Embeds (parties, tickets, punishments, queues, history)
// ============================================================================

func UnexpectedErrorPartyEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing your party request. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyInvalidSubcommandEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "You must specify a valid subcommand. Select one from the command options.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You are not registered. Run `/register` to link a Minecraft account first.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyPlayerNotFoundEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "You must select a player to invite. Use the user selection menu to choose a registered player.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyBotTargetEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You cannot invite bot accounts to a party. Please select a registered player.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyTargetNotRegisteredEmbed(user *discordgo.User) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild. Ask them to run `/register` to link their Minecraft account before inviting them to a party.", user.ID),
		Thumbnail:   ThumbFlag,
	})
}

func PartyCreatedEmbed(inactivity int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Party Created",
		Description: fmt.Sprintf("Your party is ready! Invite players with `/party invite` to build your team.\n\n> **Leader:** You\n> **Members:** 1/4\n> **Invites:** Open\n> **Disbands After:** %d minutes of inactivity", inactivity),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyInviteSentEmbed(head, body string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       head,
		Description: body + "\n\nThe player can accept or decline the invitation from the party alerts channel.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyInviteSentNoAlertEmbed(head, body string) *discordgo.MessageEmbed {
	return Preset("caution", types.PresetOptions{
		Title:       head,
		Description: body + "\n\n> **Note:** I could not post the invitation to the party alerts channel. Ask the player to run `/party accept` with you as the leader to join manually.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyInvitationEmbed(targetID, leaderID string, members, maxSize int, expires time.Time) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title: "Party Invitation",
		Description: fmt.Sprintf(
			"<@%s>, <@%s> invited you to join their party!\n\n> **Party Size:** %d/%d\n> **Leader:** <@%s>\n> **Expires:** %s\n\nClick **Accept** to join or **Deny** to decline the invitation.",
			targetID, leaderID, members, maxSize, leaderID, DiscordRelativeTimestamp(expires),
		),
		Thumbnail: ThumbPlayers,
	})
}

func PartyInvitationDeclinedEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Invitation Declined",
		Description: fmt.Sprintf("<@%s> declined your party invitation. You can invite another player or ask them to request a new invitation.", userID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyDeclinedEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Invitation Declined",
		Description: "You declined the party invitation. If you change your mind, ask the leader to send you a new invitation.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyLeaderNotFoundEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "Select the party leader who invited you.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyJoinedEmbed(leaderID string, members, maxSize int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Party Joined",
		Description: fmt.Sprintf("You successfully joined <@%s>'s party!\n\n> **Party Size:** %d/%d\n> **Leader:** <@%s>\n\nYou can now queue together for matches.", leaderID, members, maxSize, leaderID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyLeftEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Left Party",
		Description: "You have successfully left the party. You can create a new party with `/party create` or join another party by accepting an invitation.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyPlayerLeftEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Player Left",
		Description: fmt.Sprintf("<@%s> has left your party. You can invite a new player to fill the slot with `/party invite`.", userID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyPlayerKickedEmbed(targetID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Player Removed",
		Description: fmt.Sprintf("<@%s> has been removed from your party. You can invite a new player to fill the slot with `/party invite`.", targetID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyLeadershipTransferredEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "You Are the Party Leader",
		Description: fmt.Sprintf("<@%s> has transferred party leadership to you. You can now manage the party, invite members, kick players, and disband the party.", userID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyLeadershipTransferredToLeaderEmbed(targetID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Leadership Transferred",
		Description: fmt.Sprintf("You have successfully transferred party leadership to <@%s>. They are now responsible for managing the party.", targetID),
		Thumbnail:   ThumbPlayers,
	})
}

func PartyDisbandedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been successfully disbanded. All members have been notified.\n\nYou can create a new party at any time with `/party create`.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyInvitesOpenedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Invites Open",
		Description: "Party invites have been reopened. Players can now be invited to your party using `/party invite`.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyInvitesClosedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Invites Closed",
		Description: "Party invites have been paused. No new players can be invited until you reopen invites with `/party invites`.",
		Thumbnail:   ThumbPlayers,
	})
}

func PartyNotInPartyEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in a Party",
		Description: "You are not currently in a party. Create a new party with `/party create` or accept a pending invitation from another player.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyTargetNotInPartyEmbed(userID string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not in a Party",
		Description: fmt.Sprintf("<@%s> is not currently in a party. They can create one with `/party create` or may have pending invitations to accept.", userID),
		Thumbnail:   ThumbFlag,
	})
}

func partyPlayerJoinedEmbed(userID string, members, maxSize int) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Player Joined",
		Description: fmt.Sprintf("<@%s> has joined your party!\n\n> **Party Size:** %d/%d\n\nYour party is now ready to queue for matches.", userID, members, maxSize),
		Thumbnail:   ThumbPlayers,
	})
}

func partyDisbandedByLeaderEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "The party leader has disbanded the party. You can create a new party with `/party create` at any time.",
		Thumbnail:   ThumbPlayers,
	})
}

func partyDisbandedLastMemberEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded because all other members have left. You can create a new party with `/party create` at any time.",
		Thumbnail:   ThumbPlayers,
	})
}

func partyDisbandedInactivityEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded due to inactivity. Parties automatically disband after a period of no activity. You can create a new party with `/party create` at any time.",
		Thumbnail:   ThumbPlayers,
	})
}

func partyDisbandedSystemOffEmbed() *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded because parties have been disabled in this guild. Contact an administrator for more information.",
		Thumbnail:   ThumbPlayers,
	})
}

func partyKickedEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Removed from Party",
		Description: "You have been removed from the party by its leader. You can create a new party with `/party create` or accept invitations from other players.",
		Thumbnail:   ThumbFlag,
	})
}

func partyLeaderChangedEmbed() *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "You Are the Party Leader",
		Description: "The previous party leader has left, so you are now the party leader. You can manage the party, invite members, kick players, and disband the party.",
		Thumbnail:   ThumbPlayers,
	})
}

func partyNotYourInvitationEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not Your Invitation",
		Description: "Only the invited player can answer this invitation.",
		Thumbnail:   ThumbFlag,
	})
}

func PartyErrorEmbed(err error) *discordgo.MessageEmbed {
	var d *EloDenial
	if errors.As(err, &d) {
		return Preset("error", types.PresetOptions{
			Title:       "Party ELO Limit",
			Description: fmt.Sprintf("This party would exceed the ELO limit: the **%s** ELO would be **%d**, above the limit of **%d**.", d.Method, d.Aggregated, d.Max),
			Thumbnail:   ThumbFlag,
		})
	}
	for _, e := range partyErrorTexts {
		if errors.Is(err, e.err) {
			return Preset("error", types.PresetOptions{Title: e.title, Description: e.text, Thumbnail: ThumbFlag})
		}
	}
	return nil
}

func PartyInfoEmbed(who string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return Preset("info", types.PresetOptions{
		Title:       "Party Information",
		Description: fmt.Sprintf("View the current party details for <@%s>.", who),
		Fields:      fields,
		Thumbnail:   ThumbPlayers,
	})
}

func punishmentSelfTargetEmbed(action string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: fmt.Sprintf("You cannot %s yourself. Select a different member to moderate.", action),
		Thumbnail:   ThumbFlag,
	})
}

func punishmentReasonRequiredEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Reason Required",
		Description: "You must provide a reason when issuing a punishment. Include a clear explanation in the reason field.",
		Thumbnail:   ThumbFlag,
	})
}

func punishmentInvalidDurationEmbed(err error) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Duration",
		Description: "The duration could not be read: " + err.Error() + ".",
		Thumbnail:   ThumbFlag,
	})
}

func punishmentHierarchyEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Hierarchy Error",
		Description: "That member cannot be moderated because their highest role is above or equal to mine. Adjust the role hierarchy or ask a higher-ranked moderator.",
		Thumbnail:   ThumbFlag,
	})
}

// PunishmentPlayerNotFoundEmbed is shown when the target has no player profile in this guild.
func PunishmentPlayerNotFoundEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "That player is not registered in this guild. They must register first before punishments can be managed.",
		Thumbnail:   ThumbFlag,
	})
}

func punishmentAlreadyActiveEmbed(adjective string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Punishment Already Active",
		Description: fmt.Sprintf("That player is already **%s**. Check `/punishments` for details.", adjective),
		Thumbnail:   ThumbFlag,
	})
}

func punishmentNotActiveEmbed(adjective string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Punishment Not Active",
		Description: fmt.Sprintf("That player is not currently **%s**. Verify their status with `/punishments`.", adjective),
		Thumbnail:   ThumbFlag,
	})
}

func queueDeniedEmbed(description string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Queue Entry Denied",
		Description: description,
		Thumbnail:   ThumbFlag,
	})
}

func queuePartyEmbed(title, description string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       title,
		Description: description,
		Thumbnail:   ThumbFlag,
	})
}

// unassignedGuildEmbed is the notice posted when the bot joins a guild that has no plan yet.
func unassignedGuildEmbed() *discordgo.MessageEmbed {
	return Preset("caution", types.PresetOptions{
		Title:       "Ranked Bedwars Is Connected",
		Description: "Ranked Bedwars has joined this guild and created its configuration. A plan is required before the bot can be used.",
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Getting Started",
				Value:  "1. An administrator opens the dashboard.\n2. Choose a plan, select this guild and request access.\n3. Once the request is approved, the bot switches on.",
				Inline: false,
			},
			{
				Name:   "Until Then",
				Value:  "Commands and buttons stay paused. The setup and configuration commands keep working, so the guild can be prepared in advance.",
				Inline: false,
			},
			{
				Name:   "Need Help?",
				Value:  "Contact support through the dashboard and include the name of this guild.",
				Inline: false,
			},
		},
		Thumbnail: ThumbGuild,
	})
}

// TicketWelcomeMessage renders the configurable welcome and ticket details.
func TicketWelcomeMessage(ticket *types.Ticket, panel *types.InteractivePanel, category *types.TicketCategory, guildName string) *discordgo.MessageEmbed {
	welcome := category.Welcome
	color := welcome.Color
	if color == 0 {
		color = defaultPanelColor
	}
	if welcome.NoColor {
		color = EmbedBlendColor
	}

	title := TicketPlaceholders(welcome.Title, ticket, panel, category, guildName)
	body := TicketPlaceholders(welcome.Body, ticket, panel, category, guildName)

	fields := []*discordgo.MessageEmbedField{}
	switch {
	case welcome.Fields != nil: // the fields the category's welcome message defines
		for _, f := range *welcome.Fields {
			name := truncate(strings.TrimSpace(TicketPlaceholders(f.Name, ticket, panel, category, guildName)), 256)
			value := truncate(strings.TrimSpace(TicketPlaceholders(f.Value, ticket, panel, category, guildName)), 1024)
			if name == "" || value == "" || len(fields) == 25 { // Discord refuses an empty field, and more than 25
				continue
			}
			fields = append(fields, &discordgo.MessageEmbedField{Name: name, Value: value, Inline: f.Inline})
		}
	case !welcome.HideDetails:
		fields = append(fields, ticketDetailFields(ticket)...)
	}

	footer := ""
	if welcome.Footer != "" {
		footer = TicketPlaceholders(welcome.Footer, ticket, panel, category, guildName)
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: body,
		Color:       color,
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: footer},
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: welcome.ThumbnailURL},
	}

	return embed
}

// TicketLogMessage renders one entry of the ticket log channel (and the
// owner's DM). The transcript, when any, is attached to the message itself.
func TicketLogMessage(color int, title, description string, lines []string) *discordgo.MessageEmbed {
	body := description
	if len(lines) > 0 {
		body += "\n\n" + strings.Join(lines, "\n")
	}
	return &discordgo.MessageEmbed{Title: title, Description: body, Color: color}
}

// TicketLeaderboardMessage ranks the staff who have claimed the most tickets.
func TicketLeaderboardMessage(rows []TicketLeaderboardRow, viewerID string) *discordgo.MessageEmbed {
	if len(rows) == 0 {
		return &discordgo.MessageEmbed{
			Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: ThumbGuild},
			Title:       "Ticket Leaderboard",
			Description: "No tickets have been claimed yet. Claim a ticket to get on the board.",
			Color:       ColorInfo,
		}
	}

	medals := []string{"🥇", "🥈", "🥉"}
	var board strings.Builder
	shown := rows
	if len(shown) > 10 {
		shown = shown[:10]
	}
	for idx, row := range shown {
		place := fmt.Sprintf("`#%d`", idx+1)
		if idx < len(medals) {
			place = medals[idx]
		}
		fmt.Fprintf(&board, "%s <@%s> - **%d** claimed, **%d** closed\n", place, row.StaffID, row.Stats.Claimed, row.Stats.Closed)
	}

	embed := &discordgo.MessageEmbed{
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: ThumbGuild},
		Title:       "Ticket Leaderboard",
		Description: "The staff members who have claimed the most tickets.\n\n" + board.String(),
		Color:       ColorDefault,
	}
	for idx, row := range rows {
		if row.StaffID == viewerID && idx >= len(shown) {
			embed.Footer = &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Your rank: #%d with %d claimed", idx+1, row.Stats.Claimed)}
			break
		}
	}
	return embed
}

// TicketStatsMessage shows one staff member's ticket numbers.
func TicketStatsMessage(staffID string, stats types.TicketStaffStats, rank, total, activeClaims int) *discordgo.MessageEmbed {
	rankText := "Unranked"
	if rank > 0 {
		rankText = fmt.Sprintf("#%d of %d", rank, total)
	}
	return &discordgo.MessageEmbed{
		Thumbnail: &discordgo.MessageEmbedThumbnail{URL: ThumbGuild},
		Title:     "Ticket Stats",
		Description: fmt.Sprintf(
			"<@%s>\n\n- **Claimed:** %d\n- **Closed:** %d\n- **Currently Claimed:** %d\n- **Leaderboard Rank:** %s",
			staffID, stats.Claimed, stats.Closed, activeClaims, rankText,
		),
		Color: ColorDefault,
	}
}

// TicketLogsMessage lists the most recently closed tickets.
func TicketLogsMessage(entries []*types.Ticket, filter string) *discordgo.MessageEmbed {
	if len(entries) == 0 {
		return &discordgo.MessageEmbed{
			Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: ThumbGuild},
			Title:       "Ticket Logs",
			Description: "No closed tickets matched your search.",
			Color:       ColorInfo,
		}
	}
	var list strings.Builder
	for _, ticket := range entries {
		claimed := ""
		if ticket.ClaimedBy != "" {
			claimed = fmt.Sprintf(" - claimed by <@%s>", ticket.ClaimedBy)
		}
		closer := "<@" + ticket.ClosedBy + ">"
		if ticket.ClosedBy == inactivityCloser {
			closer = "inactivity policy"
		}
		fmt.Fprintf(&list, "**#%s** %s - <@%s> - closed <t:%d:R> by %s%s\n", ticket.Ref, ticket.CategoryLabel, ticket.OwnerID, ticket.ClosedAt, closer, claimed)
	}
	description := list.String()
	if filter != "" {
		description = filter + "\n\n" + description
	}
	return &discordgo.MessageEmbed{Title: "Ticket Logs", Description: truncate(description, 4000), Color: ColorDefault}
}

func ticketCreatedEmbed(threadID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Ticket Created",
		Description: fmt.Sprintf("Your ticket was created: <#%s>. Continue the conversation there.", threadID),
		Thumbnail:   ThumbGuild,
	})
}

// ticketOpenErrorEmbed explains why a ticket could not be opened.
func ticketOpenErrorEmbed(err error) *discordgo.MessageEmbed {
	var limit *TicketLimitError
	switch {
	case errors.As(err, &limit):
		mentions := make([]string, len(limit.Threads))
		for idx, id := range limit.Threads {
			mentions[idx] = "<#" + id + ">"
		}
		return Preset("caution", types.PresetOptions{
			Title: "Ticket Limit Reached",
			Description: fmt.Sprintf(
				"You can only have **%d** open ticket(s) for this panel. Continue in %s or close it before opening a new one.",
				limit.Limit, strings.Join(mentions, ", "),
			),
			Thumbnail: ThumbGuild,
		})
	case errors.Is(err, ErrTicketPanelNotFound), errors.Is(err, ErrTicketCategoryNotFound):
		return Preset("error", types.PresetOptions{
			Title:       "Panel Outdated",
			Description: "This panel is no longer available. Ask staff to send a fresh one.",
			Thumbnail:   ThumbFlag,
		})
	}
	return Preset("error", types.PresetOptions{
		Title:       "Ticket Not Created",
		Description: "Your ticket could not be created. The bot may be missing permission to create private threads in this channel. Please contact staff.",
		Thumbnail:   ThumbFlag,
	})
}

func TicketBotTargetEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be part of tickets. Select a member instead.",
		Thumbnail:   ThumbFlag,
	})
}

func TicketPermissionDeniedEmbed(reason string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Permission Denied",
		Description: reason,
		Thumbnail:   ThumbFlag,
	})
}

func ticketNotInChannelEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Not A Ticket",
		Description: "This command must be run inside an open ticket thread.",
		Thumbnail:   ThumbFlag,
	})
}

func ticketPanelMissingEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Panel Not Found",
		Description: "The panel for this ticket no longer exists. Ask an administrator to check its configuration in the Ranked Bedwars Dashboard.",
		Thumbnail:   ThumbFlag,
	})
}

func TicketClaimedEmbed(staffID string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Ticket Claimed",
		Description: fmt.Sprintf("Successfully claimed this ticket.\n\n> **Claimed By:** <@%s>", staffID),
		Thumbnail:   ThumbGuild,
	})
}

// TicketAccessResultEmbed reports the outcome of /ticket add or /ticket remove.
func TicketAccessResultEmbed(ok bool, description string) *discordgo.MessageEmbed {
	kind, title := "success", "Ticket Access Updated"
	if !ok {
		kind, title = "error", "Nothing To Change"
	}
	return Preset(kind, types.PresetOptions{Title: title, Description: description, Thumbnail: ThumbGuild})
}

// TicketNoticeEmbed is the staff-only confirmation of a ticket change (rename, lock, unlock).
func TicketNoticeEmbed(title, description string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{Title: title, Description: description, Thumbnail: ThumbGuild})
}

func TicketRemoveOwnerEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "The ticket owner cannot be removed from their own ticket. Close the ticket instead.",
		Thumbnail:   ThumbFlag,
	})
}

func UnexpectedErrorTicketEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred. Please try again later or contact support.",
		Thumbnail:   ThumbFlag,
	})
}

// TicketActionErrorEmbed explains why a claim or close could not be applied.
func TicketActionErrorEmbed(err error) *discordgo.MessageEmbed {
	switch {
	case errors.Is(err, ErrTicketNotFound):
		return ticketNotInChannelEmbed()
	case errors.Is(err, ErrTicketAlreadyClaimed):
		return Preset("error", types.PresetOptions{
			Title:       "Already Claimed",
			Description: "This ticket has already been claimed. Only the staff member who claimed it can release it.",
			Thumbnail:   ThumbFlag,
		})
	case errors.Is(err, ErrTicketNotClaimed):
		return Preset("error", types.PresetOptions{
			Title:       "Not Claimed",
			Description: "This ticket has not been claimed yet. Claim it first to take ownership.",
			Thumbnail:   ThumbFlag,
		})
	case errors.Is(err, ErrTicketLockUnchanged):
		return Preset("error", types.PresetOptions{
			Title:       "Nothing To Change",
			Description: "The ticket is already in that state.",
			Thumbnail:   ThumbFlag,
		})
	case errors.Is(err, ErrTicketClaimDisabled):
		return Preset("error", types.PresetOptions{
			Title:       "Claiming Disabled",
			Description: "Claiming is turned off for this panel. An administrator can enable it under the panel settings.",
			Thumbnail:   ThumbFlag,
		})
	}
	return UnexpectedErrorTicketEmbed()
}

// BuildStrikeHistoryEmbed renders the strike history embed for one page.
func BuildStrikeHistoryEmbed(username string, entries []models.PlayerStrikesModel, page int) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** strike(s) on record.", len(entries))
	if len(entries) == 0 {
		description = "No strike history was found for this player."
	} else {
		description += "\n\n" + strings.Join(BuildStrikeHistoryDescriptionsDirect(entries, page), "\n\n")
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's Strike History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// BuildDecayHistoryEmbed renders the ELO decay history embed for one page.
func BuildDecayHistoryEmbed(username string, entries []models.PlayerDecaysModel, page int) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	totalDecayed, voided := 0, 0
	for _, entry := range entries {
		if entry.IsVoided {
			voided++
			continue
		}
		totalDecayed += entry.AmountDeducted
	}
	description := fmt.Sprintf("This player has lost **%d** ELO across **%d** decay(s) this season.", totalDecayed, len(entries)-voided)
	if voided > 0 {
		description += fmt.Sprintf(" **%d** voided decay(s) are not counted.", voided)
	}
	if len(entries) == 0 {
		description = "No decay history was found for this player."
	} else {
		description += "\n\n" + strings.Join(BuildDecayHistoryDescriptionsDirect(entries, page), "\n\n")
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's ELO Decay History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// BuildPunishmentHistoryEmbed renders the punishment history embed for one page.
func BuildPunishmentHistoryEmbed(username string, entries []models.PlayerPunishmentsModel, page int, activeOnly bool) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** punishment(s) on record.", len(entries))
	if len(entries) == 0 {
		description = "No punishment history was found for this player."
	} else {
		description += "\n\n" + strings.Join(BuildPunishmentHistoryDescriptionsDirect(entries, page), "\n\n")
	}
	titlePrefix := "Punishment History"
	if activeOnly {
		titlePrefix = "Active Punishments"
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's %s • Page %d of %d", username, titlePrefix, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

// BuildGameHistoryEmbed renders the game history embed for one page.
func BuildGameHistoryEmbed(username string, entries []sdk.GameHistory, page int) *discordgo.MessageEmbed {
	return BuildGameHistoryEmbedWithPlayers(username, entries, page, nil)
}

// BuildGameHistoryEmbedWithPlayers renders game history with the full participant lists.
func BuildGameHistoryEmbedWithPlayers(username string, entries []sdk.GameHistory, page int, players map[uint][]GameHistoryPlayer) *discordgo.MessageEmbed {
	totalPages := HistoryTotalPages(len(entries))
	description := fmt.Sprintf("This player has a total of **%d** game(s) on record for the current season.", len(entries))
	if len(entries) == 0 {
		description = "No game history was found for this player."
	} else {
		description += "\n\n" + strings.Join(buildGameHistoryDescriptions(entries, page, players), "\n\n")
	}
	return Preset("success", types.PresetOptions{
		Title:       fmt.Sprintf("%s's Game History • Page %d of %d", username, page+1, totalPages),
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
		FooterText:  HistoryPageFooter(page, totalPages, len(entries)),
	})
}

func inactiveEmbed(guildID string, disabled bool, expires *time.Time) *discordgo.MessageEmbed {
	if disabled {
		return Preset("error", types.PresetOptions{Title: "Guild Disabled", Description: "Ranked Bedwars has been disabled for this guild. Contact support if you think this is a mistake.", Thumbnail: ThumbFlag})
	}
	desc := "This guild does not have an active plan, so Ranked Bedwars is paused."
	if expires != nil {
		desc = fmt.Sprintf("This guild's plan ended on <t:%d:D>, so Ranked Bedwars is paused.", expires.Unix())
	}
	desc += " An administrator can request a plan or a renewal in the dashboard"
	if url := planPageURL(guildID); url != "" {
		desc += ": " + url
	}
	desc += ". Settings and setup commands still work meanwhile."
	return Preset("error", types.PresetOptions{Title: "Plan Inactive", Description: desc, Thumbnail: ThumbFlag})
}

// optionalAvailableEmbed tells an administrator every required slot is now
// configured and lists cmdName's remaining optional ones, pairing with
// buildOptionalPromptComponents' Continue/Skip buttons.
func optionalAvailableEmbed(cmdName string, optional []types.Requirement) *discordgo.MessageEmbed {
	description := fmt.Sprintf("All required resources for `%s` are now configured, and it's ready to use.\n\n`%s` also has these optional resources available:\n", cmdName, cmdName)
	for _, req := range optional {
		description += fmt.Sprintf("- %s: %s\n", req.Type, req.Key)
	}
	description += "\nWould you like to configure them now, or skip for now? You're able to configure these at any time using `/config`."
	return Preset("caution", types.PresetOptions{
		Title:       "Optional Configuration Available",
		Description: description,
		Thumbnail:   ThumbFlag,
	})
}

func configurationCompleteEmbed(cmdName string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Configuration Complete",
		Description: fmt.Sprintf("`%s` is ready to use. Please re-run the command. You're able to configure it further at any time using `/config`.", cmdName),
		Thumbnail:   ThumbTada,
	})
}

// PunishmentHierarchyEmbed is shown when role hierarchy blocks moderation.
func PunishmentHierarchyEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Hierarchy Error",
		Description: "That member cannot be moderated because their highest role is above or equal to mine. Adjust the role hierarchy or ask a higher-ranked moderator.",
	})
}

// InvalidTimeFilterEmbed is shown when a time filter string is invalid.
func InvalidTimeFilterEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Time Filter",
		Description: "That time filter format is not recognized. Use values like `1d`, `7d`, `2w`, or omit the filter to show all history.",
	})
}

// PunishmentExpiredLogEmbed is the line the moderation log gets when a punishment runs out.
func PunishmentExpiredLogEmbed(userID string, p models.PlayerPunishmentsModel) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "Punishment Expired",
		Description: fmt.Sprintf("> **Player:** <@%s>\n> **Type:** `%s`\n> **Reason:** %s", userID, p.Type, p.Reason),
		Timestamp:   time.Now().Format(time.RFC3339),
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: ThumbHammer},
	}
}

// PunishmentAlertEmbed is the alert a punishment that was applied, revoked or expired posts to the alerts channel.
func PunishmentAlertEmbed(action string, user *discordgo.User, username, pType, duration, reason string) *discordgo.MessageEmbed {
	title := "Punishment Applied"
	if action == "revoked" {
		title = "Punishment Revoked"
	} else if action == "expired" {
		title = "Punishment Expired"
	}

	description := fmt.Sprintf("> **Player:** <@%s> (**%s**)\n> **Type:** `%s`\n", user.ID, username, pType)
	if duration != "" && duration != "removed" {
		description += fmt.Sprintf("> **Duration:** `%s`\n", duration)
	}
	description += fmt.Sprintf("> **Reason:** %s", reason)

	return &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Timestamp:   time.Now().Format(time.RFC3339),
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: ThumbHammer},
	}
}

// DecayWarningEmbed warns a player that their ELO is about to decay.
func DecayWarningEmbed(n DecayNotice) *discordgo.MessageEmbed {
	return Preset("caution", types.PresetOptions{
		Title:       "ELO Decay Incoming",
		Description: fmt.Sprintf("You have been inactive for a while. Unless you play a ranked game before <t:%d:R>, you will lose **%d ELO** (%d → %d).", n.At.Unix(), n.Amount, n.Before, n.After),
		Thumbnail:   ThumbClock,
	})
}

// DecayAppliedEmbed tells a player their ELO decayed.
func DecayAppliedEmbed(n DecayNotice) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "ELO Decayed",
		Description: fmt.Sprintf("%d days without a ranked game cost you **%d ELO** (%d → %d). Play a ranked game to stop the decay.", n.InactivityDays, n.Amount, n.Before, n.After),
		Thumbnail:   ThumbFlag,
	})
}
func thumb(t embedTemplate, url string) embedTemplate { t.Thumbnail = &url; return t }

func colored(t embedTemplate, hex string) embedTemplate { t.Color = &hex; return t }

// UnexpectedErrorGameEmbed is the fallback for a game action that failed for an unknown reason.
func UnexpectedErrorGameEmbed(action string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: fmt.Sprintf("An unexpected error occurred while trying to %s. Please contact an administrator for assistance or try again later.", action),
	})
}

// GameActionErrorEmbed maps a GameManager error to the embed for it. action completes
// "trying to ..." in the fallback embed (e.g. "void the game").
func GameActionErrorEmbed(action string, err error) *discordgo.MessageEmbed {
	switch {
	case errors.Is(err, ErrGameNotFound):
		return gameErrorEmbed("Game Not Found", "That game could not be found. It may have already been cleaned up.")
	case errors.Is(err, ErrWrongState):
		return gameErrorEmbed("Invalid Game State", "That game is not in the right state for this action.")
	case errors.Is(err, ErrNotParticipant):
		return gameErrorEmbed("Not in Match", "That player is not a participant in this game.")
	case errors.Is(err, ErrAlreadySubmitted):
		return gameErrorEmbed("Already Submitted", "A result has already been submitted for this game.")
	case errors.Is(err, ErrUnknownTeam):
		return gameErrorEmbed("Unknown Team", "That team does not exist in this game.")
	case errors.Is(err, ErrNoSeason):
		return gameErrorEmbed("No Active Season", "This guild does not have an active season configured. Please contact an administrator to configure a season.")
	case errors.Is(err, ErrNothingToRestore):
		return gameErrorEmbed("Nothing to Restore", "That game has no recorded result to restore.")
	case errors.Is(err, ErrBusy):
		return gameErrorEmbed("Game Busy", "That game is currently being scored. Please try again in a moment.")
	case errors.Is(err, ErrAlreadyVoided):
		return gameErrorEmbed("Already Voided", "That game has already been voided.")
	case errors.Is(err, ErrNotScored):
		return gameErrorEmbed("Not Scored", "That game has not been scored yet.")
	case errors.Is(err, ErrRescoreIncomplete):
		return gameErrorEmbed("Rescore Incomplete", "The old results were reversed but the new ones could not be applied. Run the score again to finish.")
	case errors.Is(err, ErrNotYourTurn):
		return gameErrorEmbed("Not Your Turn", "It is not your turn to pick.")
	case errors.Is(err, ErrNotInPool):
		return gameErrorEmbed("Invalid Selection", "That player is not available to pick.")
	case errors.Is(err, ErrDraftOver):
		return gameErrorEmbed("Draft Complete", "The captain draft is already complete.")
	case errors.Is(err, ErrNoDraftYet):
		return gameErrorEmbed("No Active Draft", "There is no active captain draft for this game.")
	case errors.Is(err, ErrNotACaptain):
		return gameErrorEmbed("Not a Captain", "Only team captains can pick players during the draft.")
	}
	return UnexpectedErrorGameEmbed(action)
}

func gameErrorEmbed(title, description string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       title,
		Description: description,
	})
}

func HostGameErrorEmbed(err error) *discordgo.MessageEmbed {
	var busy *BusyError
	var unregistered *NotRegisteredError
	text := err.Error()
	switch {
	case errors.Is(err, ErrGameNotFound):
		text = "That game isn't running any more."
	case errors.Is(err, ErrNotCustom):
		text = "That game wasn't built with `/host`."
	case errors.Is(err, ErrBotHosted):
		text = "A bot is hosting that game, so its roster can't be changed."
	case errors.Is(err, ErrUnknownTeam):
		text = "That game doesn't have that team."
	case errors.Is(err, ErrNotParticipant):
		text = "That player isn't in the game."
	case errors.Is(err, ErrWrongState):
		text = "The game isn't in a state where that can be done."
	case errors.As(err, &busy):
		text = fmt.Sprintf("<@%s> is already in a game.", busy.UserID)
	case errors.As(err, &unregistered):
		text = "That player isn't registered."
	}
	return Preset("error", types.PresetOptions{Title: "Hosted Game", Description: text})
}

func HostGameDoneEmbed(text string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{Title: "Hosted Game", Description: text})
}

// ladderNotConfiguredEmbed tells staff what to set up on the dashboard (or that something went wrong reading it).
func ladderNotConfiguredEmbed(err error) *discordgo.MessageEmbed {
	if !IsLadderNotConfigured(err) {
		return unexpectedErrorPunishmentEmbed("punishment")
	}
	return Preset("error", types.PresetOptions{
		Title:       "Ladder Not Configured",
		Description: err.Error(),
		Thumbnail:   ThumbFlag,
	})
}

// LadderNotConfiguredEmbed is ladderNotConfiguredEmbed for commands outside this file (strikes).
func LadderNotConfiguredEmbed(err error) *discordgo.MessageEmbed {
	return ladderNotConfiguredEmbed(err)
}

func unexpectedErrorPunishmentEmbed(action string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: fmt.Sprintf("An unexpected error occurred while processing the %s. Please try again later or contact support.", action),
		Thumbnail:   ThumbFlag,
	})
}

func punishmentInvalidTargetEmbed(action string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: fmt.Sprintf("You must specify a player to %s. Select a member from the command options.", action),
		Thumbnail:   ThumbFlag,
	})
}

func punishmentBotTargetEmbed(adjective string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: fmt.Sprintf("Automated bot accounts cannot be %s. Target a registered player instead.", adjective),
		Thumbnail:   ThumbFlag,
	})
}

func punishmentMemberNotFoundEmbed(action string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: fmt.Sprintf("That user is not currently in this guild. They must be in the guild to apply a %s.", action),
		Thumbnail:   ThumbFlag,
	})
}

func punishmentAppliedEmbed(spec *ApplySpec, user *discordgo.User, username, duration, reason string, applied bool) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully recorded %s. This action has been logged for staff review.\n\n> **Player:** <@%s> (**%s**)\n",
		spec.Summary, user.ID, username,
	)
	if spec.Duration {
		description += fmt.Sprintf("> **Duration:** `%s`\n", duration)
	}
	description += fmt.Sprintf("> **Reason:** %s", reason)

	if applied && spec.AppliedNote != "" {
		description += "\n\n" + spec.AppliedNote
	} else if !applied && spec.FailedNote != "" {
		description += "\n\n" + spec.FailedNote
	}
	return Preset("success", types.PresetOptions{
		Title:       spec.Title,
		Description: description,
		Thumbnail:   ThumbHammer,
	})
}

func punishmentRevokedEmbed(action string, user *discordgo.User, reason string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: action + " Removed",
		Description: fmt.Sprintf(
			"Successfully removed the %s. This action has been logged for staff review.\n\n> **Player:** <@%s>\n> **Reason:** %s",
			strings.ToLower(action), user.ID, reason,
		),
		Thumbnail: ThumbHammer,
	})
}

// TicketContextErrorEmbed explains why a ticket could not be resolved.
func TicketContextErrorEmbed(err error) *discordgo.MessageEmbed {
	switch {
	case errors.Is(err, errNotTicketChannel):
		return ticketNotInChannelEmbed()
	case errors.Is(err, ErrTicketPanelNotFound):
		return ticketPanelMissingEmbed()
	}
	return UnexpectedErrorTicketEmbed()
}
func partyErrorEmbedOrGeneric(err error) *discordgo.MessageEmbed {
	if e := PartyErrorEmbed(err); e != nil {
		return e
	}
	apputil.DiscordLogger.Error(fmt.Sprintf("[Party] button action failed: %v", err))
	return CommandErrorEmbed()
}

type botDisplayConfig struct {
	status      string
	labelLayout string // layout keys: a guild renames a status or swaps its dot
	emojiLayout string
}

var (
	botDisplayConfigs = []botDisplayConfig{
		botStatus("Available", DotGreen),
		botStatus("Starting", DotYellow),
		botStatus("Busy", DotRed),
		botStatus("Cooldown", DotOrange),
		botStatus("Offline", DotGray),
	}
)

// botStatus defines the layouts that name a status and its dot.
func botStatus(status, emoji string) botDisplayConfig {
	key := "bots.status_" + strings.ToLower(status)
	return botDisplayConfig{
		status:      status,
		labelLayout: DefineLayout(key+"_label", status+" label", status),
		emojiLayout: DefineLayout(key+"_emoji", status+" dot", emoji),
	}
}

func BotsOverviewEmbed(guildID string, bots []types.Bot) *discordgo.MessageEmbed {
	statusGroups := make(map[string][]string, len(botDisplayConfigs))
	for _, cfg := range botDisplayConfigs {
		statusGroups[cfg.status] = []string{}
	}

	for _, bot := range bots {
		if strings.TrimSpace(bot.Username) == "" {
			continue
		}

		status, note := bot.Status, ""
		if !bot.IsEnabled {
			status = "Offline"
			note = FillLayout(guildID, LayoutBotsDisabled, nil)
		}

		// If bot is "Available", treat it as "Offline" for display
		// Only show active hosting states (Starting, Busy, Cooldown)
		if status == "Available" {
			status = "Offline"
		}
		if _, ok := statusGroups[status]; !ok {
			status = "Offline"
		}
		statusGroups[status] = append(statusGroups[status], FillLayout(guildID, LayoutBotsBot, map[string]string{
			"name": bot.Username, "tier": bot.Tier, "status": status, "note": note,
		}))
	}

	separator := LayoutSeparator(guildID, LayoutBotsSeparator)
	fields := make([]*discordgo.MessageEmbedField, 0, len(botDisplayConfigs))
	for _, cfg := range botDisplayConfigs {
		group := statusGroups[cfg.status]
		if len(group) == 0 {
			continue
		}

		value := strings.Join(group, separator)
		if len(value) > 1024 {
			value = value[:1021] + "..."
		}

		fields = append(fields, &discordgo.MessageEmbedField{
			Name: FillLayout(guildID, LayoutBotsGroupTitle, map[string]string{
				"emoji": FillLayout(guildID, cfg.emojiLayout, nil), "label": FillLayout(guildID, cfg.labelLayout, nil),
				"count": strconv.Itoa(len(group)), "status": cfg.status,
			}),
			Value:  value,
			Inline: false,
		})
	}

	return Preset("success", types.PresetOptions{
		Title:       "Registered Bots Status",
		Description: "Displays the status and uptime metrics of all registered game bots for this guild.",
		Fields:      fields,
		Thumbnail:   ThumbBot,
	})
}

var (
	ErrCompareNotRegistered = errors.New("player not registered")
	ErrCompareStatsHidden   = errors.New("player stats hidden")
)

// CompareSide is one player's resolved profile and season stats.
type CompareSide struct {
	Username string
	Stats    *models.PlayerStatsModel
	ShowElo  bool
}

// calculateKDR returns kills per death, treating a deathless record as its kill count.
func calculateKDR(kills, deaths int) float64 {
	if deaths == 0 {
		return float64(kills)
	}
	return float64(kills) / float64(deaths)
}

// CompareSideField renders one player's column of the comparison.
func CompareSideField(side *CompareSide) *discordgo.MessageEmbedField {
	elo := "Hidden"
	if side.ShowElo {
		elo = fmt.Sprintf("%d", side.Stats.Elo)
	}

	total := side.Stats.Wins + side.Stats.Losses
	winRate := 0.0
	if total > 0 {
		winRate = float64(side.Stats.Wins) / float64(total) * 100
	}

	return &discordgo.MessageEmbedField{
		Name: side.Username,
		Value: fmt.Sprintf(
			"> **ELO:** `%s`\n> **Rank:** `%s`\n> **Record:** `%dW` / `%dL`\n> **Win Rate:** `%.1f%%`\n> **KDR:** `%.2f`",
			elo, side.Stats.Rank, side.Stats.Wins, side.Stats.Losses, winRate, calculateKDR(side.Stats.Kills, side.Stats.Deaths),
		),
		Inline: true,
	}
}

// CompareSideErrorEmbed explains why one side of the comparison could not be loaded.
func CompareSideErrorEmbed(i *discordgo.InteractionCreate, err error, user *discordgo.User) *discordgo.MessageEmbed {
	switch {
	case errors.Is(err, ErrCompareNotRegistered):
		return Preset("error", types.PresetOptions{
			Title:       "Player Not Registered",
			Description: fmt.Sprintf("<@%s> is not registered in this guild. They must run `/register` before they can be compared.", user.ID),
			Thumbnail:   ThumbFlag,
		})
	case errors.Is(err, ErrCompareStatsHidden):
		return Preset("error", types.PresetOptions{
			Title:       "Stats Hidden",
			Description: fmt.Sprintf("<@%s> has chosen to hide their stats from other players, so they cannot be compared.", user.ID),
			Thumbnail:   ThumbFlag,
		})
	}
	return UnexpectedErrorCompareEmbed(i)
}

func CompareOverviewEmbed(season string, first, second *CompareSide) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Player Comparison",
		Description: fmt.Sprintf("Comparing **%s** and **%s** for season `%s`.", first.Username, second.Username, season),
		Fields:      []*discordgo.MessageEmbedField{CompareSideField(first), CompareSideField(second)},
		Thumbnail:   ThumbPlayers,
	})
}

// ClaimEloUsedEmbed explains why the grant is closed, quoting the stored claim
// record when it was already claimed.
func ClaimEloUsedEmbed(username, guildID, userID string) *discordgo.MessageEmbed {
	description := "You can only claim starting ELO before your first ranked game of the season."
	if st, err := database.Must().Stats.Get(guildID, userID, 0); err == nil && st.StartingEloClaimed {
		description = fmt.Sprintf("You already claimed **%d** ELO", st.StartingEloClaimedAmount)
		if st.StartingEloClaimedAt != nil {
			description += " " + DiscordRelativeTimestamp(*st.StartingEloClaimedAt)
		}
		if st.StartingEloClaimedPerks != "" {
			description += fmt.Sprintf(" from **%s**", st.StartingEloClaimedPerks)
		}
		description += " this season."
	}
	return Preset("error", types.PresetOptions{
		Title:       "Already Claimed",
		Description: description,
		Thumbnail:   MinecraftBustAvatarURL(username),
	})
}

// The old bot's thumbnails.
const (
	ThumbDoubleSwords = "https://i.imgur.com/YPxilj8.png"
	ThumbTimer        = "https://i.imgur.com/Ixj8JKk.png"
	ThumbTada         = "https://i.imgur.com/M5yz7dL.png"
	ThumbSprout       = "https://i.imgur.com/XHnyZh9.png"
	ThumbFlag         = "https://i.imgur.com/C2W5O9t.png"
	ThumbTrashCan     = "https://i.imgur.com/bKH6mWU.png"
)

// statusFields is how the old party embeds listed who was not invited yet, who was invited and who had joined.
var statusFields = []embedField{
	{Name: DotRed + " Waiting ({waitingCount})", Value: "{waiting}", Inline: true},
	{Name: DotYellow + " Invited ({invitedCount})", Value: "{invited}", Inline: true},
	{Name: DotGreen + " Joined ({joinedCount}/{playerCount})", Value: "{joined}", Inline: true},
}

var teamStatusField = embedField{Name: "Team {team}", Value: "{teamPlayers}", Inline: true, Each: "team"}

// mapScoreField is one field per map of the vote, best first: the map and its score, the winner marked.
var mapScoreField = embedField{Name: "{mapIcon} {mapName}{winnerTag}", Value: "`Score: {mapScore}`", Inline: true, Each: "map"}

var submitYourWin = thumb(tpl("info", "Submit Your Win",
	"When your match ends, send a **screenshot of the win screen** in this channel.\n\nYou can submit **3 minutes** after the match starts.\n\nRun **`/submit`** and attach the image, or send **`=submit`** with the screenshot attached to the same message."), ThumbSprout)

// The defaults are the old bot's embeds, word for word. Embeds the old bot never had are switched off.
var defaultEmbeds = map[string]embedTemplate{
	// Pre-game
	"introduction": func() embedTemplate {
		t := thumb(tpl("success", "Welcome to Game #{game}", "Welcome to **{flow} {mode}** {teamClause}. Defend your bed, destroy enemy beds, and fight until one team remains. Your team voice channel is ready, join it to coordinate with your team!", teamField), ThumbDoubleSwords)
		t.Ping = yes(true)
		return t
	}(),
	"teams":            thumb(tpl("success", "Drafting Complete", "Captain drafting is finished. Team voice channels are being prepared.", teamField), ThumbTada),
	"botConnecting":    off(tpl("pending", "Host Joining", "`{bot}` is joining the server to host your match.")),
	"privateGameReady": off(tpl("info", "Private Game Ready", "`{bot}` opened the private game. Party invites are next.")),
	"partyInvitation": thumb(tpl("pending", "Party Invitation",
		"The host is sending party invites now. Accept the in-game invite to join.\n\n**Not getting an invite?**\n1. Make sure your status is **Online**.\n2. Make sure **Party Invites** are **Enabled**.\n3. Make sure you are using the correct username.\n\nYou can also join manually with `/party join {bot}`.",
		append([]embedField{
			{Name: "Host", Value: "`{bot}`", Inline: true},
			{Name: "Ending", Value: "<t:{endsAt}:R>", Inline: true},
			{Name: "​", Value: "​", Inline: true},
		}, statusFields...)...), ThumbTimer),
	// The map vote and the captain draft: interactive, so they are always posted, but their wording is the guild's.
	"mapVoting": thumb(tpl("pending", "Map Voting", "Vote for the map you want to play using the menu below.\n\n**Voting ends:** <t:{endsAt}:R>", mapScoreField), ThumbTimer),
	"mapVoteEnded": thumb(tpl("success", "Map Voting Ended", "The majority of votes went to **{map}**! Please wait while the map is being selected...", mapScoreField), ThumbTada),
	"noMaps":       thumb(tpl("caution", "Map Setup", "No maps are enabled for this server right now. Contact staff if you think this is a mistake."), ThumbFlag),
	"captainPicking": func() embedTemplate {
		t := tpl("pending", "Captain Picking", "It is {captain}'s turn to pick for **{team}**. Choose a player from the menu below, or use `=pick`.\n\n**Time left:** {timeLeft}",
			embedField{Name: "{team}", Value: "{teamPlayers}", Inline: true, Each: "team"}, embedField{Name: "Remaining Players", Value: "{remaining}"})
		t.Thumbnail = str("{captainAvatar}")
		return t
	}(),
	"partyMembersAutoAssigned": thumb(tpl("info", "Party Members Joined", "{names} {wasWere} placed on **{team}** to stay with `{captain}`."), ThumbPlayers),
	"matchJoined": thumb(tpl("pending", "Match Joined", "The bot joined the private game lobby and is waiting for the Team Selector to open. Teams will be checked once it does.",
		embedField{Name: "Team TBD", Value: "{teamPlayers}", Inline: true, Each: "team"}), ThumbTimer),
	// Hosted by hand: nobody joins the server for the players, so they are told how to set the game up.
	"partySetup": thumb(tpl("pending", "Party Setup", "It is time to get everyone into a Minecraft party. **Decide who will host**, then invite the rest of the lobby.\n\nBefore inviting, make sure that:\n1. You are **on the server**.\n2. **Party invites** are set to **On**.\n3. Your status is set to **Online**.",
		embedField{Name: "Party Command", Value: "```{inviteCommand}```"}, teamField), ThumbPlayers),
	"mapSetup": thumb(tpl("pending", "Map Setup", "**{mapCount}** map(s) are in rotation for **{mode}**; the vote picked **{map}**.\n\n**Create a private game on that map**, then use the map name, max height and teams below to set up your match.",
		embedField{Name: "Map", Value: "{map}", Inline: true}, embedField{Name: "Max height", Value: "{mapHeight}", Inline: true}, embedField{Name: "Teams", Value: "{mapTeams}", Inline: true}), ThumbSprout),
	"partyReady": thumb(tpl("success", "Party Ready", "Everyone has joined the party. The match is about to begin, and the party will be warped to **{mode}** shortly.\n\nDo not leave the party.",
		append([]embedField{{Name: "Host", Value: "`{bot}`"}}, statusFields...)...), ThumbTada),
	"partyTimeout": thumb(tpl("error", "Party Timeout", "The game was **voided** because not everyone joined the party in time.\n\nPlayers who did not join were penalized automatically.",
		statusFields...), ThumbTrashCan),
	// Not in the old bot, but only posted when a step is stuck: it tells whoever set the server up what to fix.
	"hostProblem": thumb(tpl("error", "Hosting Problem", "The hosting bot got stuck on **{step}**.\n\n> **What happened:** {reason}\n\n{hint}",
		embedField{Name: "What the bot last saw", Value: "```{diagnosis}```"},
		embedField{Name: "For whoever set this server up", Value: "Lines starting with ⚠ had no rule that reacted to them. Fix it in the dashboard under Game Server → Minecraft Server → Server automations (Rules and Commands), then host again."}), ThumbFlag),
	"mapSelected": thumb(tpl("success", "Map Voting Ended", "The map selection has concluded! The winning map is **{map}**. Please wait while the game prepares the lobby.", mapScoreField), ThumbTada),
	"teamSelection": thumb(tpl("pending", "Team Selection",
		"The bot is checking that every player is on their assigned team.\n\n**What to do:**\n1. Open the **Team Selector** item.\n2. Join your assigned **Team Color**.\n\n**Ends:** <t:{endsAt}:R>",
		teamStatusField), ThumbTimer),
	"teamsVerified": thumb(tpl("success", "Teams Verified", "Every player is on their assigned team. The game is ready to start.", teamStatusField), ThumbTada),
	"teamTimeout":   thumb(tpl("error", "Team Selection Timeout", "Not every player was on their assigned team in time. Check that each player is on the correct team before starting."), ThumbFlag),

	// In-game
	"matchStarted":       thumb(tpl("success", "Match Started", "The game has started and the Team Selector is closed. The rosters below are the teams for this match.", teamStatusField), ThumbTada),
	"matchStartedManual": submitYourWin,
	"liveTracker": tpl("info", "Game #{game} Live Match Tracker",
		"Here is a real-time team rosters and elimination status for the `{mode}` match!\n\n**Time Elapsed:** <t:{startedAt}:R>",
		embedField{Name: "{team} {bed}", Value: "{teamPlayers}", Inline: true, Each: "team"}),
	"bedDestroyed":   off(tpl("caution", "Bed Destroyed", "**{team}**'s bed was destroyed by {destroyer}.")),
	"finalKill":      off(tpl("info", "Final Kill", "{killer} eliminated {victim}.")),
	"teamEliminated": off(tpl("caution", "Team Eliminated", "**{team}** has been eliminated.")),
	"phaseChanged":   off(tpl("info", "{phaseName}", "The match moved on to phase {phase}.")),
	"statTracked":    off(tpl("info", "{stat}", "{player} +{amount} ({total} total)")),

	// Post-game
	"matchEnded": off(tpl("info", "Game #{game} • Match Over", "**{winner}** won the match. The result is being scored.",
		embedField{Name: "Match stats", Value: "{statTable}"})),
	"scored":     thumb(colored(tpl("default", "Match Finished", "**Game #{game}**\n\n**Winners:** {winnerMentions}\n**Losers:** {loserMentions}"), "#dc4d4d"), ThumbTada),
	"casualDone": thumb(colored(tpl("success", "Casual Match Logged • Game #{game}", "**Result:** Tied · no team won.\n\nThe match is complete and this game room is closing."), "#57f287"), ThumbTada),
	"staffPending": thumb(colored(tpl("pending", "Win Submitted • Game #{game}",
		"**Status:** Pending staff scoring\n**Claimed winner:** {winner}\n\nThe match was moved to the review category. Staff will score it shortly."), "#fee75c"), ThumbTimer),
	"trackingLost": submitYourWin,
	"voided":       thumb(tpl("error", "Game Voided", "This game was voided and will not count towards your stats.\n\n**Reason:** {reason}"), ThumbFlag),
}
