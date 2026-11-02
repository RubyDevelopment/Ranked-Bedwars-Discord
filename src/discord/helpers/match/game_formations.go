package match

import (
	"ranked-bedwars/src/types"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================

// A formation is one way of turning a queue's players into teams. A guild defines as many as it likes (the dashboard's
// Queue Config → Team formations) and each mode picks one by its ID. A mode that picks none keeps the older behaviour:
// captain modes use the guild's captain flow, every other mode its standard flow.
//
// kind is "balanced" (ELO-balanced teams, with a little randomness if wanted), "random" (a plain shuffle) or
// "captains" (two captains draft the rest). A setting a formation leaves out falls back to the guild's flow for it.

const (
	FormationBalanced = "balanced"
	FormationRandom   = "random"
	FormationCaptains = "captains"
)

// Formation is defined in package types (games.go).
type Formation = types.Formation

// formationOf is the formation the mode picked, if it picked one that still exists.
func (f FlowSettings) formationOf(m types.Mode) (Formation, bool) {
	id := strings.TrimSpace(m.FormationID)
	if id == "" {
		return Formation{}, false
	}
	for _, x := range f.Formations {
		if x.ID == id {
			return x, true
		}
	}
	return Formation{}, false
}

// IsCaptain reports whether games of the mode are drafted by captains.
func (f FlowSettings) IsCaptain(m types.Mode) bool {
	if x, ok := f.formationOf(m); ok {
		return x.Kind == FormationCaptains
	}
	return isCaptainMode(m)
}

// StandardFor is the automatic team assignment settings for the mode: its formation's on top of the guild's standard flow.
func (f FlowSettings) StandardFor(m types.Mode) StandardFlow {
	s := f.Standard
	x, ok := f.formationOf(m)
	if !ok && isRandomMode(m) {
		s.IsEnabled = true
		s.RandomnessPercent = 100
		return s
	}
	if !ok || x.Kind == FormationCaptains {
		return s
	}
	s.IsEnabled = true
	s.RandomnessPercent = 0
	if x.Kind == FormationRandom {
		s.RandomnessPercent = 100
	}
	if x.RandomnessPercent != nil && x.Kind != FormationRandom {
		s.RandomnessPercent = min(100, max(0, *x.RandomnessPercent))
	}
	if x.KeepPartyTogether != nil {
		s.KeepPartyTogether = *x.KeepPartyTogether
	}
	if x.BalancePartyGroups != nil {
		s.BalancePartyGroups = *x.BalancePartyGroups
	}
	if x.AllowSplitParties != nil {
		s.AllowSplitParties = *x.AllowSplitParties
	}
	if x.MaxPartySize != nil {
		s.MaxPartySize = x.MaxPartySize
	}
	return s
}

// CaptainFor is the draft settings for the mode: its formation's on top of the guild's captain flow.
func (f FlowSettings) CaptainFor(m types.Mode) CaptainFlow {
	c := f.Captain
	x, ok := f.formationOf(m)
	if !ok || x.Kind != FormationCaptains {
		return c
	}
	c.IsEnabled = true
	if x.PickTimeoutSeconds != nil {
		c.PickTimeoutSeconds = *x.PickTimeoutSeconds
	}
	if x.TopEloCaptainChance != nil {
		c.TopEloCaptainChance = *x.TopEloCaptainChance
	}
	if x.AutoAssignParties != nil {
		c.AutoAssignParties = *x.AutoAssignParties
	}
	if x.OneCaptainPerParty != nil {
		c.OneCaptainPerParty = *x.OneCaptainPerParty
	}
	if x.DefaultPickingType != "" {
		c.DefaultPickingType = x.DefaultPickingType
	}
	if x.AlternatingMinPlayers != nil {
		c.AlternatingMinPlayers = *x.AlternatingMinPlayers
	}
	if x.MaxPartySize != nil {
		c.MaxPartySize = x.MaxPartySize
	}
	return c
}

// embedIsEmpty says whether an embed would show nothing worth a box (Discord refuses one too): no title, text, fields, author or footer.
func embedIsEmpty(e *discordgo.MessageEmbed) bool {
	return strings.TrimSpace(e.Title) == "" && strings.TrimSpace(e.Description) == "" && len(e.Fields) == 0 && e.Author == nil && (e.Footer == nil || strings.TrimSpace(e.Footer.Text) == "") && e.Image == nil && e.Thumbnail == nil
}

// ============================================================================
// Startup recovery
