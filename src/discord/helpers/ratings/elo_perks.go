package ratings

import (
	"math"
	"strings"
)

// ============================================================================

// Perk is one entry of the game config's "perks" array.
type Perk struct {
	Name          string  `json:"name"`
	IsEnabled     bool    `json:"is_enabled"`
	EloMultiplier float64 `json:"elo_multiplier"`
	StartingElo   float64 `json:"starting_elo"`
	// MapVotingMultiplier is how much a map vote of a holder counts (1 = one vote).
	MapVotingMultiplier float64  `json:"map_voting_multiplier"`
	AllowedRoles        []string `json:"allowed_roles"`
	DeniedRoles         []string `json:"denied_roles"`

	// Booster marks a perk gated on the server-booster roles (see MarkBoosters).
	Booster bool `json:"-"`
}

// PerkContext is the perks that apply to one player right now.
type PerkContext struct {
	Perks         []Perk
	BoosterStacks int // 0..2, from the booster roles
	// Err is set when the perks could not be looked up (not when there are none). Scoring and claiming then fail
	// instead of silently paying no grant and no multiplier.
	Err error
}

// QualifiesFor reports whether a member holding roleIDs gets this perk: it must
// be enabled and named, no denied role may be held (denied wins), and an empty
// allowed list means everyone. resolve maps a config entry (a snowflake or a
// role key) to a role ID; nil treats entries as snowflakes.
func (p Perk) QualifiesFor(roleIDs map[string]bool, resolve func(string) string) bool {
	if !p.IsEnabled || strings.TrimSpace(p.Name) == "" {
		return false
	}
	ref := func(s string) string {
		if resolve != nil {
			if r := resolve(s); r != "" {
				return r
			}
		}
		return s
	}
	for _, d := range p.DeniedRoles {
		if d != "" && roleIDs[ref(d)] {
			return false
		}
	}
	allowed := false
	for _, a := range p.AllowedRoles {
		if a == "" {
			continue
		}
		allowed = true
		if roleIDs[ref(a)] {
			return true
		}
	}
	return !allowed
}

// MarkBoosters flags perks whose allowed roles include a booster role.
func MarkBoosters(perks []Perk, boosterRoleIDs ...string) []Perk {
	set := make(map[string]bool, len(boosterRoleIDs))
	for _, id := range boosterRoleIDs {
		if id != "" {
			set[id] = true
		}
	}
	out := make([]Perk, len(perks))
	copy(out, perks)
	for i := range out {
		for _, r := range out[i].AllowedRoles {
			if set[r] {
				out[i].Booster = true
			}
		}
	}
	return out
}

func normMultiplier(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 1
	}
	return math.Round(v*1e6) / 1e6
}

func clampStartingElo(v float64) int {
	if math.IsNaN(v) {
		return 0
	}
	return int(math.Max(0, math.Min(maxPerkStartingElo, math.Round(v))))
}

// betterPerk orders perks: higher multiplier, then higher starting ELO, then name.
func betterPerk(a, b Perk) bool {
	if ma, mb := normMultiplier(a.EloMultiplier), normMultiplier(b.EloMultiplier); ma != mb {
		return ma > mb
	}
	if sa, sb := clampStartingElo(a.StartingElo), clampStartingElo(b.StartingElo); sa != sb {
		return sa > sb
	}
	return a.Name < b.Name
}

func topPerk(perks []Perk) *Perk {
	var best *Perk
	for i := range perks {
		if best == nil || betterPerk(perks[i], *best) {
			best = &perks[i]
		}
	}
	return best
}

func perkFlatBonus(p *Perk) int {
	if p == nil {
		return 0
	}
	m := normMultiplier(p.EloMultiplier)
	if m <= 1 {
		return 0
	}
	return int(math.Round(perkEloBonusPerStep * (m - 1)))
}

// Multiplier is the win-gain multiplier: the single best perk, never stacked.
func (c PerkContext) Multiplier() float64 {
	if p := topPerk(c.Perks); p != nil {
		return normMultiplier(p.EloMultiplier)
	}
	return 1
}

// Names lists the perks' names, for the claim record.
func (c PerkContext) Names() []string {
	out := make([]string, 0, len(c.Perks))
	for _, p := range c.Perks {
		out = append(out, strings.TrimSpace(p.Name))
	}
	return out
}

// StartingGrant is the one-time ELO granted at a player's first ranked game or
// by /claimelo: every perk's configured starting ELO stacks, plus the best
// perk's multiplier-tier bonus once, plus the best booster perk's bonus per
// booster stack.
func (c PerkContext) StartingGrant() int {
	if len(c.Perks) == 0 {
		return 0
	}
	total := 0
	for _, p := range c.Perks {
		total += clampStartingElo(p.StartingElo)
	}
	total += perkFlatBonus(topPerk(c.Perks))
	if stacks := min(max(c.BoosterStacks, 0), maxBoosterStacks); stacks > 0 {
		var boosters []Perk
		for _, p := range c.Perks {
			if p.Booster {
				boosters = append(boosters, p)
			}
		}
		total += perkFlatBonus(topPerk(boosters)) * stacks
	}
	return total
}

// ============================================================================
// Ranks

// MapVoteWeight is how much one map vote of this player counts: their best
// perk's map-vote multiplier (1 without one).
func (c PerkContext) MapVoteWeight() float64 {
	best := 1.0
	for _, p := range c.Perks {
		if p.MapVotingMultiplier > best {
			best = p.MapVotingMultiplier
		}
	}
	return best
}
