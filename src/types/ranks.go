package types

// EloDecaySetting is the ELO decay a queue applies to players whose last game was in its mode (the dashboard's
// Queues page). The keys are camelCase because they are shared with the guild's older elo_engine.elo_decay.
type EloDecaySetting struct {
	Enabled        bool   `json:"enabled"`
	DecayFloor     int    `json:"decayFloor"`
	InactivityDays int    `json:"inactivityDays"`
	WarningHours   int    `json:"warningHours"`
	IntervalDays   int    `json:"intervalDays"`
	NotifyWarning  *bool  `json:"notifyWarning"`
	NotifyApplied  *bool  `json:"notifyApplied"`
	NotifyVia      string `json:"notifyVia"`
	AlertChannelID string `json:"alertChannelId"`
}

// Rank is one entry of the game config's "ranks" array.
type Rank struct {
	RankName    string `json:"rank_name"`
	MinElo      int    `json:"min_elo"`
	MaxElo      int    `json:"max_elo"`
	KFactorWin  int    `json:"k_factor_win"`
	KFactorLoss int    `json:"k_factor_loss"`
	RoleID      string `json:"role_id"`
	MVPBonus    int    `json:"mvp_bonus"`
	Decay       int    `json:"decay"` // ELO lost per inactivity-decay tick while at this rank
}
