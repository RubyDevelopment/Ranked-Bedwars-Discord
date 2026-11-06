package panels

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

var ranks = []string{"Coal", "Iron", "Gold", "Diamond", "Obsidian"}

func TestGiveawayRules(t *testing.T) {
	player := entrant{Registered: true, Elo: 1500, Games: 40, Wins: 22, Rank: "Gold", Roles: []string{"member"}}
	cases := []struct {
		name string
		g    giveawayDoc
		e    entrant
		want string // a part of the refusal, "" for allowed
	}{
		{"no rules: anyone", giveawayDoc{}, entrant{}, ""},
		{"min elo", giveawayDoc{RequiredElo: 1600}, player, "at least **1600 ELO**"},
		{"max elo", giveawayDoc{MaxElo: 1200}, player, "up to **1200 ELO**"},
		{"elo range ok", giveawayDoc{RequiredElo: 1000, MaxElo: 1800}, player, ""},
		{"min rank refuses lower", giveawayDoc{MinRank: "Diamond"}, player, "**Diamond** or higher"},
		{"min rank allows equal", giveawayDoc{MinRank: "gold"}, player, ""},
		{"max rank refuses higher", giveawayDoc{MaxRank: "Iron"}, player, "**Iron** and below"},
		{"games", giveawayDoc{MinGames: 50}, player, "50 games"},
		{"wins", giveawayDoc{MinWins: 30}, player, "30 wins"},
		{"a rule on stats needs registration", giveawayDoc{MinWins: 1}, entrant{}, "must be registered"},
		{"required role: any of", giveawayDoc{RequiredRoleIDs: []string{"vip", "member"}}, player, ""},
		{"required role missing", giveawayDoc{RequiredRoleIDs: []string{"vip"}}, player, "need <@&vip>"},
		{"blocked role", giveawayDoc{BlockedRoleIDs: []string{"member"}}, player, "cannot enter"},
		{"punished", giveawayDoc{NoActivePunishment: true}, entrant{Punished: true}, "active punishment"},
		{"past winner", giveawayDoc{ExcludePastWinners: true}, entrant{PastWinner: true}, "earlier giveaway"},
		{"unknown rank name in the rule is not held against anyone", giveawayDoc{MinRank: "Ruby"}, player, ""},
	}
	for _, c := range cases {
		got := c.g.check(c.e, ranks)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// A giveaway that sets no rule never reads stats; one that does, does.
func TestNeedsStats(t *testing.T) {
	if (giveawayDoc{RequiredRoleIDs: []string{"x"}, NoActivePunishment: true}).needsStats() {
		t.Error("role and punishment rules do not need stats")
	}
	if !(giveawayDoc{MinGames: 1}).needsStats() || !(giveawayDoc{BonusRanks: map[string]int{"Gold": 1}}).needsStats() {
		t.Error("games and rank bonuses need stats")
	}
}

func TestBonusEntries(t *testing.T) {
	g := giveawayDoc{BonusRanks: map[string]int{"Gold": 2}, BonusRoles: map[string]int{"booster": 3, "vip": 1}}
	if w := g.weight(entrant{Rank: "Gold", Roles: []string{"booster"}}); w != 6 {
		t.Errorf("gold booster holds %d entries, want 6", w)
	}
	if w := g.weight(entrant{Rank: "Iron"}); w != 1 {
		t.Errorf("a plain entrant holds %d entries, want 1", w)
	}
	if w := (giveawayDoc{BonusRoles: map[string]int{"x": 500}}).weight(entrant{Roles: []string{"x"}}); w != maxWeight {
		t.Errorf("weight %d is not capped at %d", w, maxWeight)
	}
}

// Winners are different people, never more than entered, and a heavier entry wins more often.
func TestPickWinners(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	entries := []string{"a", "b", "c", "d"}
	won := pickWinners(entries, nil, 10, rng)
	if len(won) != 4 {
		t.Fatalf("%d winners from 4 entries", len(won))
	}
	seen := map[string]bool{}
	for _, w := range won {
		if seen[w] {
			t.Fatalf("%s won twice", w)
		}
		seen[w] = true
	}
	if got := pickWinners(nil, nil, 3, rng); len(got) != 0 {
		t.Fatalf("winners from nobody: %v", got)
	}
	heavy := 0
	for i := 0; i < 2000; i++ {
		if pickWinners([]string{"light", "heavy"}, map[string]int{"heavy": 9}, 1, rng)[0] == "heavy" {
			heavy++
		}
	}
	if heavy < 1650 || heavy > 1950 { // expected about 90%
		t.Errorf("the 9x entry won %d of 2000 draws", heavy)
	}
}

func TestRulesTextAndEmbed(t *testing.T) {
	g := giveawayDoc{Prize: "Premium", Winners: 2, RequiredElo: 1000, MinRank: "Gold", MinGames: 20, NoActivePunishment: true, BonusRanks: map[string]int{"Diamond": 2}}
	text := g.rulesText()
	for _, part := range []string{"At least 1000 ELO", "Rank Gold or higher", "20+ games this season", "No active punishment", "extra entries"} {
		if !strings.Contains(text, part) {
			t.Errorf("the rules do not say %q:\n%s", part, text)
		}
	}
	if (giveawayDoc{}).rulesText() != "" {
		t.Error("a giveaway with no rules lists some")
	}
}

func TestAPIEmoji(t *testing.T) {
	for in, want := range map[string]string{"🎮": "🎮", " <:flame:123456> ": "flame:123456", "<a:dance:99>": "dance:99", ":flame:123": "flame:123", "flame:123": "flame:123"} {
		if got := apiEmoji(in); got != want {
			t.Errorf("apiEmoji(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGeneralRules(t *testing.T) {
	member := entrant{JoinedDays: 30, Roles: []string{"member"}}
	cases := []struct {
		name string
		g    giveawayDoc
		e    entrant
		want string
	}{
		{"joined long enough", giveawayDoc{MinJoinDays: 14}, member, ""},
		{"too new in the server", giveawayDoc{MinJoinDays: 60}, member, "**60 days**"},
		{"join date unknown is not held against anyone", giveawayDoc{MinJoinDays: 60}, entrant{JoinedDays: -1}, ""},
		{"boosters only: refused", giveawayDoc{BoostersOnly: true}, member, "boosters"},
		{"boosters only: allowed", giveawayDoc{BoostersOnly: true}, entrant{Booster: true}, ""},
		{"asks to be registered", giveawayDoc{RequireRegistered: true}, member, "must be registered"},
		{"registered is enough", giveawayDoc{RequireRegistered: true}, entrant{Registered: true}, ""},
		{"recent play: played yesterday", giveawayDoc{ActiveDays: 7}, entrant{Registered: true, IdleDays: 1}, ""},
		{"recent play: too long ago", giveawayDoc{ActiveDays: 7}, entrant{Registered: true, IdleDays: 20}, "last **7 days**"},
		{"recent play: never played", giveawayDoc{ActiveDays: 7}, entrant{Registered: true, IdleDays: -1}, "last **7 days**"},
	}
	for _, c := range cases {
		if got := c.g.check(c.e, ranks); (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// a rank bonus reads stats but never needs a registration
	if g := (giveawayDoc{BonusRanks: map[string]int{"Gold": 1}}); !g.needsStats() || g.needsRegistration() {
		t.Error("a rank bonus alone must read stats without refusing the unregistered")
	}
	if (giveawayDoc{RequiredRoleIDs: []string{"x"}, MinJoinDays: 3, BoostersOnly: true}).needsStats() {
		t.Error("general rules never read the ladder")
	}
}

// Every refusal that has a remedy names the command for it, written as the giveaway's own reference.
func TestRefusalsPointAtCommands(t *testing.T) {
	g := giveawayDoc{MinGames: 5, RequiredElo: 900, MinRank: "Gold", ref: func(name string) string { return "<" + name + ">" }}
	for _, c := range []struct {
		e    entrant
		want string
	}{
		{entrant{}, "<register>"},
		{entrant{Registered: true, Elo: 100, Games: 10, Rank: "Gold"}, "<stats>"},
		{entrant{Registered: true, Elo: 1000, Games: 10, Rank: "Iron"}, "<ranks>"},
		{entrant{Registered: true, Elo: 1000, Games: 1, Rank: "Gold"}, "<queues>"},
	} {
		if got := g.check(c.e, ranks); !strings.Contains(got, c.want) {
			t.Errorf("%+v: %q does not point at %s", c.e, got, c.want)
		}
	}
	lines := strings.Join(g.requirements(), "\n")
	for _, want := range []string{"<register>", "<stats>", "<ranks>", "<queues>"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the requirements do not point at %s:\n%s", want, lines)
		}
	}
}

// The draw passes over people who no longer qualify and goes on to the next, and never gives a winner twice.
func TestDrawSkipsWhoNoLongerQualifies(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	entries := []string{"a", "b", "c", "d", "e"}
	gone := map[string]bool{"a": true, "c": true}
	for i := 0; i < 200; i++ {
		won := pickWinnersChecked(entries, nil, 2, rng, func(id string) bool { return !gone[id] })
		if len(won) != 2 {
			t.Fatalf("%d winners with three qualifying entries", len(won))
		}
		for _, w := range won {
			if gone[w] {
				t.Fatalf("%s no longer qualifies but won", w)
			}
		}
	}
	if won := pickWinnersChecked(entries, nil, 3, rng, func(id string) bool { return id == "b" }); len(won) != 1 || won[0] != "b" {
		t.Errorf("only b qualifies: %v", won)
	}
}

func TestEmbedStates(t *testing.T) {
	g := giveawayDoc{ID: "12", Prize: "Premium", Winners: 2, MinGames: 5, host: "123456789012345678", BonusRoles: map[string]int{"99": 2},
		ref: func(name string) string { return "</" + name + ":1>" }}
	running := giveawayEmbed(g, time.Unix(2_000_000_000, 0), giveawayView{})
	text := fmt.Sprint(running)
	for _, part := range []string{"🎉 Premium", "Press **Enter**", "Requirements", "</stats:1>", "Bonus entries", "Hosted by", "Giveaway #12", "Check where you stand", "</leaderboard:1>"} {
		if !strings.Contains(text, part) {
			t.Errorf("the running post lacks %q:\n%s", part, text)
		}
	}
	over := fmt.Sprint(giveawayEmbed(g, time.Time{}, giveawayView{state: giveawayOver, winners: []string{"7", "8"}, entries: 31}))
	for _, part := range []string{"ended", "<@7>, <@8>", "31", "Giveaway ended #12"} {
		if !strings.Contains(over, part) {
			t.Errorf("the ended post lacks %q:\n%s", part, over)
		}
	}
	if strings.Contains(over, "Requirements") {
		t.Error("an ended post should not list requirements again")
	}
	if got := fmt.Sprint(giveawayEmbed(giveawayDoc{ID: "3", Prize: "x"}, time.Unix(2_000_000_000, 0), giveawayView{})); !strings.Contains(got, "Open to everyone") {
		t.Errorf("a giveaway with no rules: %s", got)
	}
	if got := fmt.Sprint(giveawayEmbed(g, time.Time{}, giveawayView{state: giveawayCancelled})); !strings.Contains(got, "cancelled") {
		t.Errorf("a cancelled post: %s", got)
	}
}

func TestSameEmoji(t *testing.T) {
	for _, c := range []struct {
		bound string
		e     discordgo.Emoji
		want  bool
	}{
		{"🎮", discordgo.Emoji{Name: "🎮"}, true},
		{"❤️", discordgo.Emoji{Name: "❤"}, true}, // the variation selector does not make it another emoji
		{"🎮", discordgo.Emoji{Name: "🎲"}, false},
		{"<:flame:123>", discordgo.Emoji{Name: "flame", ID: "123"}, true},
		{"flame:123", discordgo.Emoji{Name: "flame", ID: "123"}, true},
		{"<a:dance:99>", discordgo.Emoji{Name: "dance", ID: "99"}, true},
		{"flame:123", discordgo.Emoji{Name: "flame", ID: "456"}, false},
	} {
		if got := sameEmoji(c.bound, c.e); got != c.want {
			t.Errorf("sameEmoji(%q, %+v) = %v, want %v", c.bound, c.e, got, c.want)
		}
	}
}
