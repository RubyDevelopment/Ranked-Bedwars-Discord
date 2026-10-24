package helpers

import (
	"strings"
	"testing"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models/base"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"
)

func TestBuildStrikeHistoryFieldsShowsStatusAndExpiration(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour)
	descriptions := BuildStrikeHistoryDescriptionsDirect([]models.PlayerStrikesModel{
		{
			Identity:  base.Identity{ID: 123},
			StaffID:   "456",
			Reason:    "Example reason",
			ExpiresAt: &expiresAt,
			IsEnabled: true,
		},
	}, 0)
	if len(descriptions) != 1 {
		t.Fatalf("expected one strike description, got %d", len(descriptions))
	}
	description := descriptions[0]
	if !strings.HasPrefix(description, DotGreen) {
		t.Errorf("active strike should use green dot, got %q", description)
	}
	if !strings.Contains(description, "**Strike #123 | Active**") {
		t.Errorf("description %q does not show strike status", description)
	}
	if strings.Contains(description, "**ID:**") {
		t.Errorf("description %q still contains the ID row", description)
	}
	issuedIndex := strings.Index(description, "**Issued:**")
	expiresIndex := strings.Index(description, "**Expires:**")
	if issuedIndex < 0 || expiresIndex <= issuedIndex {
		t.Errorf("expiration should appear under issued; description: %q", description)
	}
	if !strings.Contains(description, DiscordRelativeTimestamp(expiresAt)) {
		t.Errorf("description %q does not show the expiration time", description)
	}
}

func TestBuildPunishmentHistoryFieldsShowsStatusAndExpiration(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour)
	descriptions := BuildPunishmentHistoryDescriptionsDirect([]models.PlayerPunishmentsModel{
		{
			Identity:  base.Identity{ID: 456},
			Type:      "ban",
			StaffID:   "789",
			Reason:    "Example reason",
			ExpiresAt: &expiresAt,
			IsEnabled: true,
		},
	}, 0)
	if len(descriptions) != 1 {
		t.Fatalf("expected one punishment description, got %d", len(descriptions))
	}
	description := descriptions[0]
	if !strings.HasPrefix(description, DotGreen) {
		t.Errorf("active punishment should use green dot, got %q", description)
	}
	if !strings.Contains(description, "**Ban #456 | Active**") {
		t.Errorf("description %q does not show punishment status", description)
	}
	if strings.Contains(description, "**ID:**") {
		t.Errorf("description %q still contains the ID row", description)
	}
	issuedIndex := strings.Index(description, "**Issued:**")
	expiresIndex := strings.Index(description, "**Expires:**")
	if issuedIndex < 0 || expiresIndex <= issuedIndex {
		t.Errorf("expiration should appear under issued; description: %q", description)
	}
	if !strings.Contains(description, DiscordRelativeTimestamp(expiresAt)) {
		t.Errorf("description %q does not show the expiration time", description)
	}
}

func TestBuildGameHistoryFieldsUsesCompactResultLayout(t *testing.T) {
	startedAt := time.Now().Add(-7 * time.Minute)
	entry := sdk.GameHistory{
		GamePlayersModel: models.GamePlayersModel{
			GameID:        42,
			EloBefore:     560,
			EloAfter:      585,
			EloChange:     25,
			Kills:         6,
			Deaths:        12,
			BedsDestroyed: 0,
			IsWinner:      true,
		},
		Game: &models.GamesModel{
			Identity:   base.Identity{ID: 42},
			GameNumber: 25363,
			MapName:    "Lectus",
			ModeName:   "3v3",
			Status:     "completed",
			StartedAt:  &startedAt,
		},
	}
	players := map[uint][]GameHistoryPlayer{
		42: {
			{Username: "StephenSon_18", IsWinner: true},
			{Username: "KHANGAMERYT2", IsWinner: true},
			{Username: "Zavvian", IsWinner: true},
			{Username: "SMXLC"},
			{Username: "LOMXD"},
			{Username: "Searchcry"},
		},
	}

	descriptions := buildGameHistoryDescriptions([]sdk.GameHistory{entry}, 0, players)
	if len(descriptions) != 1 {
		t.Fatalf("expected one game description, got %d", len(descriptions))
	}
	description := descriptions[0]
	if !strings.HasPrefix(description, DotGreen) {
		t.Errorf("won game should use green dot, got %q", description)
	}
	for _, want := range []string{"Game #25363", "Lectus", "3v3", "<t:"} {
		if !strings.Contains(description, want) {
			t.Errorf("description %q does not contain %q", description, want)
		}
	}
	for _, want := range []string{
		"> **Recap:** `6` Kills, `12` Deaths, `0` Beds",
		"> **ELO:** `560` → `585` · gained +25",
		"> **Winners:** `StephenSon_18`, `KHANGAMERYT2`, `Zavvian`",
		"> **Losers:** `SMXLC`, `LOMXD`, `Searchcry`",
	} {
		if !strings.Contains(description, want) {
			t.Errorf("description %q does not contain %q", description, want)
		}
	}
}

