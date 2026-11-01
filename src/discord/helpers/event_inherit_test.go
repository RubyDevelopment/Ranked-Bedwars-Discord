package helpers

import (
	"testing"

	"ranked-bedwars/src/types"
)

func TestEventInheritsWhatCommandsAlreadyHave(t *testing.T) {
	commands := map[string]types.CommandConfig{
		"mute":  {RequiredChannels: map[string]types.RequirementSlot{"chat_mute_logs": {ID: "111"}}, RequiredRoles: map[string]types.RequirementSlot{"chat_muted_role": {ID: "222"}}},
		"unban": {RequiredChannels: map[string]types.RequirementSlot{"server_ban_logs": {}}},
	}
	event := types.EventConfig{
		RequiredChannels: map[string]types.RequirementSlot{"chat_mute_logs": {}, "server_ban_logs": {}, "warn_logs": {ID: "keep"}},
		RequiredRoles:    map[string]types.RequirementSlot{"chat_muted_role": {Required: true}},
	}
	if !inheritCommandAssignments(&event, commands) {
		t.Fatal("expected a change")
	}
	if got := event.RequiredChannels["chat_mute_logs"].ID; got != "111" {
		t.Fatalf("chat_mute_logs = %q, want 111", got)
	}
	if got := event.RequiredRoles["chat_muted_role"]; got.ID != "222" || !got.Required {
		t.Fatalf("chat_muted_role = %+v, want id 222 and still required", got)
	}
	if event.RequiredChannels["server_ban_logs"].ID != "" || event.RequiredChannels["warn_logs"].ID != "keep" {
		t.Fatal("an unassigned key stays empty and an existing assignment is kept")
	}
	if inheritCommandAssignments(&event, commands) {
		t.Fatal("a second pass has nothing left to copy")
	}
}
