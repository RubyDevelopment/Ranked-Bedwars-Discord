package helpers

import (
	"errors"
	"testing"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/bwmarrin/discordgo"
)

type rejectedVoidGameAPI struct {
	GameAPI
	err error
}

func (a rejectedVoidGameAPI) EndGame(string, string, uint, string) (*models.GamesModel, error) {
	return nil, a.err
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
