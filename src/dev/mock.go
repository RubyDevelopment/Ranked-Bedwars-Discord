// Package dev is the bot's development mode: with DEV_MODE=true it fills the database with a mock guild (ranks, modes,
// maps, bots, a season, role assignments, command and event configs) and registered mock players, so every command can
// be tried without registering or configuring anything first.
//
// Run a scratch Ranked-Bedwars-Database API (go run . in that repo, with DATABASE_DRIVER=sqlite,
// DATABASE_DSN=file:dev.db and MASTER_API_KEY=devmaster), then start the bot with:
//
//	DEV_MODE=true
//	DEV_GUILD_ID=<the Discord server to test in>
//	DEV_USER_ID=<your Discord user id>        (you are registered as DEV_USERNAME, default "DevPlayer")
//	DATABASE_API_URL=http://127.0.0.1:8080
//	DATABASE_API_KEY=devmaster
package dev

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Switch
// ============================================================================

// Enabled reports whether the bot runs in development mode.
func Enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DEV_MODE")))
	return v == "1" || v == "true" || v == "yes"
}

// ============================================================================
// Mock Data
// ============================================================================

// mockRanks is the rank ladder of the mock guild.
var mockRanks = []types.Rank{
	{RankName: "Bronze", MinElo: 0, MaxElo: 999, KFactorWin: 30, KFactorLoss: -15, RoleID: "900000000000000101", MVPBonus: 5},
	{RankName: "Silver", MinElo: 1000, MaxElo: 1199, KFactorWin: 25, KFactorLoss: -20, RoleID: "900000000000000102", MVPBonus: 5},
	{RankName: "Gold", MinElo: 1200, MaxElo: 1399, KFactorWin: 22, KFactorLoss: -22, RoleID: "900000000000000103", MVPBonus: 5, Decay: 5},
	{RankName: "Diamond", MinElo: 1400, MaxElo: 1599, KFactorWin: 20, KFactorLoss: -25, RoleID: "900000000000000104", MVPBonus: 5, Decay: 10},
	{RankName: "Emerald", MinElo: 1600, MaxElo: 99999, KFactorWin: 18, KFactorLoss: -28, RoleID: "900000000000000105", MVPBonus: 5, Decay: 15},
}

func mockModes() []types.Mode {
	mode := func(name, stable string, teams, per int) types.Mode {
		return types.Mode{
			Name: name, StableID: stable, Type: "standard", TeamCount: teams, PlayersPerTeam: per, MaxPlayers: teams * per,
			IsEnabled: true, IsEloGainEnabled: true, IsStatsTrackingEnabled: true, IsPartyQueueEnabled: true,
		}
	}
	return []types.Mode{mode("4v4", "mock-4v4", 2, 4), mode("3v3", "mock-3v3", 2, 3), mode("2v2", "mock-2v2", 2, 2)}
}

func mockMaps() []types.Map {
	teams := []string{"Red", "Blue"}
	return []types.Map{
		{Name: "Amazon", Teams: teams, IsEnabled: true, IsMapAvailable: true, StableID: "mock-amazon"},
		{Name: "Waterfall", Teams: teams, IsEnabled: true, IsMapAvailable: true, StableID: "mock-waterfall"},
		{Name: "Lighthouse", Teams: teams, IsEnabled: true, IsMapAvailable: true, StableID: "mock-lighthouse"},
	}
}

func mockBots() []types.Bot {
	return []types.Bot{
		{Username: "RBW_Bot1", Status: "available", IsEnabled: true, Tier: "standard"},
		{Username: "RBW_Bot2", Status: "available", IsEnabled: true, Tier: "premium"},
		{Username: "RBW_Bot3", Status: "offline", IsEnabled: true, Tier: "standard"},
	}
}

// mockRoles are the guild role assignments: stand-in snowflakes, so nothing needs to exist in Discord.
func mockRoles() map[string]types.DiscordRole {
	role := func(id, name string) types.DiscordRole { return types.DiscordRole{ID: id, Name: name} }
	return map[string]types.DiscordRole{
		"guild_role_registered": role("900000000000000001", "Registered"),
		"guild_role_premium":    role("900000000000000002", "Premium"),
		"guild_role_pugs":       role("900000000000000003", "PUGS"),
		"guild_role_pups":       role("900000000000000004", "PUPS"),
	}
}

type mockPlayer struct {
	username                                       string
	elo, wins, losses, kills, deaths, beds, finals int
}

