package types

import (
	"encoding/json"
	"testing"
)

func TestGameChatLinePreservesMinecraftFormatting(t *testing.T) {
	const motd = "§7Welcome, §eRubyRBW §7to §6JartexNetwork"
	data, err := json.Marshal(GameChatLine{At: 123, Text: "Welcome, RubyRBW to JartexNetwork", Motd: motd})
	if err != nil {
		t.Fatal(err)
	}

	var decoded GameChatLine
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Motd != motd {
		t.Fatalf("motd changed during JSON round trip: got %q, want %q", decoded.Motd, motd)
	}
}
