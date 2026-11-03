// voice_status_test tries out Discord's voice channel status (the line under a voice channel's name, like "✅ 3/8 queued · ✨ 1 in game"):
// it writes a temporary status to one voice channel, confirms Discord applied it, waits, and puts the old status back.
//
//	go run ./scripts/voice_status_test                                  (the default channel, 30 seconds)
//	go run ./scripts/voice_status_test --status "✅ 3/8 queued · ✨ 1 in game" --seconds 60
//	go run ./scripts/voice_status_test --keep                           (leave the new status in place)
//
// The REST channel object does not carry the status, so the script reads it from the gateway (the server's channel list when it
// connects, and the VOICE_CHANNEL_STATUS_UPDATE event after each change); it connects with the Guilds intent only, for a few seconds.
//
// The token is read from DISCORD_BOT_TOKEN (the bot's .env), or --token. The bot needs the "Set Voice Channel Status" permission in the
// channel (or Manage Channels). Ctrl-C while it waits still puts the old status back.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

const (
	defaultGuild   = "1228653980780072981"
	defaultChannel = "1262036072993521744"
)

// watcher remembers the status the gateway reports for one channel.
type watcher struct {
	channel string
	mu      sync.Mutex
	status  string
	known   bool
	changed chan struct{}
}

func (w *watcher) set(status string) {
	w.mu.Lock()
	w.status, w.known = status, true
	w.mu.Unlock()
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

func (w *watcher) get() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status, w.known
}

// handle reads the events that carry a channel's status.
func (w *watcher) handle(_ *discordgo.Session, e *discordgo.Event) {
	switch e.Type {
	case "GUILD_CREATE":
		var g struct {
			Channels []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"channels"`
		}
		if json.Unmarshal(e.RawData, &g) == nil {
			for _, c := range g.Channels {
				if c.ID == w.channel {
					w.set(c.Status)
				}
			}
		}
	case "VOICE_CHANNEL_STATUS_UPDATE", "CHANNEL_UPDATE":
		var c struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if json.Unmarshal(e.RawData, &c) == nil && c.ID == w.channel {
			w.set(c.Status)
		}
	}
}

// waitFor waits until the channel's status reads want, or the time runs out.
func (w *watcher) waitFor(want string, wait time.Duration) bool {
	deadline := time.After(wait)
	for {
		if got, ok := w.get(); ok && got == want {
			return true
		}
		select {
		case <-w.changed:
		case <-deadline:
			return false
		}
	}
}

func main() {
	token := flag.String("token", "", "bot token (default: DISCORD_BOT_TOKEN)")
	guild := flag.String("guild", defaultGuild, "the server the channel is in")
	channel := flag.String("channel", defaultChannel, "the voice channel")
	status := flag.String("status", "✅ 3/8 queued · ✨ 1 in game (test)", "the temporary status (at most 500 characters)")
	seconds := flag.Int("seconds", 30, "how long to leave it before putting the old one back")
	keep := flag.Bool("keep", false, "leave the new status in place")
	flag.Parse()

	_ = godotenv.Load()
	tok := strings.TrimSpace(*token)
	if tok == "" {
		tok = strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
	}
	if tok == "" {
		fmt.Fprintln(os.Stderr, "Missing the bot token: set DISCORD_BOT_TOKEN or pass --token.")
		os.Exit(2)
	}
	if n := len([]rune(*status)); n == 0 || n > 500 {
		fmt.Fprintln(os.Stderr, "--status must be 1 to 500 characters.")
		os.Exit(2)
	}
	s, err := discordgo.New("Bot " + strings.TrimPrefix(tok, "Bot "))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	s.Identify.Intents = discordgo.IntentsGuilds
	w := &watcher{channel: *channel, changed: make(chan struct{}, 1)}
	s.AddHandler(w.handle)
	if err := s.Open(); err != nil {
		fmt.Fprintln(os.Stderr, "Could not connect to Discord:", err)
		os.Exit(1)
	}
	defer s.Close()
	fmt.Printf("Logged in as %s.\n", s.State.User.Username)

	// The server's channel list arrives right after connecting.
	before := ""
	for i := 0; i < 40; i++ {
		if got, ok := w.get(); ok {
			before = got
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if _, ok := w.get(); !ok {
		fmt.Fprintf(os.Stderr, "The channel %s is not in server %s as far as the bot can see.\n", *channel, *guild)
		os.Exit(1)
	}
	fmt.Printf("Its status now: %q\n", before)

	if err := put(s, *channel, *status); err != nil {
		fmt.Fprintln(os.Stderr, "Discord refused the status:", err)
		fmt.Fprintln(os.Stderr, "(The bot needs the Set Voice Channel Status permission in that channel.)")
		os.Exit(1)
	}
	if w.waitFor(*status, 10*time.Second) {
		fmt.Printf("Set, and Discord confirms it: %q\n", *status)
	} else {
		got, _ := w.get()
		fmt.Printf("Discord accepted the request but the channel reads %q, not the new status.\n", got)
	}
	if *keep {
		fmt.Println("Left in place (--keep).")
		return
	}

	fmt.Printf("Waiting %d seconds, then putting back %q …\n", *seconds, before)
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	select {
	case <-time.After(time.Duration(*seconds) * time.Second):
	case <-interrupted:
		fmt.Println("Interrupted: putting the old status back now.")
	}
	if err := put(s, *channel, before); err != nil {
		fmt.Fprintln(os.Stderr, "Could not put the old status back:", err)
		os.Exit(1)
	}
	if w.waitFor(before, 10*time.Second) {
		fmt.Printf("Done. The status is back to %q.\n", before)
	} else {
		got, _ := w.get()
		fmt.Printf("Put back, but the channel reads %q.\n", got)
	}
}

// put writes a voice channel's status; an empty one clears it.
func put(s *discordgo.Session, channelID, status string) error {
	url := discordgo.EndpointChannel(channelID) + "/voice-status"
	_, err := s.RequestWithBucketID("PUT", url, map[string]string{"status": status}, url)
	return err
}
