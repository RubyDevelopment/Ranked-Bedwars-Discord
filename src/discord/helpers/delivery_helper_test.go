package helpers

import (
	"encoding/json"
	"testing"
)

func TestDeliverySettingsAreReadFromTheProfile(t *testing.T) {
	raw := json.RawMessage(`{"profile":{"delivery":{"results":{"channel_id":"111","mode":"builtin"},"alerts":{"channel_id":"222","role_id":"333","events":{"game_voided":false,"queue_denied":true}}}}}`)
	f := parseGuildFlow(raw)
	if f.resultsChannel("999") != "111" || f.Delivery.ResultsMode != "builtin" {
		t.Fatalf("results delivery: %+v", f.Delivery)
	}
	if !f.alertOn("queue_denied") || f.alertOn("game_voided") || !f.alertOn("tracking_lost") {
		t.Fatalf("alerts: queue_denied=%v game_voided=%v tracking_lost(unlisted, on)=%v", f.alertOn("queue_denied"), f.alertOn("game_voided"), f.alertOn("tracking_lost"))
	}
}

func TestNoAlertsChannelMeansNoAlerts(t *testing.T) {
	f := parseGuildFlow(json.RawMessage(`{"profile":{"delivery":{"alerts":{"events":{"queue_denied":true}}}}}`))
	if f.alertOn("queue_denied") {
		t.Fatal("an alert with no channel must not be sent")
	}
	if got := f.resultsChannel("999"); got != "999" {
		t.Fatalf("results default to the game's chat, got %q", got)
	}
}
