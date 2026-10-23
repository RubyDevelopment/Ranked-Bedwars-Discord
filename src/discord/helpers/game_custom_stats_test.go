package helpers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

func TestCompleteGameWithCustomStatsSendsFractionalParticipantValues(t *testing.T) {
	var method, path, actor string
	var request struct {
		Participants []struct {
			PlayerID    uint               `json:"player_id"`
			CustomStats map[string]float64 `json:"custom_stats"`
		} `json:"participants"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, actor = r.Method, r.URL.Path, r.Header.Get("X-Actor")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	api := sdkGameAPI{sdk.NewServices(server.URL, "test-api-key")}
	_, err := api.CompleteGameWithCustomStats("system", "123", 4, sdk.CompleteGameRequest{
		Status: "completed",
		Participants: []models.GamePlayersModel{
			{PlayerID: 7, Team: "Red"},
		},
	}, map[uint]map[string]float64{
		7: {"diamonds": 1.5},
	})
	if err != nil {
		t.Fatalf("CompleteGameWithCustomStats() error = %v", err)
	}
	if method != http.MethodPost || path != "/v1/guilds/123/games/4/complete" || actor != "system" {
		t.Fatalf("request = %s %s actor %q", method, path, actor)
	}
	if len(request.Participants) != 1 || request.Participants[0].PlayerID != 7 ||
		!reflect.DeepEqual(request.Participants[0].CustomStats, map[string]float64{"diamonds": 1.5}) {
		t.Fatalf("participant request = %#v", request.Participants)
	}
}
