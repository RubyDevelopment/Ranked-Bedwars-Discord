package helpers

import (
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
)

// A strike step can take ELO (see PlanStrike). Like decay, it is its own part of the rating: games and modifications
// are never rewritten, and removing the strike gives back exactly what it took.

// StrikePenalty takes up to amount ELO off the player as a strike penalty (the database never takes the rating below
// 0) and returns the result; Before-After is what was actually taken.
func (m *EloManager) StrikePenalty(actor, guildID, userID string, amount int) (*Adjustment, error) {
	defer m.lock(guildID, userID)()
	st, err := m.api().Stats(guildID, userID)
	if err != nil {
		return nil, err
	}
	return m.applyLocked(actor, guildID, userID, st, sdk.EloEffect{Part: "strike", Delta: max(0, amount), Reason: "strike"})
}

// RefundStrike gives back the ELO a removed strike took, if it was taken from the player's current season.
func (m *EloManager) RefundStrike(actor, guildID, userID string, strike *models.PlayerStrikesModel) error {
	taken := -strike.EloChange
	if taken <= 0 {
		return nil
	}
	defer m.lock(guildID, userID)()
	st, err := m.api().Stats(guildID, userID)
	if err != nil || st.SeasonID != strike.SeasonID {
		return err
	}
	_, err = m.applyLocked(actor, guildID, userID, st, sdk.EloEffect{Part: "strike", Delta: -taken, Reason: "strike removed"})
	return err
}
