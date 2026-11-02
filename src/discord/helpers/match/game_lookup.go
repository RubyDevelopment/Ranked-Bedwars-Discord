package match

import (
	"ranked-bedwars/src/types"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// ============================================================================

// ByNumber finds a live game by its per-season number.
func (m *GameManager) ByNumber(guildID string, number int) (Game, bool) {
	for _, g := range m.GuildGames(guildID) {
		if g.Number == number {
			return g, true
		}
	}
	return Game{}, false
}

// FindByNumber finds a live game or, for staff recovery actions, a stored game
// in the active season.
func (m *GameManager) FindByNumber(guildID string, number int) (Game, bool, error) {
	if game, ok := m.ByNumber(guildID, number); ok {
		return game, true, nil
	}
	rec, err := m.api().GameByNumber(guildID, number)
	if err != nil {
		if isNotFound(err) {
			return Game{}, false, nil
		}
		return Game{}, false, err
	}
	return Game{ID: rec.ID, Number: rec.GameNumber, GuildID: guildID, SeasonID: rec.SeasonID}, true, nil
}

// ByNumberOrStored finds a game by its per-season number: a live one, else the stored game rebuilt from its database
// rows. A finished game leaves the live registry (and every game does when the bot restarts), but its rows stay. ok is
// false when the active season has no such game.
func (m *GameManager) ByNumberOrStored(guildID string, number int) (g Game, ok bool, err error) {
	if g, ok := m.ByNumber(guildID, number); ok {
		return g, true, nil
	}
	rec, err := m.api().GameByNumber(guildID, number)
	if err != nil {
		if isNotFound(err) {
			return Game{}, false, nil
		}
		return Game{}, false, err
	}
	g, rows, err := m.loadStored(guildID, rec)
	if err != nil {
		// A mode that is no longer configured must not hide the game: show what the row itself says.
		g = Game{ID: rec.ID, Number: rec.GameNumber, Total: rec.GuildNumber, GuildID: guildID, SeasonID: rec.SeasonID, Map: rec.MapName, Mode: types.Mode{Name: rec.ModeName}}
		rows = nil
	}
	switch rec.Status {
	case string(types.GameStatusVoided):
		g.State = GameVoided
	case string(types.GameStatusCompleted):
		g.State = GameCleanup
	case string(types.GameStatusInProgress):
		g.State = GameInGame
	default:
		g.State = GameWaiting
	}
	for i, r := range rows { // g.Players is in the order of rows
		if r.IsWinner && g.Winner == "" {
			g.Winner = r.Team
		}
		if r.IsMvp && g.MVP == "" && i < len(g.Players) {
			g.MVP = g.Players[i].UserID
		}
	}
	g.CreatedAt = rec.CreatedAt
	if rec.StartedAt != nil {
		g.StartedAt = *rec.StartedAt
	}
	g.StateAt = rec.UpdatedAt
	if rec.EndedAt != nil {
		g.StateAt = *rec.EndedAt
	}
	if rec.BotName != nil {
		g.Bot = *rec.BotName
	}
	if g.State == GameVoided && g.VoidReason == "" {
		g.VoidReason = "Not recorded"
	}
	return g, true, nil
}

func isNotFound(err error) bool { return sdk.IsNotFound(err) }

// ============================================================================
// Team Formation
