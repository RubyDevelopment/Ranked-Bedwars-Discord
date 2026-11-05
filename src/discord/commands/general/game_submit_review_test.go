package general

import (
	"strings"
	"testing"

	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/discord/helpers/proofread"

	"github.com/bwmarrin/discordgo"
)

func reviewGame() match.Game {
	return match.Game{ID: 28508, Number: 277, Teams: []string{"Team A", "Team B"}, Players: []match.GamePlayer{
		{UserID: "1", Username: "bummyyyyy", Team: "Team A"}, {UserID: "2", Username: "EGIRLMEI", Team: "Team A"},
		{UserID: "3", Username: "deloid_AK", Team: "Team B"}, {UserID: "4", Username: "HT206", Team: "Team B"},
	}}
}

func TestReviewShowsWinnersLosersMVPAndConfirmButton(t *testing.T) {
	res := proofread.Result{Winner: "Team A", WinnerBy: "the win message in chat", MVP: proofread.Member{Name: "EGIRLMEI", Team: "Team A"}, MVPBy: "most kills (3)"}
	var text strings.Builder
	for _, f := range reviewFields(reviewGame(), res) {
		text.WriteString(f.Name + " " + f.Value + "\n")
	}
	for _, want := range []string{"Team A", "<@1> <@2>", "Team B", "<@3> <@4>", "<@2> · most kills (3)"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("missing %q in:\n%s", want, text.String())
		}
	}
	if got := mvpOf(reviewGame(), res); got != "2" {
		t.Errorf("mvp %q", got)
	}
}

func TestConfirmButtonNeedsAWinner(t *testing.T) {
	count := func(res proofread.Result) int {
		return len(reviewButtons(reviewGame(), res)[0].(discordgo.ActionsRow).Components)
	}
	if n := count(proofread.Result{}); n != 1 {
		t.Errorf("%d buttons without a winner, want only the manual one", n)
	}
	if n := count(proofread.Result{Winner: "Team A"}); n != 2 {
		t.Errorf("%d buttons with a winner, want confirm and manual", n)
	}
	ok := reviewButtons(reviewGame(), proofread.Result{Winner: "Team A", MVP: proofread.Member{Name: "EGIRLMEI"}})[0].(discordgo.ActionsRow).Components[0].(*discordgo.Button)
	if want := "subscore|ok|28508|Team A|2"; ok.CustomID != want {
		t.Errorf("confirm id %q, want %q", ok.CustomID, want)
	}
}

func TestAutoScoreOnlyTrustsAClearReading(t *testing.T) {
	game := reviewGame() // four players
	seen := func(n int) []proofread.Member { return make([]proofread.Member, n) }
	clear := proofread.Result{Winner: "Team A", Seen: seen(3)}
	for name, tc := range map[string]struct {
		res  proofread.Result
		team string
		want string // a part of the reason, "" when it may score
	}{
		"clear":          {clear, "Team A", ""},
		"no winner":      {proofread.Result{Seen: seen(4)}, "Team A", "could not be read"},
		"other team":     {clear, "Team B", "Team B submitted"},
		"too few seen":   {proofread.Result{Winner: "Team A", Seen: seen(1)}, "Team A", "1 of 4"},
		"flagged by the": {proofread.Result{Winner: "Team A", Seen: seen(4), Mismatch: "x"}, "Team A", "submitted"},
	} {
		got := autoScoreBlocker(game, tc.res, tc.team, 50)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
