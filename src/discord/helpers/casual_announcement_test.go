package helpers

import (
	"strings"
	"testing"

	"ranked-bedwars/src/types"
)

func casualGame(winner string) Game {
	return Game{
		ID: 3, Number: 2, Total: 209, Winner: winner, Mode: types.Mode{Name: "[1v1] Practice mode"},
		Players: []GamePlayer{
			{UserID: "11", Username: "Alice", Team: "Team A"},
			{UserID: "22", Username: "Bob", Team: "Team B"},
			{UserID: "33", Username: "Cara", Team: "Team B"},
			{UserID: "44", Username: "Dan", Team: teamUnassigned},
		},
		Teams: []string{"Team A", "Team B"},
	}
}

// A casual game rates nobody, but its winning team is known: it is announced like a scored game, with the winners and the losers.
func TestCasualGameWithAWinnerListsWinnersAndLosers(t *testing.T) {
	vars := embedData{Game: casualGame("team b")}.common() // the winner is matched without regard to case
	if got, want := vars["winnerMentions"], "<@22> <@33>"; got != want {
		t.Fatalf("winnerMentions = %q, want %q", got, want)
	}
	if got, want := vars["loserMentions"], "<@11>"; got != want {
		t.Fatalf("loserMentions = %q, want %q (a player on no team is left out)", got, want)
	}

	emb := renderEmbed("scored", defaultEmbeds["scored"], embedData{Game: casualGame("Team B")})
	if emb == nil {
		t.Fatal("the scored embed rendered nothing")
	}
	for _, want := range []string{"Game #2", "**Winners:** <@22> <@33>", "**Losers:** <@11>"} {
		if !strings.Contains(emb.Description, want) {
			t.Fatalf("the announcement is missing %q:\n%s", want, emb.Description)
		}
	}
}

func TestCasualGameWithoutAWinnerHasNoWinnersLine(t *testing.T) {
	vars := embedData{Game: casualGame("")}.common()
	if vars["winnerMentions"] != "" || vars["loserMentions"] != "" {
		t.Fatalf("no team won, yet winners %q losers %q", vars["winnerMentions"], vars["loserMentions"])
	}
}

// A scored game keeps reading its winners from the results.
func TestScoredGameStillReadsItsResults(t *testing.T) {
	g := casualGame("Team B")
	g.Results = []EloResult{{UserID: "11", Win: true}, {UserID: "22"}}
	vars := embedData{Game: g}.common()
	if vars["winnerMentions"] != "<@11>" || vars["loserMentions"] != "<@22>" {
		t.Fatalf("winners %q losers %q: the results must decide for a scored game", vars["winnerMentions"], vars["loserMentions"])
	}
}
