package types

import (
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
)

// Adjustment is the outcome of a manual ELO change.
type Adjustment struct {
	Before, After         int
	RankBefore, RankAfter string
}

// Claim is a paid starting grant, as stored on the player's season stats.
type Claim struct {
	*Adjustment
	Amount int
	Perks  []string
	At     time.Time
}

// DecayNoticeKind says what a DecayNotice announces.
type DecayNoticeKind int

// DecayResult is one applied decay.
type DecayResult struct {
	ID            uint
	Amount        int
	Before, After int
}

// EloAPI is the slice of the database SDK the manager uses. Players are
// identified by Discord snowflake; stats are for the active season.
type EloAPI interface {
	Stats(guildID, userID string) (*models.PlayerStatsModel, error) // get-or-create
	SetElo(actor, guildID, userID string, seasonID uint, elo int) (*models.PlayerStatsModel, error)
	Patch(actor, guildID, userID string, seasonID uint, fields map[string]any) (*models.PlayerStatsModel, error)
	Reset(actor, guildID, userID string, seasonID uint, startElo int, rank string) (*models.PlayerStatsModel, error)
	DecayCandidates(guildID string, inactiveDays int) ([]models.PlayerStatsModel, error)
	Snowflake(guildID string, playerID uint) (string, error)
	CreateDecay(actor, guildID, userID string, d models.PlayerDecaysModel) (uint, error)
	GetDecay(guildID string, id uint) (*models.PlayerDecaysModel, error)
	DeleteDecay(actor, guildID string, id uint) error
}

// EloOutcome is one player's result for one game.
type EloOutcome struct {
	Before, After, Change int
	Gain                  int // perk-adjusted win ELO (0 on a loss)
	Loss                  int // ELO actually lost, after the floor at 0 (0 on a win)
	MVPBonus              int
	PerkStart             int // starting grant paid out with this game
	RankBefore, RankAfter string
}

// EloResult is one player's scored result, shaped for the participant row
// (Before/After/Change) and the results canvas (the breakdown).
type EloResult struct {
	UserID   string
	SeasonID uint
	Win, MVP bool
	EloOutcome
}

// MVPCandidate is one player's performance in a game.
type MVPCandidate struct {
	ID                  string
	Kills, Deaths, Beds int
}

// Preview is what n staff-granted wins or losses are worth to a player.
type Preview struct {
	Count, Total       int // Total is the summed magnitude of the ELO change
	Start, End         int
	RankStart, RankEnd string
}

// ScoreInput is a finished game to score.
type ScoreInput struct {
	GuildID         string
	Winners, Losers []string
	MVP             string       // must be a winner; "" = none (see SelectMVP)
	Parties         []PartyGroup // groups captured at queue time
	Mode            *Mode        // nil = ranked; a mode without ELO gain scores no change
}
