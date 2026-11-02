package ratings

import (
	"errors"
	"time"
)

// ============================================================================
// ELO decay
// ============================================================================

const (
	// SeasonStartElo is the rating a fresh or wiped season row starts at. It is
	// never the lowest rank's min_elo (that once spawned players at e.g. 800).
	SeasonStartElo = 0

	maxPerkStartingElo   = 5000
	maxBoosterStacks     = 2
	perkEloBonusPerStep  = 100            // flat ELO per +1.0 of multiplier
	defaultDecayWarning  = 12 * time.Hour // how long before a decay the player is warned
	defaultDecayInterval = 24 * time.Hour // how often a decay repeats once it has started
)

var (
	ErrNoRanks           = errors.New("no ranks configured")
	ErrInvalidRank       = errors.New("rank has an invalid K-factor")
	ErrNoStartingGrant   = errors.New("no perk grants starting ELO")
	ErrStartingGrantUsed = errors.New("starting ELO already claimed or games already played")
)

// ============================================================================
// Perks
