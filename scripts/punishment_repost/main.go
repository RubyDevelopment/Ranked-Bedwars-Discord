// punishment_repost reads the latest messages of the old punishment log channels (strikes, warns, mutes, bans), takes the case out of each
// embed, and posts every case again, one by one and oldest first, in one channel, in the format of the bot's punishment embeds
// (Punishment Applied / Revoked / Expired). It understands the bot's current embeds and the older "Moderation Log", "Revocation Log",
// "Appeal Log", "Strike Expired" and "Account Age Queue Ban" ones.
//
// Nothing is mentioned out loud: every message is sent with mention parsing switched off, so the <@player> in the text and in the embed
// shows as a mention but pings nobody.
//
// Nothing is sent unless --yes is given, so a first run only shows what would be posted. A case already in the destination channel
// (among its latest 100 messages) is skipped, so running it again does not post it twice.
//
//	go run ./scripts/punishment_repost                 (preview of the default channels)
//	go run ./scripts/punishment_repost --yes
//	go run ./scripts/punishment_repost --limit 10 --from 123… --from 456… --to 789… --yes
//
// The token is read from DISCORD_BOT_TOKEN (the bot's .env), or --token. The bot needs to read the source channels (and the Message
// Content intent, or their embeds come back empty) and to send messages in the destination. DATABASE_API_URL and DATABASE_API_KEY, when
// set, give the players' registered Minecraft names and the server's own embed branding.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/punish"
)

const (
	defaultGuild = "1228653980780072981"
	defaultTo    = "1566954729609896027"
)

// The channels the cases come from: strikes, warns, mutes and bans.
var defaultFrom = []string{"1228653983912955948", "1520177460258275568", "1299259518227714100", "1299259504143241247"}

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, strings.TrimSpace(v)); return nil }

// post is one message to send: a case in the bot's own format.
type post struct {
	posted  time.Time // when the message was posted in the source channel: the order they are posted again in
	at      time.Time // when it happened (the embed's own time): shown, and kept on the embed
	from    string
	content string
	embed   *discordgo.MessageEmbed
}

func main() {
	var from list
	token := flag.String("token", "", "bot token (default: DISCORD_BOT_TOKEN)")
	to := flag.String("to", defaultTo, "channel the cases are posted in")
	guild := flag.String("guild", defaultGuild, "the server (for its icon and the players' names)")
	limit := flag.Int("limit", 25, "how many of the latest messages to read in each channel (at most 100)")
	delay := flag.Duration("delay", 1200*time.Millisecond, "pause between two posts")
	verbose := flag.Bool("verbose", false, "also show each post's content, colour, thumbnail, footer and time")
	confirmed := flag.Bool("yes", false, "post the cases (without it nothing is sent)")
	flag.Var(&from, "from", "source channel ID (repeat for several; default: strikes, warns, mutes, bans)")
	flag.Parse()

	_ = godotenv.Load()
	if len(from) == 0 {
		from = defaultFrom
	}
	if *limit < 1 || *limit > 100 {
		fmt.Fprintln(os.Stderr, "--limit must be between 1 and 100.")
		os.Exit(2)
	}
	tok := strings.TrimSpace(*token)
	if tok == "" {
		tok = strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
	}
	if tok == "" {
		fmt.Fprintln(os.Stderr, "Missing the bot token: set DISCORD_BOT_TOKEN or pass --token.")
		os.Exit(2)
	}
	s, err := discordgo.New("Bot " + strings.TrimPrefix(tok, "Bot "))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	me, err := s.User("@me")
	if err != nil {
		fmt.Fprintln(os.Stderr, "The token was refused:", err)
		os.Exit(1)
	}
	fmt.Printf("Logged in as %s (%s).\n", me.Username, me.ID)

	// The players' registered names and the server's branding come from the database; without it the names fall back to Discord's.
	if os.Getenv("DATABASE_API_URL") != "" && os.Getenv("DATABASE_API_KEY") != "" {
		if err := database.NewClient().Initialize(); err != nil {
			fmt.Fprintln(os.Stderr, "Database not available, continuing without it:", err)
		}
	}

	r := &reposter{s: s, guild: *guild, names: map[string]string{}}
	var posts []post
	skipped := 0
	for _, channel := range from {
		messages, err := s.ChannelMessages(channel, *limit, "", "", "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not read channel %s: %v\n", channel, err)
			os.Exit(1)
		}
		found := 0
		for _, m := range messages {
			for _, e := range m.Embeds {
				p, ok := r.convert(channel, m, e)
				if !ok {
					skipped++
					fmt.Printf("  skipped (not a case I know): #%s %q\n", channel, e.Title)
					continue
				}
				posts = append(posts, p)
				found++
			}
		}
		fmt.Printf("Channel %s: read %d messages, %d cases.\n", channel, len(messages), found)
	}
	sort.SliceStable(posts, func(i, j int) bool { return posts[i].posted.Before(posts[j].posted) })

	// What the destination already holds, so a second run does not post the same case again.
	have := map[string]bool{}
	recent, err := s.ChannelMessages(*to, 100, "", "", "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not read the destination channel %s: %v\n", *to, err)
		os.Exit(1)
	}
	for _, m := range recent {
		for _, e := range m.Embeds {
			have[fingerprint(e)] = true
		}
	}

	sent, already := 0, 0
	for _, p := range posts {
		if have[fingerprint(p.embed)] {
			already++
			continue
		}
		fmt.Printf("\n[%s] from #%s\n  %s\n  %s\n", p.at.Format("2006-01-02 15:04"), p.from, p.embed.Title, strings.ReplaceAll(p.embed.Description, "\n", "\n  "))
		if *verbose {
			footer, icon := "", false
			if p.embed.Footer != nil {
				footer, icon = p.embed.Footer.Text, p.embed.Footer.IconURL != ""
			}
			fmt.Printf("  [content %q · color %d · thumbnail %v · footer %q icon %v · timestamp %s]\n", p.content, p.embed.Color, p.embed.Thumbnail != nil, footer, icon, p.embed.Timestamp)
		}
		if !*confirmed {
			continue
		}
		_, err := s.ChannelMessageSendComplex(*to, &discordgo.MessageSend{
			Content: p.content,
			Embeds:  []*discordgo.MessageEmbed{p.embed},
			// Nothing is parsed, so nobody is pinged by the mention in the text or in the embed.
			AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "  could not send: %v\n", err)
			os.Exit(1)
		}
		have[fingerprint(p.embed)] = true
		sent++
		time.Sleep(*delay)
	}
	fmt.Printf("\n%d cases found, %d already in the destination, %d not recognised.\n", len(posts), already, skipped)
	if *confirmed {
		fmt.Printf("Posted %d.\n", sent)
	} else {
		fmt.Println("Nothing was sent. Run again with --yes to post them.")
	}
}

