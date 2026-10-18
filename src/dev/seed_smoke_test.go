package dev

import (
	"os"
	"testing"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
)

// TestSeedAgainstLiveAPI runs the dev seeding through the SDK against a real Ranked-Bedwars-Database API.
// It is skipped unless SMOKE_DATABASE_API_URL and SMOKE_DATABASE_API_KEY point at a scratch instance.
func TestSeedAgainstLiveAPI(t *testing.T) {
	url, key := os.Getenv("SMOKE_DATABASE_API_URL"), os.Getenv("SMOKE_DATABASE_API_KEY")
	if url == "" || key == "" {
		t.Skip("set SMOKE_DATABASE_API_URL and SMOKE_DATABASE_API_KEY to run against a scratch database API")
	}
	svc := database.New(url, key)
	if err := svc.Health(); err != nil {
		t.Fatalf("health: %v", err)
	}
	database.Set(svc)
	t.Cleanup(func() { database.Set(nil) })

	const guild = "100000000000000001"
	for run := 1; run <= 2; run++ { // the second run proves seeding is idempotent
		if err := Seed(guild, "200000000000000002"); err != nil {
			t.Fatalf("seed run %d: %v", run, err)
		}
	}
	ranks, err := helpers.GuildMetaRanks(guild)
	if err != nil || len(ranks) != len(mockRanks) {
		t.Fatalf("ranks = %d (want %d), err %v", len(ranks), len(mockRanks), err)
	}
	if modes, err := helpers.GuildMetaModes(guild); err != nil || len(modes) == 0 {
		t.Fatalf("modes = %d, err %v", len(modes), err)
	}
	if roles, err := helpers.GuildStateRoles(guild); err != nil || len(roles) == 0 {
		t.Fatalf("roles = %d, err %v", len(roles), err)
	}
	season, err := svc.Season.Active(guild)
	if err != nil {
		t.Fatalf("active season: %v", err)
	}
	board, err := svc.Stats.Leaderboard(guild, season.ID, sdkPage())
	if err != nil || len(board.Data) < len(mockPlayers) {
		t.Fatalf("leaderboard rows = %v, err %v", board, err)
	}
}

func sdkPage() sdk.Page { return sdk.Page{Limit: 50} }
