package ratings

import (
	"fmt"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
	"strings"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
)

// ============================================================================

// RankFor returns the rank covering elo: the highest tier whose min_elo the
// rating has reached, the lowest tier for anything below the ladder, nil when
// no ranks are configured.
func RankFor(ranks []types.Rank, elo int) *types.Rank {
	if len(ranks) == 0 {
		return nil
	}
	sorted := helpers.SortedRanks(ranks)
	if r := helpers.RankForElo(sorted, elo); r != nil {
		return r
	}
	return &sorted[0]
}

func rankKFactors(r *types.Rank) (win, loss int, err error) {
	win, loss = r.KFactorWin, r.KFactorLoss
	if loss < 0 {
		loss = -loss
	}
	if win <= 0 || loss <= 0 {
		return 0, 0, fmt.Errorf("%w: %q", ErrInvalidRank, r.RankName)
	}
	return win, loss, nil
}

// ValidateLadder lists everything wrong with a rank ladder (empty = usable).
func ValidateLadder(ranks []types.Rank) []string {
	if len(ranks) == 0 {
		return []string{"Add at least one competitive rank"}
	}
	var issues []string
	for i, r := range ranks {
		label := strings.TrimSpace(r.RankName)
		if label == "" {
			issues = append(issues, fmt.Sprintf("rank #%d is missing a name", i+1))
			label = fmt.Sprintf("rank #%d", i+1)
		}
		if r.MinElo < 0 {
			issues = append(issues, fmt.Sprintf("%q needs a min ELO of 0 or higher", label))
		}
		if r.MaxElo != 0 && r.MaxElo <= r.MinElo {
			issues = append(issues, fmt.Sprintf("%q needs a max ELO above its min ELO (or leave it open on the top tier)", label))
		}
		if r.KFactorWin <= 0 {
			issues = append(issues, fmt.Sprintf("%q needs a win K-factor above 0", label))
		}
		if r.KFactorLoss == 0 {
			issues = append(issues, fmt.Sprintf("%q needs a loss K-factor above 0", label))
		}
		if r.MVPBonus < 0 {
			issues = append(issues, fmt.Sprintf("%q needs an MVP bonus of 0 or higher", label))
		}
		if r.Decay < 0 {
			issues = append(issues, fmt.Sprintf("%q needs a decay of 0 or higher", label))
		}
		if strings.TrimSpace(r.RoleID) == "" {
			issues = append(issues, fmt.Sprintf("%q needs a Discord role assigned", label))
		}
	}
	sorted := helpers.SortedRanks(ranks)
	if sorted[0].MinElo > 0 {
		issues = append(issues, fmt.Sprintf("lowest rank %q must start at ELO 0", sorted[0].RankName))
	}
	for i := 0; i+1 < len(sorted); i++ {
		cur, next := sorted[i], sorted[i+1]
		switch {
		case cur.MaxElo == 0:
			issues = append(issues, fmt.Sprintf("%q needs a max ELO (only the top tier may leave it open)", cur.RankName))
		case cur.MaxElo+1 < next.MinElo:
			issues = append(issues, fmt.Sprintf("gap between %q and %q: raise %q max ELO to %d", cur.RankName, next.RankName, cur.RankName, next.MinElo-1))
		case cur.MaxElo >= next.MinElo:
			issues = append(issues, fmt.Sprintf("%q and %q overlap", cur.RankName, next.RankName))
		}
	}
	return issues
}

// ============================================================================
// Scoring maths (pure)
// ============================================================================

// SyncRank makes the stored rank name match the player's ELO and returns it.
func (m *EloManager) SyncRank(actor, guildID, userID string) (string, error) {
	st, err := m.API().Stats(guildID, userID)
	if err != nil {
		return "", err
	}
	return m.syncRank(actor, guildID, userID, st, st.Elo)
}

func (m *EloManager) syncRank(actor, guildID, userID string, st *models.PlayerStatsModel, elo int) (string, error) {
	ranks, err := m.deps.Ranks(guildID)
	if err != nil {
		return st.Rank, err
	}
	r := RankFor(ranks, elo)
	if r == nil || r.RankName == "" {
		return st.Rank, nil
	}
	if r.RankName != st.Rank {
		if _, err := m.API().Patch(actor, guildID, userID, st.SeasonID, map[string]any{"rank": r.RankName}); err != nil {
			return st.Rank, err
		}
	}
	// The hook runs on every ELO change, not only a rank change: the nickname shows the ELO.
	m.mu.RLock()
	hook := m.rankSync
	m.mu.RUnlock()
	if hook != nil {
		hook(guildID, userID, st.Rank, r.RankName)
	}
	return r.RankName, nil
}

// ============================================================================
// Manual adjustments (staff /win, /loss, /modify, /wipe)