func fingerprint(e *discordgo.MessageEmbed) string { return e.Title + "\n" + e.Description }

type reposter struct {
	s     *discordgo.Session
	guild string
	names map[string]string
}

// name is how the player reads in the case: the registered Minecraft name, else the Discord one, else the ID.
func (r *reposter) name(userID string) string {
	if n, ok := r.names[userID]; ok {
		return n
	}
	n := userID
	if svc := database.Get(); svc != nil {
		if view, err := svc.Player.Get(r.guild, userID); err == nil && view != nil && view.Config != nil && view.Config.Username != "" {
			n = view.Config.Username
		}
	}
	if n == userID {
		if u, err := r.s.User(userID); err == nil && u != nil {
			n = u.Username
		}
	}
	r.names[userID] = n
	return n
}

var (
	mentionRe = regexp.MustCompile(`<@!?(\d+)>`)
	idRe      = regexp.MustCompile(`\d{15,25}`)
	lengthRe  = regexp.MustCompile(`^(\d+)\s*([smhdw])`)
)

// line is the value of "**Label:** value" in a description, without the quote mark, the backticks around it, or surrounding space.
func line(desc, label string) string {
	re := regexp.MustCompile(`(?m)^[> ]*(?:-#\s*)?\*\*` + regexp.QuoteMeta(label) + `:\*\*\s*(.*)$`)
	m := re.FindStringSubmatch(desc)
	if m == nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(m[1]), "`")
}

func userIn(text string) string {
	if m := mentionRe.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return idRe.FindString(text)
}

// storedType turns what an old log called a punishment into the type the bot stores, which its embeds then name.
func storedType(word string) string {
	w := strings.ToLower(strings.TrimSpace(word))
	w = strings.TrimPrefix(w, "regular ")
	switch {
	case strings.Contains(w, "strike"):
		return "strike"
	case w == "warn" || w == "warning":
		return "warn"
	case w == "mute" || w == "chat mute":
		return "mute"
	case w == "vc mute" || w == "voice mute":
		return "vc_mute"
	case w == "ban" || w == "queue ban" || w == "match ban":
		return "ban"
	case w == "server ban":
		return "server_ban"
	}
	return strings.ReplaceAll(w, " ", "_")
}

// lasting is the length "3d" or "12h" as a time, and false for "Permanent" or anything else.
func lasting(text string) (time.Duration, bool) {
	m := lengthRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(text)))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
	return time.Duration(n) * unit, true
}

// when is when the case happened: the embed's own time, else the message's.
func when(m *discordgo.Message, e *discordgo.MessageEmbed) time.Time {
	if e.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			return t
		}
	}
	return m.Timestamp
}

