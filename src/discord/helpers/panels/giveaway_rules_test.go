package panels

import (
	"math/rand"
	"strings"
	"testing"
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
