package helpers

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models/base"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/utility"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
)

// storedGameAPI serves one stored game; everything else is left unimplemented on purpose.
type storedGameAPI struct {
	GameAPI
	rec  *models.GamesModel
	rows []models.GamePlayersModel
}

func (f storedGameAPI) GameByNumber(_ string, n int) (*models.GamesModel, error) {
	if n != f.rec.GameNumber {
		return nil, &sdk.APIError{Status: 404}
	}
	return f.rec, nil
}
func (f storedGameAPI) Participants(string, uint) ([]models.GamePlayersModel, error) {
	return f.rows, nil
}
func (f storedGameAPI) UserID(_ string, playerID uint) (string, error) {
	return fmt.Sprintf("user-%d", playerID), nil
}

// The shape the old bot archived: teams is an object, which does not fit Game.Teams, and the mode is embedded.
const storedGameMetadata = `{"engine_state":"CLEANUP","flow":"CAPTAIN","mode":{"name":"3v3 PPP Captain","type":"captain",
"team_count":2,"players_per_team":3,"max_players":6,"is_elo_gain_enabled":true,"is_stats_tracking_enabled":true},
"players":[{"user_id":"user-1","username":"Alice"}],"teams":{"team a":{"name":"Team A"}},"winners":{"name":"Team B"}}`

func storedGame(status string) (*models.GamesModel, []models.GamePlayersModel) {
	started := time.Date(2026, 11, 1, 22, 40, 0, 0, time.UTC)
	ended := time.Date(2026, 11, 1, 23, 2, 50, 0, time.UTC)
	bot := "Clairo"
	rec := &models.GamesModel{
		Identity: base.Identity{ID: 27675}, GuildID: 3, SeasonID: 5, GameNumber: 27468, MapName: "Arcadium",
		ModeName: "3v3 PPP Captain", Status: status, BotName: &bot, RuntimeMetadata: utility.JSON(storedGameMetadata),
		StartedAt: &started, EndedAt: &ended, Timestamps: base.Timestamps{CreatedAt: started.Add(-5 * time.Minute), UpdatedAt: ended},
	}
	var rows []models.GamePlayersModel
	for i := uint(1); i <= 6; i++ {
		team := "Team A"
		if i > 3 {
			team = "Team B"
		}
		rows = append(rows, models.GamePlayersModel{PlayerID: i, Team: team, IsWinner: i > 3, IsMvp: i == 5, IsCaptain: i == 1 || i == 4})
	}
	return rec, rows
}

// A finished game is gone from the live registry (after CleanupDelay, and for good when the bot restarts), so /viewgame
// has to rebuild it from its rows.
func TestByNumberOrStoredRebuildsAFinishedGame(t *testing.T) {
	rec, rows := storedGame("completed")
	m := NewGameManager(GameDeps{API: storedGameAPI{rec: rec, rows: rows}, Elo: NewEloManager(EloDeps{})})

	if _, ok := m.ByNumber("3", 27468); ok {
		t.Fatal("the game must not be live")
	}
	g, ok, err := m.ByNumberOrStored("3", 27468)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if g.ID != 27675 || g.Number != 27468 || g.Map != "Arcadium" || g.Mode.Name != "3v3 PPP Captain" || g.Bot != "Clairo" {
		t.Fatalf("game header wrong: %+v", g)
	}
	if g.State != GameCleanup {
		t.Fatalf("state = %q, want %q", g.State, GameCleanup)
	}
	if g.Winner != "Team B" || g.MVP != "user-5" {
		t.Fatalf("winner %q mvp %q", g.Winner, g.MVP)
	}
	if len(g.Players) != 6 || len(g.Teams) != 2 || g.Teams[0] != "Team A" {
		t.Fatalf("players %d teams %v", len(g.Players), g.Teams)
	}
	if g.CreatedAt.IsZero() || g.StartedAt.IsZero() {
		t.Fatal("times are not filled in")
	}
}

func TestByNumberOrStoredVoidedAndMissing(t *testing.T) {
	rec, rows := storedGame("voided")
	m := NewGameManager(GameDeps{API: storedGameAPI{rec: rec, rows: rows}, Elo: NewEloManager(EloDeps{})})

	g, ok, err := m.ByNumberOrStored("3", 27468)
	if err != nil || !ok || g.State != GameVoided || g.VoidReason == "" {
		t.Fatalf("voided game: ok=%v err=%v state=%q reason=%q", ok, err, g.State, g.VoidReason)
	}
	if _, ok, err := m.ByNumberOrStored("3", 1); ok || err != nil {
		t.Fatalf("a game that does not exist: ok=%v err=%v", ok, err)
	}
}

var errStopAtReverse = errors.New("stop at reverse")

func (f storedGameAPI) GetGame(string, uint) (*models.GamesModel, error) { return f.rec, nil }
func (f storedGameAPI) ReverseGame(_, _ string, _ uint, _ []uint, _ bool) (int, error) {
	return 0, errStopAtReverse
}

// /unvoid, /rescore and /partialvoid work on finished games, which are not in the live registry. Each one has to get a
// stored game through every check and up to the point where it reverses the results (what follows needs the rating
// database). Rows are casual so no starting-grant lookup runs.
func TestStoredGameOperationsReachTheReversal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		run    func(m *GameManager) error
	}{
		{"unvoid", "voided", func(m *GameManager) error { _, err := m.Unvoid("actor", "3", 27675); return err }},
		{"rescore", "completed", func(m *GameManager) error { _, err := m.Rescore("actor", "3", 27675, "Team A", "user-2"); return err }},
		{"partialvoid", "completed", func(m *GameManager) error { _, err := m.PartialVoid("actor", "3", 27675, ""); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, rows := storedGame(tc.status)
			for i := range rows {
				rows[i].Scoring = "casual"
			}
			m := NewGameManager(GameDeps{API: storedGameAPI{rec: rec, rows: rows}, Elo: NewEloManager(EloDeps{})})
			err := tc.run(m)
			if !errors.Is(err, errStopAtReverse) {
				t.Fatalf("got %v, want the run to reach the reversal", err)
			}
		})
	}
}
