package main

import "testing"

func TestNewKey(t *testing.T) {
	for in, want := range map[string]string{
		"games/123/45-discord-abc123.json": "games/123/45/discord-abc123.json",
		"games/123/45-bot.json":            "games/123/45/bot.json",
		"games/123/45-game-Zx9.json":       "games/123/45/game-Zx9.json",
		"tickets/123/999-abc.json":         "tickets/123/999-abc.json",
		"games/123/other.json":             "games/123/other.json",
	} {
		if got := newKey(in); got != want {
			t.Errorf("newKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSQLQuote(t *testing.T) {
	if got := sqlQuote(`a'b\c`); got != `'a''b\\c'` {
		t.Errorf("got %s", got)
	}
}
