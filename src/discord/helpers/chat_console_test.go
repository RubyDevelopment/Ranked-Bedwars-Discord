package helpers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// An action posted to /live/control reaches the engine as bot.control, and the engine's bot.result is the answer; a bot.event from
// the engine reaches a viewer of that bot.
func TestLiveConsoleRoundTrip(t *testing.T) {
	t.Setenv("LIVE_CHAT_SECRET", "s3cret")
	Engine.secret = "bridge"
	srv := httptest.NewServer(http.HandlerFunc(Engine.serveWS))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), http.Header{"Authorization": {"Bearer bridge"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for i := 0; i < 100 && !Engine.Connected(); i++ {
		time.Sleep(10 * time.Millisecond)
	}

	viewer, stop := LiveChat.Subscribe("123456789012345678", "BotOne")
	defer stop()
	<-viewer.out // the initial status

	// The fake engine: answers the first bot.control, and logs a GUI opening.
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame map[string]any
			_ = json.Unmarshal(raw, &frame)
			if frame["type"] != "bot.control" {
				continue
			}
			_ = conn.WriteJSON(map[string]any{"type": "bot.event", "guild_id": frame["guild_id"], "bot": "BotOne", "kind": "window_open", "at": 1, "data": map[string]any{"title": "Menu"}})
			_ = conn.WriteJSON(map[string]any{"type": "bot.result", "request_id": frame["request_id"], "guild_id": frame["guild_id"], "bot": "BotOne", "ok": true, "data": map[string]any{"echo": frame["action"]}})
		}
	}()

	req := httptest.NewRequest(http.MethodPost, "/live/control", strings.NewReader(`{"guild":"123456789012345678","bot":"BotOne","action":"chat","params":{"message":"hi"}}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	rec := httptest.NewRecorder()
	LiveChat.ServeControl(rec, req)
	var got controlResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || !got.OK || !strings.Contains(string(got.Data), `"chat"`) {
		t.Fatalf("result %s (%v)", rec.Body.String(), err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-viewer.out:
			if e.name == "status" {
				continue // status frames (engine up, bots) can arrive in between
			}
			if e.name != "event" || !strings.Contains(string(e.data), "window_open") {
				t.Errorf("viewer got %s %s", e.name, e.data)
			}
			return
		case <-deadline:
			t.Fatal("the viewer never got the bot.event")
		}
	}
}
