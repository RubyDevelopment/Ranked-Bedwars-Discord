package helpers

import (
	"errors"
	"testing"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models/base"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

type rejectedVoidGameAPI struct {
	GameAPI
	err error
}

type forceVoidListingGameAPI struct {
	GameAPI
	games []models.GamesModel
	err   error
}

func (a rejectedVoidGameAPI) EndGame(string, string, uint, string) (*models.GamesModel, error) {
	return nil, a.err
}

func (a forceVoidListingGameAPI) ListForceVoidGames(string) ([]models.GamesModel, error) {
	return a.games, a.err
}

func TestVoidLiveKeepsGameWhenDatabaseRejectsStatusChange(t *testing.T) {
	wantErr := errors.New("database unavailable")
	manager := NewGameManager(GameDeps{API: rejectedVoidGameAPI{err: wantErr}})
	live := &liveGame{g: Game{ID: 42, GuildID: "guild", State: GameInGame}}
	manager.mu.Lock()
	manager.games[live.g.ID] = live
	manager.mu.Unlock()

	if err := manager.Void("staff", "guild", live.g.ID, "reason", VoidOptions{}); !errors.Is(err, wantErr) {
		t.Fatalf("Void() error = %v, want %v", err, wantErr)
	}
	got, ok := manager.Get(live.g.ID)
	if !ok {
		t.Fatal("game was removed after the database rejected the void")
	}
	if got.State != GameInGame {
		t.Fatalf("game state = %q, want %q", got.State, GameInGame)
	}
	live.mu.Lock()
	busy := live.busy
	live.mu.Unlock()
	if busy {
		t.Fatal("game remained busy after the failed database write")
	}
}

func TestForceVoidCandidatesIncludesOpenAndCompletedGames(t *testing.T) {
	manager := NewGameManager(GameDeps{API: forceVoidListingGameAPI{games: []models.GamesModel{
		{Identity: base.Identity{ID: 1}, GuildID: 7, SeasonID: 3, GameNumber: 1, Status: string(types.GameStatusWaiting), ModeName: "Doubles"},
		{Identity: base.Identity{ID: 2}, GuildID: 7, SeasonID: 3, GameNumber: 2, Status: string(types.GameStatusInProgress), ModeName: "Doubles"},
		{Identity: base.Identity{ID: 3}, GuildID: 7, SeasonID: 3, GameNumber: 3, Status: string(types.GameStatusCompleted), ModeName: "Solo"},
		{Identity: base.Identity{ID: 4}, GuildID: 7, SeasonID: 3, GameNumber: 4, Status: string(types.GameStatusVoided), ModeName: "Solo"},
		{Identity: base.Identity{ID: 5}, GuildID: 7, SeasonID: 3, GameNumber: 5, Status: string(types.GameStatusCancelled), ModeName: "Solo"},
	}}})

	live := &liveGame{g: Game{ID: 2, Number: 2, GuildID: "7", State: GamePostGame, Mode: types.Mode{Name: "Doubles"}}}
	manager.mu.Lock()
	manager.games[live.g.ID] = live
	manager.mu.Unlock()

	candidates, err := manager.ForceVoidCandidates("7")
	if err != nil {
		t.Fatalf("ForceVoidCandidates() error = %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("ForceVoidCandidates() returned %d games, want 3", len(candidates))
	}
	if candidates[0].ID != 1 || candidates[0].State != GameWaiting {
		t.Fatalf("waiting game candidate = %+v", candidates[0])
	}
	if candidates[1].ID != 2 || candidates[1].State != GamePostGame {
		t.Fatalf("live game candidate = %+v, want live snapshot state %q", candidates[1], GamePostGame)
	}
	if candidates[2].ID != 3 || candidates[2].State != GameCleanup {
		t.Fatalf("completed game candidate = %+v", candidates[2])
	}
}

func TestWithGuildFooterIconForGuildUsesServerIconAndPreservesCustomIcon(t *testing.T) {
	session := &discordgo.Session{State: discordgo.NewState()}
	guild := &discordgo.Guild{ID: "guild", Icon: "server-icon"}
	if err := session.State.GuildAdd(guild); err != nil {
		t.Fatalf("add guild to state: %v", err)
	}

	defaultIcon := &discordgo.MessageEmbed{Footer: &discordgo.MessageEmbedFooter{Text: "Match"}}
	customIcon := &discordgo.MessageEmbed{Footer: &discordgo.MessageEmbedFooter{Text: "Match", IconURL: "https://example.com/custom.png"}}
	WithGuildFooterIconForGuild(session, guild.ID, defaultIcon, customIcon)

	if want := guild.IconURL("128"); defaultIcon.Footer.IconURL != want {
		t.Fatalf("default footer icon = %q, want %q", defaultIcon.Footer.IconURL, want)
	}
	if customIcon.Footer.IconURL != "https://example.com/custom.png" {
		t.Fatalf("custom footer icon was replaced: %q", customIcon.Footer.IconURL)
	}
}
