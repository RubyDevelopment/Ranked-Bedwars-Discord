package helpers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"

	"ranked-bedwars/src/database"
)

func TestEngineTimelineFrameConversion(t *testing.T) {
	at := time.Date(2026, 10, 23, 20, 0, 0, 0, time.UTC).UnixMilli()
	bridge := &EngineBridge{}
	input := bridge.timelineInput(engineInbound{
		Kind: timelineKill, At: at, Killer: "player-a", Victim: "player-b", Final: true, FirstBlood: true,
	})
	if input.OccurredAt != time.UnixMilli(at).UTC() || *input.Actor != "player-a" || *input.Target != "player-b" {
		t.Fatalf("converted event = %+v", input)
	}
	if input.Final == nil || !*input.Final || input.FirstBlood == nil || !*input.FirstBlood {
		t.Fatalf("kill metadata = %+v", input)
	}
}

func TestEngineEventRoutesToTimelineAPI(t *testing.T) {
	at := time.Date(2026, 10, 23, 20, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/guilds/123456789012345678/games/7/events" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var input timelineEventRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode event input: %v", err)
		}
		if input.Kind != timelineKill || input.OccurredAt != at || input.Actor == nil || *input.Actor != "killer" {
			t.Errorf("input = %+v", input)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1,"guild_id":42,"game_id":7,"kind":"kill","occurred_at":"2026-10-23T20:00:00Z"}`))
	}))
	defer server.Close()

	previous := database.Get()
	database.Set(sdk.NewServices(server.URL, "test-key"))
	t.Cleanup(func() { database.Set(previous) })
	bridge := &EngineBridge{hosted: map[uint]string{7: "123456789012345678"}}
	bridge.handle(engineInbound{Type: "game.event", GameID: 7, Kind: timelineKill, At: at.UnixMilli(), Killer: "killer", Victim: "victim"})
}
