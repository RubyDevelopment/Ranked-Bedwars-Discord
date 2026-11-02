package types

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
	Color       string `json:"color"` // the rank's colour (#rrggbb), for banners
}
