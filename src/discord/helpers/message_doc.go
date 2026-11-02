package helpers

import (
	"encoding/json"
	"ranked-bedwars/src/discord/helpers/banner"
	"ranked-bedwars/src/types"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// V2Flag marks a message as Components V2.
const V2Flag = 1 << 15

// LinkButton is defined in package types (interactions.go).
type LinkButton = types.LinkButton

// Embed is defined in package types (interactions.go).
type Embed = types.Embed

// V2Item is defined in package types (interactions.go).
type V2Item = types.V2Item

// FieldsLayout is how a command response's fields are written into {{response.fields}}: Template is repeated for every
// field ({{field.name}}, {{field.value}}, {{field.index}}) and the repeats are joined by Separator (blank, tight or dot).
type FieldsLayout struct {
	Template  string `json:"template"`
	Separator string `json:"separator"`
}

// Render writes the fields; the zero layout is the classic "**name**\nvalue" blocks with a blank line between them.
func (l *FieldsLayout) Render(names, values []string) string {
	tpl, sep := "**{{field.name}}**\n{{field.value}}", "\n\n"
	if l != nil {
		if strings.TrimSpace(l.Template) != "" {
			tpl = l.Template
		}
		switch l.Separator {
		case "tight":
			sep = "\n"
		case "dot":
			sep = "  ·  "
		}
	}
	out := make([]string, 0, len(names))
	for i := range names {
		out = append(out, FillVars(tpl, map[string]string{"field.name": names[i], "field.value": values[i], "field.index": strconv.Itoa(i + 1)}))
	}
	return strings.Join(out, sep)
}

// MessageDoc is a saved message.
type MessageDoc struct {
	ID                    string           `json:"id"`
	Name                  string           `json:"name"`
	AllowMentions         bool             `json:"allow_mentions,omitempty"`
	AllowedMentions       *AllowedMentions `json:"allowed_mentions,omitempty"`
	SuppressEmbeds        bool             `json:"suppress_embeds,omitempty"`
	SuppressNotifications bool             `json:"suppress_notifications,omitempty"`
	Mode                  string           `json:"mode"`
	Content               string           `json:"content"`
	Embeds                []Embed          `json:"embeds"`
	Buttons               []LinkButton     `json:"buttons"`
	V2                    []V2Item         `json:"v2"`
	ResponseFields        *FieldsLayout    `json:"response_fields"`
}

type AllowedMentions struct {
	Parse       []string `json:"parse"`
	Users       []string `json:"users"`
	Roles       []string `json:"roles"`
	RepliedUser bool     `json:"replied_user"`
}

// ParseMessageDoc reads a stored message document. A document that does not parse is an error; missing fields are empty.
func ParseMessageDoc(raw json.RawMessage) (*MessageDoc, error) {
	var d MessageDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// FillVars replaces every {path} (or {{path}}) with vars[path]; unknown paths stay as written.
func FillVars(text string, vars map[string]string) string {
	if !strings.Contains(text, "{") {
		return text
	}
	return banner.VarPattern.ReplaceAllStringFunc(text, func(whole string) string {
		m := banner.VarPattern.FindStringSubmatch(whole)
		if v, ok := vars[m[1]+m[2]]; ok {
			return v
		}
		return whole
	})
}

// VarFiller returns FillVars bound to vars.
func VarFiller(vars map[string]string) func(string) string {
	return func(t string) string { return FillVars(t, vars) }
}

func colorInt(hex string) (int, bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, false
	}
	n, err := strconv.ParseInt(hex[1:], 16, 32)
	return int(n), err == nil
}

func link(b LinkButton, f func(string) string) map[string]any {
	out := map[string]any{"type": 2, "style": 5, "label": f(b.Label), "url": f(b.URL)}
	if b.DiscordID != 0 {
		out["id"] = b.DiscordID
	}
	if b.Emoji != "" {
		out["emoji"] = map[string]any{"name": b.Emoji}
	}
	if b.Disabled {
		out["disabled"] = true
	}
	return out
}

func componentID(payload map[string]any, id uint32) map[string]any {
	if id != 0 {
		payload["id"] = id
	}
	return payload
}

func leaf(l V2Item, f func(string) string) map[string]any {
	switch l.Type {
	case "section":
		if l.Accessory.Kind == "thumbnail" || l.Accessory.Kind == "button" {
			texts := make([]map[string]any, 0, len(l.Texts))
			for _, t := range l.Texts {
				texts = append(texts, map[string]any{"type": 10, "content": f(t)})
			}
			acc := link(LinkButton{Label: l.Accessory.Label, URL: l.Accessory.URL, DiscordID: l.Accessory.DiscordID, Emoji: l.Accessory.Emoji, Disabled: l.Accessory.Disabled}, f)
			if l.Accessory.Kind == "thumbnail" {
				acc = componentID(map[string]any{"type": 11, "media": map[string]any{"url": f(l.Accessory.URL)}}, l.Accessory.DiscordID)
				if l.Accessory.Description != "" {
					acc["description"] = f(l.Accessory.Description)
				}
				if l.Accessory.Spoiler {
					acc["spoiler"] = true
				}
			}
			return componentID(map[string]any{"type": 9, "components": texts, "accessory": acc}, l.DiscordID)
		}
		// Discord requires an accessory on a section: without one it is plain text.
		parts := make([]string, 0, len(l.Texts))
		for _, t := range l.Texts {
			parts = append(parts, f(t))
		}
		return componentID(map[string]any{"type": 10, "content": strings.Join(parts, "\n")}, l.DiscordID)
	case "gallery":
		items := make([]map[string]any, 0, len(l.Items))
		for _, it := range l.Items {
			item := map[string]any{"media": map[string]any{"url": f(it.URL)}}
			if it.DiscordID != 0 {
				item["id"] = it.DiscordID
			}
			if it.Description != "" {
				item["description"] = f(it.Description)
			}
			if it.Spoiler {
				item["spoiler"] = true
			}
			items = append(items, item)
		}
		return componentID(map[string]any{"type": 12, "items": items}, l.DiscordID)
	case "separator":
		spacing := 1
		if l.Spacing == 2 {
			spacing = 2
		}
		return componentID(map[string]any{"type": 14, "divider": l.Divider == nil || *l.Divider, "spacing": spacing}, l.DiscordID)
	case "buttons":
		row := make([]map[string]any, 0, len(l.Buttons))
		for _, b := range l.Buttons {
			row = append(row, link(b, f))
		}
		return componentID(map[string]any{"type": 1, "components": row}, l.DiscordID)
	default:
		return componentID(map[string]any{"type": 10, "content": f(l.Content)}, l.DiscordID)
	}
}

// Payload is the JSON body Discord takes for a message, with {{variables}} filled in by f. Pings are off unless the
// document allows mentions.
func (d *MessageDoc) Payload(f func(string) string) map[string]any {
	body := map[string]any{"allowed_mentions": d.allowedMentionsPayload()}
	flags := 0
	if d.SuppressEmbeds {
		flags |= 1 << 2
	}
	if d.SuppressNotifications {
		flags |= 1 << 12
	}
	if d.Mode == "v2" {
		flags |= V2Flag
		comps := make([]map[string]any, 0, len(d.V2))
		for _, it := range d.V2 {
			if it.Type != "container" {
				comps = append(comps, leaf(it, f))
				continue
			}
			children := make([]map[string]any, 0, len(it.Children))
			for _, c := range it.Children {
				children = append(children, leaf(c, f))
			}
			c := componentID(map[string]any{"type": 17, "spoiler": it.Spoiler, "components": children}, it.DiscordID)
			if n, ok := colorInt(it.Accent); ok {
				c["accent_color"] = n
			}
			comps = append(comps, c)
		}
		body["components"] = comps
		body["flags"] = flags
		return body
	}
	if flags != 0 {
		body["flags"] = flags
	}
	if d.Content != "" {
		body["content"] = f(d.Content)
	}
	embeds := make([]map[string]any, 0, len(d.Embeds))
	for _, e := range d.Embeds {
		m := map[string]any{}
		set := func(k, v string) {
			if v != "" {
				m[k] = v
			}
		}
		set("title", f(e.Title))
		set("url", e.URL)
		set("description", f(e.Description))
		if e.Blend {
			m["color"] = EmbedBlendColor
		} else if n, ok := colorInt(e.Color); ok {
			m["color"] = n
		}
		// Each part is filled first and left out when it comes out empty: a variable like {response.thumbnail} is empty on an
		// answer that has none, and Discord rejects an empty name or address.
		if name := f(e.Author.Name); name != "" {
			a := map[string]any{"name": name}
			if e.Author.URL != "" {
				a["url"] = e.Author.URL
			}
			if e.Author.IconURL != "" {
				a["icon_url"] = f(e.Author.IconURL)
			}
			m["author"] = a
		}
		if text := f(e.Footer.Text); text != "" {
			ft := map[string]any{"text": text}
			if e.Footer.IconURL != "" {
				ft["icon_url"] = f(e.Footer.IconURL)
			}
			m["footer"] = ft
		}
		if img := f(e.Image); img != "" {
			m["image"] = map[string]any{"url": img}
		}
		if thumb := f(e.Thumbnail); thumb != "" {
			m["thumbnail"] = map[string]any{"url": thumb}
		}
		if e.Timestamp {
			m["timestamp"] = time.Now().UTC().Format(time.RFC3339)
		}
		if len(e.Fields) > 0 {
			fields := make([]map[string]any, 0, len(e.Fields))
			for _, x := range e.Fields {
				fields = append(fields, map[string]any{"name": f(x.Name), "value": f(x.Value), "inline": x.Inline})
			}
			m["fields"] = fields
		}
		embeds = append(embeds, m)
	}
	body["embeds"] = embeds
	if len(d.Buttons) > 0 {
		row := make([]map[string]any, 0, len(d.Buttons))
		for _, b := range d.Buttons {
			row = append(row, link(b, f))
		}
		body["components"] = []map[string]any{{"type": 1, "components": row}}
	}
	return body
}

func (d *MessageDoc) allowedMentionsPayload() map[string]any {
	if d.AllowedMentions == nil {
		parse := []string{}
		if d.AllowMentions {
			parse = []string{"users", "roles"}
		}
		return map[string]any{"parse": parse}
	}

	users := ValidMentionIDs(d.AllowedMentions.Users)
	roles := ValidMentionIDs(d.AllowedMentions.Roles)
	parse := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, kind := range d.AllowedMentions.Parse {
		if kind != "everyone" && kind != "users" && kind != "roles" {
			continue
		}
		if (kind == "users" && len(users) > 0) || (kind == "roles" && len(roles) > 0) || seen[kind] {
			continue
		}
		seen[kind] = true
		parse = append(parse, kind)
	}
	out := map[string]any{"parse": parse, "replied_user": d.AllowedMentions.RepliedUser}
	if len(users) > 0 {
		out["users"] = users
	}
	if len(roles) > 0 {
		out["roles"] = roles
	}
	return out
}

func ValidMentionIDs(ids []string) []string {
	out := make([]string, 0, min(len(ids), 100))
	seen := map[string]bool{}
	for _, id := range ids {
		if len(out) == 100 {
			break
		}
		if len(id) < 17 || len(id) > 20 || seen[id] {
			continue
		}
		valid := true
		for _, char := range id {
			if char < '0' || char > '9' {
				valid = false
				break
			}
		}
		if valid {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

// SendPayload posts payload in channelID and returns the new message's id.
func SendPayload(s *discordgo.Session, channelID string, payload map[string]any) (string, error) {
	trimPayloadStrings(payload)
	body, err := s.RequestWithBucketID("POST", discordgo.EndpointChannelMessages(channelID), payload, discordgo.EndpointChannelMessages(channelID))
	if err != nil {
		return "", err
	}
	var m struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &m)
	return m.ID, nil
}

// EditPayload replaces a message's content with payload.
func EditPayload(s *discordgo.Session, channelID, messageID string, payload map[string]any) error {
	trimPayloadStrings(payload)
	_, err := s.RequestWithBucketID("PATCH", discordgo.EndpointChannelMessage(channelID, messageID), payload, discordgo.EndpointChannelMessage(channelID, ""))
	return err
}

// EditOriginalPayload fills in the reply of an interaction that was deferred. Whether it is ephemeral was fixed when it was deferred.
func EditOriginalPayload(s *discordgo.Session, i *discordgo.Interaction, payload map[string]any) error {
	trimPayloadStrings(payload)
	_, err := s.RequestWithBucketID("PATCH", discordgo.EndpointWebhookMessage(i.AppID, i.Token, "@original"), payload, discordgo.EndpointWebhookToken(i.AppID, ""))
	return err
}

// historyPagePrefix identifies history pagination button custom IDs.

// ComponentText flattens the text of a V2 message so it shows up in the transcript.
func ComponentText(components []discordgo.MessageComponent) []string {
	var out []string
	for _, component := range components {
		switch c := component.(type) {
		case *discordgo.TextDisplay:
			out = append(out, c.Content)
		case discordgo.TextDisplay:
			out = append(out, c.Content)
		case *discordgo.Container:
			out = append(out, ComponentText(c.Components)...)
		case discordgo.Container:
			out = append(out, ComponentText(c.Components)...)
		case *discordgo.Section:
			out = append(out, ComponentText(c.Components)...)
		case discordgo.Section:
			out = append(out, ComponentText(c.Components)...)
		}
	}
	return out
}
