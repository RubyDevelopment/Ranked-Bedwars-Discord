// alert_repost reads the latest messages of the old alerts and decay-alerts channels and posts every one again, one by one and oldest
// first, in one channel, with the bot's current embeds:
//
//   - alerts (queue denied, game voided, queue status…): already the bot's own embeds, copied as they are;
//   - decay alerts: the older "ELO Decay Warning" and "ELO Decay Applied" embeds are rebuilt as the bot's current "ELO Decay Incoming"
//     and "ELO Decayed" ones (a notice from an old log does not know the ELO before and after a warning, so those are left out);
//     the current ones are copied as they are.
//
// Nothing is mentioned out loud: every message is sent with mention parsing switched off, so the <@player> in the text and in the embed
// shows as a mention but pings nobody.
//
// Nothing is sent unless --yes is given, so a first run only shows what would be posted. A message already in the destination channel
// (among its latest 100) is skipped, so running it again does not post it twice.
//
//	go run ./scripts/alert_repost                 (preview of the default channels)
//	go run ./scripts/alert_repost --yes
//
// The token is read from DISCORD_BOT_TOKEN (the bot's .env), or --token. The bot needs to read the source channels and to send messages
// in the destination. DATABASE_API_URL and DATABASE_API_KEY, when set, give the server's own embed branding.
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
	"ranked-bedwars/src/discord/helpers/ratings"
)

const (
	defaultGuild = "1228653980780072981"
	defaultTo    = "1566954767031468102"
)

// The channels the messages come from: alerts and decay alerts.
var defaultFrom = []string{"1228653983912955946", "1334366849319702538"}

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, strings.TrimSpace(v)); return nil }

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
	to := flag.String("to", defaultTo, "channel the messages are posted in")
	guild := flag.String("guild", defaultGuild, "the server (for its icon and embed branding)")
	limit := flag.Int("limit", 25, "how many of the latest messages to read in each channel (at most 100)")
	delay := flag.Duration("delay", 1200*time.Millisecond, "pause between two posts")
	verbose := flag.Bool("verbose", false, "also show each post's text, colour, thumbnail, footer and time")
	confirmed := flag.Bool("yes", false, "post the messages (without it nothing is sent)")
	flag.Var(&from, "from", "source channel ID (repeat for several; default: alerts and decay alerts)")
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

	if os.Getenv("DATABASE_API_URL") != "" && os.Getenv("DATABASE_API_KEY") != "" {
		if err := database.NewClient().Initialize(); err != nil {
			fmt.Fprintln(os.Stderr, "Database not available, continuing without it:", err)
		}
	}

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
				p, ok := convert(s, *guild, channel, m, e)
				if !ok {
					skipped++
					fmt.Printf("  skipped (not a message I know): #%s %q\n", channel, e.Title)
					continue
				}
				posts = append(posts, p)
				found++
			}
		}
		fmt.Printf("Channel %s: read %d messages, %d to post.\n", channel, len(messages), found)
	}
	sort.SliceStable(posts, func(i, j int) bool { return posts[i].posted.Before(posts[j].posted) })

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
		for _, f := range p.embed.Fields {
			fmt.Printf("  · %s: %s\n", f.Name, strings.ReplaceAll(f.Value, "\n", " "))
		}
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
	fmt.Printf("\n%d messages found, %d already in the destination, %d not recognised.\n", len(posts), already, skipped)
	if *confirmed {
		fmt.Printf("Posted %d.\n", sent)
	} else {
		fmt.Println("Nothing was sent. Run again with --yes to post them.")
	}
}

func fingerprint(e *discordgo.MessageEmbed) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s", e.Title, e.Description)
	for _, f := range e.Fields {
		fmt.Fprintf(&b, "\n%s=%s", f.Name, f.Value)
	}
	return b.String()
}

