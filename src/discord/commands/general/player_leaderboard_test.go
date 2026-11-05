package general

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/pagination"
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

// The page buttons: ones that cannot be pressed, and buttons of other features, must be left alone without touching Discord.
func TestLeaderboardPageButtonsIgnoreWhatIsNotTheirs(t *testing.T) {
	if handleLeaderboardPage(nil, nil, "pgv1|games|1|2") {
		t.Error("a games history button is not ours")
	}
	for _, id := range []string{"lbpg|off-back", "lbpg|off-page", "lbpg|off-next5", "lbpg|abc|kills", "lbpg|0|kills", "lbpg|-3|kills"} {
		if !handleLeaderboardPage(nil, nil, id) {
			t.Errorf("%q is ours and must be handled (as a no-op)", id)
		}
	}
}

// The ids the buttons carry are the page to go to and the stat, in the form the handler reads back.
func TestLeaderboardPageButtonIDs(t *testing.T) {
	rows := pagination.BuildPageRow(7, 12,
		func(target int) string { return fmt.Sprintf("%s%d|%s", leaderboardPagePrefix, target, "kills") },
		func(slot string) string { return leaderboardPagePrefix + "off-" + slot })
	var ids []string
	for _, c := range rows[0].(*discordgo.ActionsRow).Components {
		ids = append(ids, c.(*discordgo.Button).CustomID)
	}
	want := []string{"lbpg|2|kills", "lbpg|6|kills", "lbpg|off-page", "lbpg|8|kills", "lbpg|12|kills"}
	for n := range want {
		if ids[n] != want[n] {
			t.Errorf("button %d has id %q, want %q", n, ids[n], want[n])
		}
	}
}

// What the page buttons send to Discord, checked against a stand-in for it: one multipart request that clears the old attachments, uploads the
// new banner as files[0], and carries the new buttons.
func TestLeaderboardPageEditRequest(t *testing.T) {
	var gotType, gotPayload string
	var gotFile []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("not multipart: %v", err)
			return
		}
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "payload_json":
				gotPayload = string(data)
			case "files[0]":
				gotFile = data
			}
		}
		_, _ = w.Write([]byte(`{"id":"1"}`))
	}))
	defer srv.Close()
	old := discordgo.EndpointWebhooks
	discordgo.EndpointWebhooks = srv.URL + "/webhooks/"
	defer func() { discordgo.EndpointWebhooks = old }()
	s, _ := discordgo.New("Bot x")
	s.Client = srv.Client()

	row := pagination.BuildPageRow(2, 3, func(p int) string { return fmt.Sprintf("lbpg|%d|elo", p) }, func(slot string) string { return "lbpg|off-" + slot })
	edit := leaderboardPageEdit(&leaderboardView{png: []byte("PNGDATA"), text: "Page 2/3", components: row})
	if _, err := s.InteractionResponseEdit(&discordgo.Interaction{AppID: "app", Token: "tok"}, edit); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gotType, "multipart/form-data") || string(gotFile) != "PNGDATA" {
		t.Fatalf("request is %q with file %q", gotType, gotFile)
	}
	for _, want := range []string{`"attachments":[]`, `"content":"Page 2/3"`, `"embeds":[]`, `lbpg|1|elo`, `lbpg|3|elo`} {
		if !strings.Contains(gotPayload, want) {
			t.Errorf("the request lacks %s: %s", want, gotPayload)
		}
	}
	// With nothing to draw the embed answers, and the buttons are cleared when there are none.
	e := leaderboardPageEdit(&leaderboardView{embed: &discordgo.MessageEmbed{Title: "Empty"}})
	if len(e.Files) != 0 || e.Components == nil || len(*e.Components) != 0 || len(*e.Embeds) != 1 {
		t.Errorf("an embed answer must have no files, no buttons and one embed: %+v", e)
	}
}
