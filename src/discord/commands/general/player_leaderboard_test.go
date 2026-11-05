package general

import (
	"testing"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// =lb kills, =lb games, =lb final kills ... : the words typed after the command must pick the stat, and a number must still be the page.
func TestLeaderboardPrefixWordsPickTheStat(t *testing.T) {
	cases := []struct {
		words []string
		stat  string
		page  int
	}{
		{[]string{"kills"}, "kills", 0},
		{[]string{"games"}, "games_played", 0},
		{[]string{"wins"}, "wins", 0},
		{[]string{"losses"}, "losses", 0},
		{[]string{"final", "kills"}, "final_kills", 0},
		{[]string{"beds"}, "beds_destroyed", 0},
		{[]string{"mvps"}, "mvp_count", 0},
		{[]string{"peak"}, "highest_elo", 0},
		{[]string{"streak"}, "highest_win_streak", 0},
		{[]string{"assists"}, "assists", 0},
		{[]string{"elo"}, "elo", 0},
		{[]string{"kills", "2"}, "kills", 2},
		{[]string{"2", "games"}, "games_played", 2},
		{[]string{"3"}, "", 3},
	}
	for _, c := range cases {
		msg := &discordgo.Message{ID: "1", GuildID: "2", ChannelID: "3", Author: &discordgo.User{ID: "4"}, Member: &discordgo.Member{User: &discordgo.User{ID: "4"}}}
		i := helpers.BuildPrefixInteraction(nil, msg, PlayerLeaderboardCommand, &types.PrefixCommandRoute{CommandName: "leaderboard", RemainingArgs: c.words})
		if got := helpers.OptionString(i, "stat"); got != c.stat {
			t.Errorf("=lb %v: stat %q, want %q", c.words, got, c.stat)
		}
		if got := helpers.OptionInt(i, "page"); got != c.page {
			t.Errorf("=lb %v: page %d, want %d", c.words, got, c.page)
		}
	}
}

// A banner's column heading has room for five characters, and every stat needs its own.
func TestLeaderboardStatHeadingsFit(t *testing.T) {
	seen := map[string]bool{}
	for _, st := range leaderboardStats {
		if st.short == "" || len([]rune(st.short)) > 5 {
			t.Errorf("%s: heading %q must be 1-5 characters", st.key, st.short)
		}
		if seen[st.short] {
			t.Errorf("%s: heading %q is used twice", st.key, st.short)
		}
		seen[st.short] = true
	}
}
