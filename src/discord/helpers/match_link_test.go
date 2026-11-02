package helpers

import "testing"

// The match page is looked up by the game's number among all the guild's games, never by the database id (which counts
// every server's games) or the season's game number (which repeats).
func TestMatchLinkUsesTheGuildGameNumber(t *testing.T) {
	t.Setenv("DASHBOARD_URL", "https://jartex.myrbw.dev/")

	if got, want := MatchURL("1043269884911550605", 209), "https://jartex.myrbw.dev/public/1043269884911550605/games/209"; got != want {
		t.Fatalf("MatchURL = %q, want %q", got, want)
	}
	if got, want := MatchLink("1043269884911550605", 209, "Game #1"), "[Game #1](<https://jartex.myrbw.dev/public/1043269884911550605/games/209>)"; got != want {
		t.Fatalf("MatchLink = %q, want %q", got, want)
	}
	// A game without a number yet (or no guild) has no page: the label stays plain text.
	if got := MatchURL("1043269884911550605", 0); got != "" {
		t.Fatalf("MatchURL for number 0 = %q, want none", got)
	}
	if got := MatchLink("", 5, "Game #5"); got != "Game #5" {
		t.Fatalf("MatchLink without a guild = %q", got)
	}
}

// A stored game's link must carry the number the database recorded for it.
func TestStoredGameCarriesItsGuildNumber(t *testing.T) {
	rec, rows := storedGame("completed")
	rec.GuildNumber = 209
	m := NewGameManager(GameDeps{API: storedGameAPI{rec: rec, rows: rows}, Elo: NewEloManager(EloDeps{})})
	g, ok, err := m.ByNumberOrStored("3", rec.GameNumber)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if g.Total != 209 {
		t.Fatalf("Total = %d, want the record's guild number 209", g.Total)
	}
}