func TestBuildGameHistoryFieldsUsesRedForLossAndGrayForVoided(t *testing.T) {
	startedAt := time.Now()
	entries := []sdk.GameHistory{
		{
			GamePlayersModel: models.GamePlayersModel{GameID: 1},
			Game: &models.GamesModel{
				Identity: base.Identity{ID: 1}, GameNumber: 1, Status: "completed", StartedAt: &startedAt,
			},
		},
		{
			GamePlayersModel: models.GamePlayersModel{GameID: 2, IsWinner: true},
			Game: &models.GamesModel{
				Identity: base.Identity{ID: 2}, GameNumber: 2, Status: "voided", StartedAt: &startedAt,
			},
		},
	}

	descriptions := BuildGameHistoryDescriptionsDirect(entries, 0)
	if len(descriptions) != 2 {
		t.Fatalf("expected two game descriptions, got %d", len(descriptions))
	}
	if !strings.HasPrefix(descriptions[0], DotRed) {
		t.Errorf("lost game should use red dot, got %q", descriptions[0])
	}
	if !strings.HasPrefix(descriptions[1], DotGray) {
		t.Errorf("voided game should use gray dot, got %q", descriptions[1])
	}
}

func TestBuildGameHistoryShowsTieWhenNoPlayersWon(t *testing.T) {
	startedAt := time.Now()
	entry := sdk.GameHistory{
		GamePlayersModel: models.GamePlayersModel{GameID: 3},
		Game: &models.GamesModel{
			Identity:   base.Identity{ID: 3},
			GameNumber: 3,
			Status:     "completed",
			StartedAt:  &startedAt,
		},
	}
	players := map[uint][]GameHistoryPlayer{
		3: {
			{Username: "PlayerOne"},
			{Username: "PlayerTwo"},
		},
	}

	descriptions := buildGameHistoryDescriptions([]sdk.GameHistory{entry}, 0, players)
	if len(descriptions) != 1 {
		t.Fatalf("expected one game description, got %d", len(descriptions))
	}
	description := descriptions[0]
	if !strings.HasPrefix(description, DotYellow) {
		t.Errorf("tied game should use yellow dot, got %q", description)
	}
	if !strings.Contains(description, "> **Tied:** `PlayerOne`, `PlayerTwo`") {
		t.Errorf("description %q does not list tied players", description)
	}
	if strings.Contains(description, "**Losers:**") {
		t.Errorf("tied game should not list losers: %q", description)
	}
}

func TestHistoryDotsUseGrayForVoidedAndRedForInactiveEntries(t *testing.T) {
	expiredAt := time.Now().Add(-time.Hour)
	strikes := BuildStrikeHistoryDescriptionsDirect([]models.PlayerStrikesModel{
		{Identity: base.Identity{ID: 1}, IsVoided: true},
		{Identity: base.Identity{ID: 2}, IsEnabled: true, ExpiresAt: &expiredAt},
	}, 0)
	if !strings.HasPrefix(strikes[0], DotGray) || !strings.HasPrefix(strikes[1], DotRed) {
		t.Errorf("unexpected strike dots: %q, %q", strikes[0], strikes[1])
	}

	punishments := BuildPunishmentHistoryDescriptionsDirect([]models.PlayerPunishmentsModel{
		{Identity: base.Identity{ID: 1}, Type: "ban", IsVoided: true},
		{Identity: base.Identity{ID: 2}, Type: "ban", IsEnabled: false},
	}, 0)
	if !strings.HasPrefix(punishments[0], DotGray) || !strings.HasPrefix(punishments[1], DotRed) {
		t.Errorf("unexpected punishment dots: %q, %q", punishments[0], punishments[1])
	}

	decays := BuildDecayHistoryDescriptionsDirect([]models.PlayerDecaysModel{
		{Identity: base.Identity{ID: 1}},
		{Identity: base.Identity{ID: 2}, IsVoided: true},
	}, 0)
	if !strings.HasPrefix(decays[0], DotGreen) || !strings.HasPrefix(decays[1], DotGray) {
		t.Errorf("unexpected decay dots: %q, %q", decays[0], decays[1])
	}
}

func TestHistoryEmbedsRenderEntriesInDescription(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	strike := models.PlayerStrikesModel{Identity: base.Identity{ID: 1}, IsEnabled: true}
	punishment := models.PlayerPunishmentsModel{Identity: base.Identity{ID: 2}, Type: "ban", IsEnabled: true}
	decay := models.PlayerDecaysModel{Identity: base.Identity{ID: 3}}
	gameTime := time.Now()
	game := sdk.GameHistory{
		GamePlayersModel: models.GamePlayersModel{GameID: 4, IsWinner: true},
		Game: &models.GamesModel{
			Identity: base.Identity{ID: 4}, GameNumber: 4, Status: "completed", StartedAt: &gameTime,
		},
	}
	punishment.ExpiresAt = &expiresAt

	embeds := []*discordgo.MessageEmbed{
		BuildStrikeHistoryEmbed("player", []models.PlayerStrikesModel{strike}, 0),
		BuildPunishmentHistoryEmbed("player", []models.PlayerPunishmentsModel{punishment}, 0, false),
		BuildDecayHistoryEmbed("player", []models.PlayerDecaysModel{decay}, 0),
		BuildGameHistoryEmbedWithPlayers("player", []sdk.GameHistory{game}, 0, nil),
	}
	for _, embed := range embeds {
		if len(embed.Fields) != 0 {
			t.Errorf("history embed should not use fields, got %d", len(embed.Fields))
		}
		if !strings.Contains(embed.Description, "\n\n") {
			t.Errorf("history entries should be appended to the description after the summary: %q", embed.Description)
		}
	}
}
