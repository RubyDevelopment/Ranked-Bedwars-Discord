package types

import (
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
)

// PartiesConfig is the guild config's "flows.party" section (the dashboard's
// Party Limits page); older guilds may still carry it in the game config's "party_settings".
type PartiesConfig struct {
	IsEnabled                 *bool   `json:"is_enabled,omitempty"` // nil = enabled
	MaxSize                   int     `json:"max_size"`
	InactivityDisbandTime     int     `json:"inactivity_disband_time"` // minutes, 0 = never
	InviteExpireSeconds       int     `json:"invite_expire_seconds"`
	MinQueueSize              int     `json:"min_queue_size"`
	RequireAllMembersInQueue  bool    `json:"require_all_members_in_queue"`
	MaxEloDifference          int     `json:"max_elo_difference"` // cap on the aggregated party ELO, 0 = disabled
	ExemptBoostersFromEloCap  bool    `json:"exempt_boosters_from_elo_limit"`
	EloAggregationMethod      string  `json:"elo_aggregation_method"` // average, highest, sum, leader
	EloModifierIsEnabled      bool    `json:"elo_modifier_is_enabled"`
	EloModifierMultiplier     float64 `json:"elo_modifier_multiplier"`
	EloModifierGamesThreshold int     `json:"elo_modifier_games_threshold"`
	EloModifierFlatAdjustment int     `json:"elo_modifier_flat_adjustment"`
}

// ModePartyConfig is a per-mode override of PartiesConfig; nil fields inherit.
type ModePartyConfig struct {
	MaxSize                  *int    `json:"max_size"`
	MinQueueSize             *int    `json:"min_queue_size"`
	RequireAllMembersInQueue *bool   `json:"require_all_members_in_queue"`
	MaxEloDifference         *int    `json:"max_elo_difference"`
	ExemptBoostersFromEloCap *bool   `json:"exempt_boosters_from_elo_limit"`
	EloAggregationMethod     *string `json:"elo_aggregation_method"`
}

// PartyAPI is the slice of sdk.PartyService the manager uses (an interface so
// tests can stand in for the database).
type PartyAPI interface {
	Get(guildID string, partyID uint) (*models.PartyView, error)
	ByPlayer(guildID, playerID string) (*models.PartyView, error)
	ListActive(guildID string, inactiveBefore time.Time) ([]models.PartyView, error)
	Create(actor, guildID, leaderID string, maxSize int) (*models.PartyView, error)
	Invite(actor, guildID string, partyID uint, inviterID, inviteeID string, expiresIn time.Duration) (*models.PartyInviteView, error)
	Accept(actor, guildID string, partyID uint, playerID string) (*models.PartyView, error)
	Decline(actor, guildID string, partyID uint, playerID string) error
	Leave(actor, guildID, playerID string) (*models.PartyChange, error)
	Kick(actor, guildID string, partyID uint, playerID, by string) (*models.PartyChange, error)
	TransferLeadership(actor, guildID string, partyID uint, by, to string) (*models.PartyView, error)
	Disband(actor, guildID string, partyID uint, by, reason string) (*models.PartyChange, error)
	SetInvitesOpen(actor, guildID string, partyID uint, open bool) error
	Touch(actor, guildID string, partyID uint) error
	RecordGame(actor, guildID string, partyID uint) error
	ExpireInvites(actor, guildID string) (int64, error)
}

// PartyDeps are the manager's outside dependencies. Every field is optional
// (nil = safe default), which is also what lets tests run without a database.
type PartyDeps struct {
	API            PartyAPI // nil = the process-wide database client's Party service
	Config         func(guildID string) PartiesConfig
	Modes          func(guildID string) []Mode
	Elo            func(guildID, userID string) (elo int, ok bool) // ok=false → unknown, ELO checks fail open
	BoostersExempt func(guildID string, userIDs []string) bool
	InGame         func(guildID, userID string) bool
	Notify         func(guildID, userID string, n PartyNotice)
	Now            func() time.Time
}

// PartyGroup is a party as a match/queue roster sees it.
type PartyGroup struct {
	Leader  string
	Members []string
}

// PartyNotice is a message the caller should deliver to a user (embeds live in
// the command/event layer, not here).
type PartyNotice int