var (
	warningRe = regexp.MustCompile(`<@!?(\d+)>, your ELO will decay by \*\*(\d+)\*\* on <t:(\d+):`)
	appliedRe = regexp.MustCompile("<@!?(\\d+)> was inactive for \\*\\*(\\d+)\\+? days\\*\\* and lost \\*\\*(\\d+)\\*\\* ELO\\.\\s*\\n+\\*\\*Previous:\\*\\* `(\\d+)` → \\*\\*New:\\*\\* `(\\d+)`")
	mentionRe = regexp.MustCompile(`<@!?(\d+)>`)
)

// when is when the message happened: the embed's own time, else the message's.
func when(m *discordgo.Message, e *discordgo.MessageEmbed) time.Time {
	if e.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			return t
		}
	}
	return m.Timestamp
}

// convert makes the post for one embed of a source channel: a current embed is copied as it is, an older decay one is rebuilt.
func convert(s *discordgo.Session, guild, channel string, m *discordgo.Message, e *discordgo.MessageEmbed) (post, bool) {
	at := when(m, e)
	content := m.Content
	var embed *discordgo.MessageEmbed

	switch e.Title {
	case "ELO Decay Warning":
		g := warningRe.FindStringSubmatch(e.Description)
		if g == nil {
			return post{}, false
		}
		due, _ := strconv.ParseInt(g[3], 10, 64)
		amount, _ := strconv.Atoi(g[2])
		embed = ratings.DecayWarningEmbed(ratings.DecayNotice{Kind: ratings.DecayWarning, UserID: g[1], Amount: amount, At: time.Unix(due, 0)})
		if strings.TrimSpace(content) == "" {
			content = "<@" + g[1] + ">"
		}
	case "ELO Decay Applied":
		g := appliedRe.FindStringSubmatch(e.Description)
		if g == nil {
			return post{}, false
		}
		days, _ := strconv.Atoi(g[2])
		amount, _ := strconv.Atoi(g[3])
		before, _ := strconv.Atoi(g[4])
		after, _ := strconv.Atoi(g[5])
		embed = ratings.DecayAppliedEmbed(ratings.DecayNotice{Kind: ratings.DecayApplied, UserID: g[1], Amount: amount, Before: before, After: after, InactivityDays: days})
		if strings.TrimSpace(content) == "" {
			content = "<@" + g[1] + ">"
		}
	default:
		// the bot's current embeds (queue denied, game voided, queue status, ELO Decay Incoming, ELO Decayed…): copied as they are
		if e.Title == "" && e.Description == "" && len(e.Fields) == 0 {
			return post{}, false
		}
		embed = copyOf(e)
		if strings.TrimSpace(content) == "" {
			if g := mentionRe.FindStringSubmatch(e.Description); g != nil && strings.HasPrefix(e.Title, "ELO Decay") {
				content = "<@" + g[1] + ">"
			}
		}
	}

	if embed.Timestamp == "" {
		embed.Timestamp = at.UTC().Format(time.RFC3339) // a rebuilt notice happened when the old one was posted
	}
	helpers.WithGuildFooterIconForGuild(s, guild, embed)
	return post{posted: m.Timestamp, at: at, from: channel, content: content, embed: embed}, true
}

// copyOf is an embed as it was sent, without the read-only parts Discord adds (proxy addresses, sizes).
func copyOf(e *discordgo.MessageEmbed) *discordgo.MessageEmbed {
	c := &discordgo.MessageEmbed{URL: e.URL, Title: e.Title, Description: e.Description, Timestamp: e.Timestamp, Color: e.Color}
	if e.Footer != nil {
		c.Footer = &discordgo.MessageEmbedFooter{Text: e.Footer.Text, IconURL: e.Footer.IconURL}
	}
	if e.Image != nil {
		c.Image = &discordgo.MessageEmbedImage{URL: e.Image.URL}
	}
	if e.Thumbnail != nil {
		c.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: e.Thumbnail.URL}
	}
	if e.Author != nil {
		c.Author = &discordgo.MessageEmbedAuthor{Name: e.Author.Name, URL: e.Author.URL, IconURL: e.Author.IconURL}
	}
	for _, f := range e.Fields {
		c.Fields = append(c.Fields, &discordgo.MessageEmbedField{Name: f.Name, Value: f.Value, Inline: f.Inline})
	}
	return c
}