// convert reads one embed of a log channel into a post in the bot's punishment format.
func (r *reposter) convert(channel string, m *discordgo.Message, e *discordgo.MessageEmbed) (post, bool) {
	at := when(m, e)
	d := e.Description
	var n *punish.PunishmentNotice
	var keep *discordgo.MessageEmbed // an embed already in the new format is posted as it is

	switch e.Title {
	case "Punishment Applied", "Punishment Revoked", "Punishment Expired":
		if strings.Contains(d, "**Player:**") {
			keep = e
			n = &punish.PunishmentNotice{User: &discordgo.User{ID: userIn(line(d, "Player"))}}
			break
		}
		// the older expiry notice: "Warn for <@id> has expired.  **Reason:** `…`"
		m := regexp.MustCompile(`(?s)^(.+?) for <@!?(\d+)> has expired`).FindStringSubmatch(d)
		if m == nil {
			return post{}, false
		}
		n = &punish.PunishmentNotice{Action: "expired", User: &discordgo.User{ID: m[2]}, Type: storedType(m[1]), Reason: line(d, "Reason")}
	case "Strike Expired":
		m := regexp.MustCompile(`for <@!?(\d+)> has expired`).FindStringSubmatch(d)
		if m == nil {
			return post{}, false
		}
		// "Strike **#631** for <@id> has expired.  **Level:** 1 | **Reason:** `q`": the reason sits on the same line as the level.
		n = &punish.PunishmentNotice{Action: "expired", User: &discordgo.User{ID: m[1]}, Type: "strike"}
		if reason := regexp.MustCompile("\\*\\*Reason:\\*\\*\\s*`([^`]*)`").FindStringSubmatch(d); reason != nil {
			n.Reason = reason[1]
		}
		if level := regexp.MustCompile(`\*\*Level:\*\*\s*(\d+)`).FindStringSubmatch(d); level != nil && n.Reason == "" {
			n.Reason = "Level " + level[1]
		}
		if n.Reason == "" {
			n.Reason = reasonOf(d)
		}
	case "Moderation Log":
		user := userIn(line(d, "Target"))
		if user == "" {
			return post{}, false
		}
		n = &punish.PunishmentNotice{Action: "applied", User: &discordgo.User{ID: user}, Type: storedType(line(d, "Action")), Reason: line(d, "Reason"), By: userIn(line(d, "Staff"))}
		if dur := line(d, "Duration"); dur != "" {
			n.Duration = dur
			if length, ok := lasting(dur); ok {
				n.Expires = at.Add(length).UTC().Format(time.RFC3339)
			}
		}
		if elo := regexp.MustCompile(`ELO Removed:\s*(\d+)`).FindStringSubmatch(line(d, "Details")); elo != nil {
			n.Elo = "`" + elo[1] + "`"
		}
	case "Revocation Log":
		user := userIn(line(d, "Target"))
		if user == "" {
			return post{}, false
		}
		action := strings.TrimPrefix(strings.ToUpper(line(d, "Action")), "UN")
		n = &punish.PunishmentNotice{Action: "revoked", User: &discordgo.User{ID: user}, Type: storedType(action), Reason: line(d, "Reason"), By: userIn(line(d, "Staff"))}
	case "Appeal Log":
		user := userIn(line(d, "Target"))
		if user == "" {
			return post{}, false
		}
		n = &punish.PunishmentNotice{Action: "revoked", User: &discordgo.User{ID: user}, Type: "strike", Reason: "Appealed: " + line(d, "Justification"), By: userIn(line(d, "Staff"))}
	case "Account Age Queue Ban":
		user := userIn(d)
		if user == "" {
			return post{}, false
		}
		n = &punish.PunishmentNotice{Action: "applied", User: &discordgo.User{ID: user}, Type: "ban", Duration: "Permanent"}
		for _, f := range e.Fields {
			if f.Name == "Reason" {
				n.Reason = f.Value
			}
		}
		if n.Reason == "" {
			n.Reason = "Discord account too new"
		}
	default:
		return post{}, false
	}

	var embed *discordgo.MessageEmbed
	if keep != nil {
		embed = &discordgo.MessageEmbed{Title: keep.Title, Description: keep.Description}
	} else {
		n.Username = r.name(n.User.ID)
		embed = punish.PunishmentAlertEmbed(*n)
	}
	embed.Timestamp = at.UTC().Format(time.RFC3339)
	embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: helpers.ThumbHammer}
	helpers.WithGuildFooterIconForGuild(r.s, r.guild, embed)

	content := m.Content
	if strings.TrimSpace(content) == "" {
		content = "<@" + n.User.ID + ">"
	}
	return post{posted: m.Timestamp, at: at, from: channel, content: content, embed: embed}, true
}

// reasonOf is the reason of an old embed that does not label one: its last line.
func reasonOf(desc string) string {
	lines := strings.Split(strings.TrimSpace(desc), "\n")
	return strings.Trim(strings.TrimSpace(lines[len(lines)-1]), "`")
}
