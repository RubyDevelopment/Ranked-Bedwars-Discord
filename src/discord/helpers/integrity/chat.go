package integrity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"ranked-bedwars/src/database"

	"github.com/andybalholm/brotli"
)

// What the game chats say. The statistics say that something is odd; the chats of the games involved can say why. Only what the players
// involved themselves wrote is looked at, and only for words that matter here; a hit is a lead to read in context, never proof.

// suspicious is what players say about throwing, boosting and trading wins.
var suspicious = regexp.MustCompile(`(?i)\b(let (you|him|her|them|u) win|i'?ll lose|i will lose|lose on purpose|on purpose|intentional(ly)?|throw(ing)? (the |this )?(game|it|match)|boost(ing|ed)?|win ?trad(e|ing)|trade (wins?|games?)|your turn|my turn to win|next one'?s? (yours|mine)|give (you|u) the win|sit(ting)? out|stay(ing)? in spawn|(i'?ll|ill) carry|carry (you|u)|don'?t (play|fight)|dont (play|fight)|elo ?farm|farm(ing)? elo|alt account|my alt|afk)\b`)

// Hit is one message worth reading.
type Hit struct {
	Game   int
	Source string // "discord" or "minecraft"
	Who    string
	At     time.Time
	Text   string
}

type logRefs struct {
	Discord string `json:"discord"`
	Game    string `json:"game"`
}

type storedGame struct {
	Logs    logRefs `json:"logs"`
	Players []struct {
		PlayerID uint   `json:"player_id"`
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	} `json:"players"`
}

// fetchLog reads a stored log: JSON, brotli-compressed when it was written by this bot.
func fetchLog(ctx context.Context, link string, into any) error {
	if link == "" {
		return fmt.Errorf("no log")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept-Encoding", "br")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("log: %s", resp.Status)
	}
	var body io.Reader = io.LimitReader(resp.Body, 40<<20)
	if resp.Header.Get("Content-Encoding") == "br" {
		body = brotli.NewReader(body)
	}
	return json.NewDecoder(body).Decode(into)
}

// ChatHits looks in the logs of the given games (a few of them) for what the finding's players wrote about throwing, boosting or trading wins.
// players are the finding's player ids.
func ChatHits(ctx context.Context, guildID string, games []int, players []uint, max int) []Hit {
	if len(games) > 5 {
		games = games[len(games)-5:] // the most recent
	}
	var mu sync.Mutex
	var hits []Hit
	var wg sync.WaitGroup
	for _, number := range games {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec, err := database.Must().Client().GetGameByNumber(ctx, guildID, number, nil)
			if err != nil || rec == nil || rec.RuntimeMetadata.IsEmpty() {
				return
			}
			var g storedGame
			if rec.RuntimeMetadata.Unmarshal(&g) != nil {
				return
			}
			byUser, byName := map[string]string{}, map[string]string{}
			for _, p := range g.Players {
				for _, id := range players {
					if p.PlayerID == id {
						byUser[p.UserID] = trimTags(p.Username)
						byName[strings.ToLower(trimTags(p.Username))] = trimTags(p.Username)
					}
				}
			}
			if len(byUser) == 0 {
				return
			}
			var found []Hit
			var discord struct {
				Messages []struct {
					At      time.Time `json:"at"`
					Content string    `json:"content"`
					Author  struct {
						ID string `json:"id"`
					} `json:"author"`
				} `json:"messages"`
			}
			if fetchLog(ctx, g.Logs.Discord, &discord) == nil {
				for _, m := range discord.Messages {
					if who, ok := byUser[m.Author.ID]; ok && suspicious.MatchString(m.Content) {
						found = append(found, Hit{Game: number, Source: "discord", Who: who, At: m.At, Text: m.Content})
					}
				}
			}
			var mc struct {
				Messages []struct {
					At   int64  `json:"at"`
					Text string `json:"text"`
				} `json:"messages"`
			}
			if fetchLog(ctx, g.Logs.Game, &mc) == nil {
				for _, m := range mc.Messages {
					if !suspicious.MatchString(m.Text) {
						continue
					}
					low := strings.ToLower(m.Text)
					for lower, name := range byName {
						if strings.Contains(low, lower+":") || strings.Contains(low, lower+" »") || strings.Contains(low, "<"+lower+">") {
							found = append(found, Hit{Game: number, Source: "minecraft", Who: name, At: time.UnixMilli(m.At), Text: m.Text})
							break
						}
					}
				}
			}
			mu.Lock()
			hits = append(hits, found...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Game != hits[j].Game {
			return hits[i].Game < hits[j].Game
		}
		return hits[i].At.Before(hits[j].At)
	})
	if len(hits) > max {
		hits = hits[:max]
	}
	return hits
}

var nameTag = regexp.MustCompile(`^\s*\[[^\]]*\]\s*|\s*\[[^\]]*\]\s*$`)

func trimTags(s string) string { return strings.TrimSpace(nameTag.ReplaceAllString(s, "")) }