// mockPlayers are the registered players besides the developer, spread across the rank ladder.
var mockPlayers = []mockPlayer{
	{"Notch_Fan", 1710, 62, 21, 410, 190, 120, 233},
	{"BedBreaker99", 1580, 48, 25, 352, 201, 140, 190},
	{"SweatLord", 1495, 40, 27, 301, 180, 98, 170},
	{"CasualCarl", 1310, 28, 30, 190, 205, 60, 101},
	{"PixelPanda", 1260, 25, 24, 175, 160, 55, 93},
	{"LavaLarry", 1150, 19, 27, 140, 190, 40, 71},
	{"NoobSlayer", 1085, 14, 20, 99, 140, 28, 52},
	{"FreshSpawn", 940, 8, 15, 60, 98, 15, 30},
	{"TeamKiller", 870, 6, 18, 41, 88, 9, 21},
	{"AFKAndy", 760, 3, 14, 22, 70, 4, 10},
}

// ============================================================================
// Seeding
// ============================================================================

// Seed fills the database for guildID with the mock guild and players. It is safe to run again: what exists is kept.
func Seed(guildID, devUserID string) error {
	svc := database.Get()
	if svc == nil {
		return fmt.Errorf("database service not initialized")
	}
	ctx := context.Background()
	actor := sdk.SystemActor
	client := svc.Client()

	// 1. Guild (also creates its config, state and game config rows)
	if _, _, err := svc.Guild.Create(actor, guildID, "Mock Guild", ""); err != nil {
		return fmt.Errorf("guild: %w", err)
	}

	// 2. Game config: ranks, modes, maps and bots (each collection is synced to its own table by name)
	for field, value := range map[string]any{"ranks": mockRanks, "modes": mockModes(), "maps": mockMaps(), "bots": mockBots()} {
		if err := svc.GameConfig.SaveField(actor, guildID, field, value); err != nil {
			return fmt.Errorf("game config %s: %w", field, err)
		}
	}

	// 3. Role assignments
	if _, err := svc.GuildState.Patch(actor, guildID, map[string]any{"roles": mockRoles()}); err != nil {
		return fmt.Errorf("guild roles: %w", err)
	}

	// 4. Season
	season, err := svc.Season.Active(guildID)
	if err != nil {
		if !sdk.IsNotFound(err) {
			return fmt.Errorf("season: %w", err)
		}
		if season, err = client.CreateSeason(ctx, actor, guildID, sdk.CreateSeasonRequest{Name: "Season 1", Description: "Mock season", MakeActive: true}); err != nil {
			return fmt.Errorf("create season: %w", err)
		}
	}

	// 5. Command and event configs (the dashboard and the requirement checks read these)
	if err := helpers.SeedCommandConfigs(guildID); err != nil {
		return fmt.Errorf("command configs: %w", err)
	}
	if err := helpers.SeedEventConfigs(guildID); err != nil {
		return fmt.Errorf("event configs: %w", err)
	}

	// 6. Players: the developer first, then the mock field
	devName := strings.TrimSpace(os.Getenv("DEV_USERNAME"))
	if devName == "" {
		devName = "DevPlayer"
	}
	if devUserID != "" {
		if err := seedPlayer(guildID, devUserID, mockPlayer{devName, 1250, 12, 8, 90, 70, 25, 40}, season.ID); err != nil {
			return err
		}
	}
	for idx, p := range mockPlayers {
		if err := seedPlayer(guildID, strconv.Itoa(900000000000001000+idx), p, season.ID); err != nil {
			return err
		}
	}
	return nil
}

// seedPlayer registers one player (a no-op when they already are) and writes their season stats.
func seedPlayer(guildID, discordID string, p mockPlayer, seasonID uint) error {
	svc := database.Must()
	reg, err := svc.Player.Register(sdk.SystemActor, guildID, discordID, p.username, seasonID)
	if err != nil {
		if sdk.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("register %s: %w", p.username, err)
	}
	games := p.wins + p.losses
	rank := mockRanks[0].RankName
	for _, r := range mockRanks {
		if p.elo >= r.MinElo && p.elo <= r.MaxElo {
			rank = r.RankName
		}
	}
	_, err = svc.Client().UpdatePlayerStats(context.Background(), sdk.SystemActor, guildID, sdk.UintToString(reg.PlayerID), seasonID, map[string]any{
		"elo": p.elo, "highest_elo": p.elo + 25, "rank": rank, "games_played": games, "wins": p.wins, "losses": p.losses,
		"kills": p.kills, "deaths": p.deaths, "beds_destroyed": p.beds, "final_kills": p.finals,
		"win_streak": 2, "highest_win_streak": 6,
	})
	if err != nil {
		return fmt.Errorf("stats for %s: %w", p.username, err)
	}
	return nil
}
