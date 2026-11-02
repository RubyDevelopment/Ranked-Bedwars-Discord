package tickets

import (
	"bytes"
	"fmt"
	"html"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/types"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// PostTicketLogV2 sends a Components V2 entry (the closed-ticket transcript) to the log channel.
func PostTicketLogV2(s *discordgo.Session, panel *types.InteractivePanel, components []discordgo.MessageComponent, files []*discordgo.File) {
	if panel == nil || panel.Ticket == nil || panel.Ticket.Settings.LogChannelID == "" {
		return
	}
	_, _ = s.ChannelMessageSendComplex(panel.Ticket.Settings.LogChannelID, &discordgo.MessageSend{
		Flags:           discordgo.MessageFlagsIsComponentsV2,
		Components:      components,
		Files:           files,
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
	})
}

// PostTicketLog sends an entry to the panel's log channel, if one is configured.
func PostTicketLog(s *discordgo.Session, panel *types.InteractivePanel, embed *discordgo.MessageEmbed, files []*discordgo.File) {
	if panel == nil || panel.Ticket == nil || panel.Ticket.Settings.LogChannelID == "" {
		return
	}
	_, _ = s.ChannelMessageSendComplex(panel.Ticket.Settings.LogChannelID, &discordgo.MessageSend{
		Embeds:          []*discordgo.MessageEmbed{embed},
		Files:           files,
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
	})
}

// ============================================================================
// Transcript Generation
// ============================================================================

const transcriptStyle = `
*{box-sizing:border-box;margin:0;padding:0}
html{background:#1e1f22}
body{background:#1e1f22;color:#dbdee1;font-family:'gg sans','Noto Sans','Helvetica Neue',Helvetica,Arial,sans-serif;font-size:16px;line-height:1.375;padding:32px 16px}
a{color:#00a8fc;text-decoration:none}a:hover{text-decoration:underline}
.wrap{max-width:920px;margin:0 auto}
.header{background:#2b2d31;border-radius:12px;padding:24px;margin-bottom:16px;border-top:4px solid #5865f2}
.header h1{color:#f2f3f5;font-size:24px;font-weight:700;margin-bottom:4px}
.header .sub{color:#949ba4;font-size:14px;margin-bottom:16px}
.stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:12px}
.stat{background:#1e1f22;border-radius:8px;padding:10px 14px}
.stat .k{color:#949ba4;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.04em}
.stat .v{color:#f2f3f5;font-size:15px;font-weight:600;margin-top:2px;word-break:break-word}
.chat{background:#313338;border-radius:12px;padding:8px 0;overflow:hidden}
.group{display:flex;gap:16px;padding:8px 20px;margin-top:10px}
.group:first-child{margin-top:0}
.group:hover{background:#2e3035}
.avatar{width:40px;height:40px;border-radius:50%;flex-shrink:0;background:#5865f2;object-fit:cover}
.avatar.fallback{display:flex;align-items:center;justify-content:center;color:#fff;font-weight:700;font-size:18px}
.main{flex:1;min-width:0}
.meta{display:flex;align-items:center;gap:8px;margin-bottom:2px;flex-wrap:wrap}
.name{color:#f2f3f5;font-weight:600}
.tag{background:#5865f2;color:#fff;font-size:10px;font-weight:700;padding:1px 5px;border-radius:4px;text-transform:uppercase}
.time{color:#949ba4;font-size:12px}
.line{position:relative}
.line+.line{margin-top:2px}
.line .hover-time{display:none;position:absolute;left:-56px;top:2px;color:#949ba4;font-size:11px;width:46px;text-align:right}
.group:hover .line .hover-time{display:block}
.body{white-space:pre-wrap;word-wrap:break-word;overflow-wrap:anywhere}
.body code{background:#1e1f22;border-radius:4px;padding:1px 5px;font-family:Consolas,'Courier New',monospace;font-size:14px}
.body pre{background:#2b2d31;border:1px solid #1e1f22;border-radius:6px;padding:8px 10px;margin:4px 0;font-family:Consolas,'Courier New',monospace;font-size:14px;white-space:pre-wrap;overflow-x:auto}
.body pre code{background:none;padding:0}
.mention{background:rgba(88,101,242,.3);color:#c9cdfb;border-radius:4px;padding:0 3px;font-weight:500}
.body img.emoji{width:22px;height:22px;vertical-align:-5px}
.reply{color:#949ba4;font-size:13px;margin-bottom:4px}
.embed{max-width:520px;background:#2b2d31;border-left:4px solid #5865f2;border-radius:4px;padding:8px 16px 12px 12px;margin-top:6px}
.embed .e-author{color:#f2f3f5;font-size:14px;font-weight:600;margin-top:4px}
.embed .e-title{color:#f2f3f5;font-weight:700;margin-top:4px}
.embed .e-desc{font-size:14px;margin-top:4px;white-space:pre-wrap;overflow-wrap:anywhere}
.embed .e-fields{display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-top:8px}
.embed .e-field{grid-column:1/-1;font-size:14px}
.embed .e-field.inline{grid-column:auto}
.embed .e-fname{color:#f2f3f5;font-weight:700;font-size:14px}
.embed .e-fval{white-space:pre-wrap;overflow-wrap:anywhere}
.embed .e-foot{color:#949ba4;font-size:12px;margin-top:8px}
.embed img.e-img{max-width:100%;border-radius:4px;margin-top:8px}
.attachments{margin-top:6px;display:flex;flex-direction:column;gap:6px;align-items:flex-start}
.attachments img{max-width:420px;max-height:320px;border-radius:6px}
.file{background:#2b2d31;border:1px solid #1e1f22;border-radius:6px;padding:8px 12px;font-size:14px}
.empty{color:#949ba4;text-align:center;padding:40px}
.footer{color:#949ba4;font-size:12px;text-align:center;margin-top:16px}
@media(max-width:600px){.group{padding:8px 12px;gap:12px}.stats{grid-template-columns:1fr 1fr}}
`

var (
	reCodeBlock   = regexp.MustCompile("(?s)```(?:[a-zA-Z0-9_+-]*\\n)?(.*?)```")
	reInlineCode  = regexp.MustCompile("`([^`\\n]+)`")
	reBold        = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reUnderline   = regexp.MustCompile(`__(.+?)__`)
	reItalicStar  = regexp.MustCompile(`\*([^*\n]+)\*`)
	reItalicUnder = regexp.MustCompile(`(^|[^\w])_([^_\n]+)_([^\w]|$)`)
	reStrike      = regexp.MustCompile(`~~(.+?)~~`)
	reLink        = regexp.MustCompile(`https?://(?:[^\s<&]|&amp;)+`)
	reUserMention = regexp.MustCompile(`&lt;@!?(\d+)&gt;`)
	reRoleMention = regexp.MustCompile(`&lt;@&amp;(\d+)&gt;`)
	reChanMention = regexp.MustCompile(`&lt;#(\d+)&gt;`)
	reEmoji       = regexp.MustCompile(`&lt;(a?):([A-Za-z0-9_]+):(\d+)&gt;`)
	reTimestamp   = regexp.MustCompile(`&lt;t:(\d+)(?::[a-zA-Z])?&gt;`)
	reHeading     = regexp.MustCompile(`(?m)^#{1,3} (.+)$`)
	reQuote       = regexp.MustCompile(`(?m)^&gt; (.+)$`)
)

// transcriptMarkdown turns Discord markdown into safe HTML. Text is escaped
// first, so nothing a member typed can inject markup.
func transcriptMarkdown(text string, names map[string]string) string {
	var stash []string
	keep := func(markup string) string {
		stash = append(stash, markup)
		return "\x00" + strconv.Itoa(len(stash)-1) + "\x00"
	}

	text = html.EscapeString(text)
	text = reCodeBlock.ReplaceAllStringFunc(text, func(m string) string {
		return keep("<pre><code>" + strings.TrimRight(reCodeBlock.FindStringSubmatch(m)[1], "\n") + "</code></pre>")
	})
	text = reInlineCode.ReplaceAllStringFunc(text, func(m string) string {
		return keep("<code>" + reInlineCode.FindStringSubmatch(m)[1] + "</code>")
	})

	text = reUserMention.ReplaceAllStringFunc(text, func(m string) string {
		id := reUserMention.FindStringSubmatch(m)[1]
		name := names[id]
		if name == "" {
			name = "unknown-user"
		}
		return keep(`<span class="mention">@` + html.EscapeString(name) + `</span>`)
	})
	text = reRoleMention.ReplaceAllString(text, `<span class="mention">@role</span>`)
	text = reChanMention.ReplaceAllString(text, `<span class="mention">#channel</span>`)
	text = reEmoji.ReplaceAllStringFunc(text, func(m string) string {
		g := reEmoji.FindStringSubmatch(m)
		ext := "png"
		if g[1] == "a" {
			ext = "gif"
		}
		return keep(fmt.Sprintf(`<img class="emoji" alt=":%s:" src="https://cdn.discordapp.com/emojis/%s.%s?size=44">`, g[2], g[3], ext))
	})
	text = reTimestamp.ReplaceAllStringFunc(text, func(m string) string {
		unix, _ := strconv.ParseInt(reTimestamp.FindStringSubmatch(m)[1], 10, 64)
		return keep(`<span class="mention">` + time.Unix(unix, 0).UTC().Format("2006-01-02 15:04 UTC") + `</span>`)
	})
	text = reLink.ReplaceAllStringFunc(text, func(m string) string {
		return keep(`<a href="` + m + `" target="_blank" rel="noopener">` + m + `</a>`)
	})

	text = reHeading.ReplaceAllString(text, `<strong>$1</strong>`)
	text = reQuote.ReplaceAllString(text, `<span style="border-left:4px solid #4e5058;padding-left:8px;display:inline-block">$1</span>`)
	text = reBold.ReplaceAllString(text, `<strong>$1</strong>`)
	text = reUnderline.ReplaceAllString(text, `<u>$1</u>`)
	text = reItalicStar.ReplaceAllString(text, `<em>$1</em>`)
	text = reItalicUnder.ReplaceAllString(text, `$1<em>$2</em>$3`)
	text = reStrike.ReplaceAllString(text, `<s>$1</s>`)

	for idx := len(stash) - 1; idx >= 0; idx-- {
		text = strings.ReplaceAll(text, "\x00"+strconv.Itoa(idx)+"\x00", stash[idx])
	}
	return text
}

// BuildTicketTranscript renders the thread's messages as a standalone HTML page.
func BuildTicketTranscript(s *discordgo.Session, ticket *types.Ticket) ([]byte, []types.DiscordLogMessage, error) {
	var messages []*discordgo.Message
	var fetchErr error
	beforeID := ""
	for len(messages) < 5000 {
		batch, err := s.ChannelMessages(ticket.ThreadID, 100, beforeID, "", "")
		if err != nil {
			fetchErr = err
			break
		}
		if len(batch) == 0 {
			break
		}
		messages = append(messages, batch...)
		beforeID = batch[len(batch)-1].ID
		if len(batch) < 100 {
			break
		}
	}
	// Discord returns newest first; the page reads oldest first.
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	archiveMessages := make([]types.DiscordLogMessage, 0, len(messages))
	for _, message := range messages {
		archiveMessages = append(archiveMessages, match.DiscordLogMessageOf(message))
	}

	names := map[string]string{}
	for _, message := range messages {
		if message.Author != nil {
			names[message.Author.ID] = transcriptDisplayName(message.Author, message.Member)
		}
		for _, mentioned := range message.Mentions {
			if _, known := names[mentioned.ID]; !known {
				names[mentioned.ID] = mentioned.Username
			}
		}
	}

	var page bytes.Buffer
	fmt.Fprintf(&page, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>%s</style></head><body><div class="wrap">`,
		html.EscapeString(ticket.ThreadName), transcriptStyle)

	stat := func(key, value string) string {
		return `<div class="stat"><div class="k">` + key + `</div><div class="v">` + html.EscapeString(value) + `</div></div>`
	}
	formatTime := func(unix int64) string {
		if unix == 0 {
			return "-"
		}
		return time.Unix(unix, 0).UTC().Format("Jan 2, 2006 15:04 UTC")
	}
	closer := "-"
	if ticket.ClosedBy != "" {
		closer = names[ticket.ClosedBy]
		if closer == "" {
			closer = ticket.ClosedBy
		}
	}
	fmt.Fprintf(&page, `<div class="header"><h1>%s</h1><div class="sub">Ticket transcript</div><div class="stats">%s%s%s%s%s%s</div></div>`,
		html.EscapeString(ticket.ThreadName),
		stat("Reference", "#"+ticket.Ref),
		stat("Category", ticket.CategoryLabel),
		stat("Owner", ticket.OwnerName),
		stat("Opened", formatTime(ticket.CreatedAt)),
		stat("Closed", formatTime(ticket.ClosedAt)),
		stat("Messages", strconv.Itoa(len(messages))+" - closed by "+closer),
	)

	page.WriteString(`<div class="chat">`)
	if len(messages) == 0 {
		page.WriteString(`<div class="empty">No messages were found in this ticket.</div>`)
	}

	var lastAuthor string
	var lastTime time.Time
	open := false
	for _, message := range messages {
		authorID := ""
		if message.Author != nil {
			authorID = message.Author.ID
		}
		grouped := open && authorID == lastAuthor && message.Timestamp.Sub(lastTime) < 7*time.Minute
		if !grouped {
			if open {
				page.WriteString(`</div></div>`)
			}
			writeTranscriptGroupHeader(&page, message, names)
			open = true
		}
		lastAuthor, lastTime = authorID, message.Timestamp
		writeTranscriptMessage(&page, message, names)
	}
	if open {
		page.WriteString(`</div></div>`)
	}
	page.WriteString(`</div>`)
	fmt.Fprintf(&page, `<div class="footer">Generated %s</div></div></body></html>`, time.Now().UTC().Format("Jan 2, 2006 15:04 UTC"))
	return page.Bytes(), archiveMessages, fetchErr
}

func appendTicketLog(ticket *types.Ticket, eventType, actorID, message string, createdAt int64) {
	ticket.Logs = append(ticket.Logs, types.TicketLogEntry{
		Type: eventType, ActorID: actorID, Message: message, CreatedAt: createdAt,
	})
}

func transcriptDisplayName(user *discordgo.User, member *discordgo.Member) string {
	if member != nil && member.Nick != "" {
		return member.Nick
	}
	if user.GlobalName != "" {
		return user.GlobalName
	}
	return user.Username
}

func writeTranscriptGroupHeader(page *bytes.Buffer, message *discordgo.Message, names map[string]string) {
	name := "Unknown"
	avatar := `<div class="avatar fallback">?</div>`
	tag := ""
	if message.Author != nil {
		name = names[message.Author.ID]
		if name == "" {
			name = message.Author.Username
		}
		if message.Author.Bot {
			tag = `<span class="tag">Bot</span>`
		}
		avatar = fmt.Sprintf(`<img class="avatar" src="%s" alt="">`, html.EscapeString(message.Author.AvatarURL("64")))
	}
	fmt.Fprintf(page, `<div class="group">%s<div class="main"><div class="meta"><span class="name">%s</span>%s<span class="time">%s</span></div>`,
		avatar, html.EscapeString(name), tag, message.Timestamp.UTC().Format("Jan 2, 2006 15:04"))
}

func writeTranscriptMessage(page *bytes.Buffer, message *discordgo.Message, names map[string]string) {
	page.WriteString(`<div class="line">`)
	fmt.Fprintf(page, `<span class="hover-time">%s</span>`, message.Timestamp.UTC().Format("15:04"))

	if text := strings.TrimSpace(message.Content); text != "" {
		fmt.Fprintf(page, `<div class="body">%s</div>`, transcriptMarkdown(text, names))
	}
	if v2 := strings.Join(helpers.ComponentText(message.Components), "\n"); strings.TrimSpace(v2) != "" {
		fmt.Fprintf(page, `<div class="body">%s</div>`, transcriptMarkdown(v2, names))
	}

	for _, embed := range message.Embeds {
		color := "#5865f2"
		if embed.Color != 0 {
			color = HexColor(embed.Color)
		}
		fmt.Fprintf(page, `<div class="embed" style="border-left-color:%s">`, color)
		if embed.Author != nil && embed.Author.Name != "" {
			fmt.Fprintf(page, `<div class="e-author">%s</div>`, html.EscapeString(embed.Author.Name))
		}
		if embed.Title != "" {
			fmt.Fprintf(page, `<div class="e-title">%s</div>`, transcriptMarkdown(embed.Title, names))
		}
		if embed.Description != "" {
			fmt.Fprintf(page, `<div class="e-desc">%s</div>`, transcriptMarkdown(embed.Description, names))
		}
		if len(embed.Fields) > 0 {
			page.WriteString(`<div class="e-fields">`)
			for _, field := range embed.Fields {
				class := "e-field"
				if field.Inline {
					class += " inline"
				}
				fmt.Fprintf(page, `<div class="%s"><div class="e-fname">%s</div><div class="e-fval">%s</div></div>`,
					class, html.EscapeString(field.Name), transcriptMarkdown(field.Value, names))
			}
			page.WriteString(`</div>`)
		}
		if embed.Image != nil && embed.Image.URL != "" {
			fmt.Fprintf(page, `<img class="e-img" src="%s" alt="">`, html.EscapeString(embed.Image.URL))
		}
		if embed.Footer != nil && embed.Footer.Text != "" {
			fmt.Fprintf(page, `<div class="e-foot">%s</div>`, html.EscapeString(embed.Footer.Text))
		}
		page.WriteString(`</div>`)
	}

	if len(message.Attachments) > 0 {
		page.WriteString(`<div class="attachments">`)
		for _, attachment := range message.Attachments {
			url := html.EscapeString(attachment.URL)
			if strings.HasPrefix(attachment.ContentType, "image/") {
				fmt.Fprintf(page, `<a href="%s" target="_blank" rel="noopener"><img src="%s" alt="%s"></a>`, url, url, html.EscapeString(attachment.Filename))
				continue
			}
			fmt.Fprintf(page, `<div class="file">📎 <a href="%s" target="_blank" rel="noopener">%s</a></div>`, url, html.EscapeString(attachment.Filename))
		}
		page.WriteString(`</div>`)
	}
	page.WriteString(`</div>`)
}

// ============================================================================
// Components V2 Plumbing
