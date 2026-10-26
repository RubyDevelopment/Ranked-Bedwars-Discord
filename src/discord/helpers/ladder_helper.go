package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"

	"ranked-bedwars/src/database"
)

// ============================================================================
// Punishment & strike ladders
// ============================================================================

// Every punishment command is driven by a ladder the guild sets up on the dashboard (Moderation > Punishment ladders /
// Strike ladders): the ladder picks the step for the player's offence count, and the step says what is applied and for
// how long. With no ladder set up the commands refuse to run.

// ErrLadderNotConfigured means the guild has not set up the ladder a command needs; its message says what to do.
type ErrLadderNotConfigured struct{ Reason string }

func (e *ErrLadderNotConfigured) Error() string { return e.Reason }

// IsLadderNotConfigured reports whether err is an ErrLadderNotConfigured.
func IsLadderNotConfigured(err error) bool {
	var target *ErrLadderNotConfigured
	return errors.As(err, &target)
}

type ladderStep struct {
	Type            string  `json:"type"`   // punishment ladder
	Action          string  `json:"action"` // strike ladder
	StrikesRequired int     `json:"strikes_required"`
	Duration        float64 `json:"duration"`
	DurationUnit    string  `json:"durationUnit"`
	EloRemoval      int     `json:"elo_removal"`
}

func (s ladderStep) action() string {
	if s.Action != "" {
		return s.Action
	}
	return s.Type
}

// span is the step's duration; 0 means permanent.
func (s ladderStep) span() time.Duration {
	unit := time.Hour * 24
	switch s.DurationUnit {
	case "minutes":
		unit = time.Minute
	case "hours":
		unit = time.Hour
	}
	return time.Duration(s.Duration * float64(unit))
}

type ladderLevel struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	PunishmentType string       `json:"punishmentType"`
	Enabled        *bool        `json:"enabled"`
	Steps          []ladderStep `json:"steps"`
}

type keyedLevel struct {
	Key string
	ladderLevel
}

type ladderConfig struct {
	OffenceMode string                 `json:"offence_mode"` // persistent (default), active_only or season_reset
	Levels      map[string]ladderLevel `json:"levels"`
}

// enabledLevels lists the usable levels (enabled, with at least one step) in a stable order.
func (c *ladderConfig) enabledLevels() []keyedLevel {
	keys := make([]string, 0, len(c.Levels))
	for k := range c.Levels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []keyedLevel
	for _, k := range keys {
		if lvl := c.Levels[k]; (lvl.Enabled == nil || *lvl.Enabled) && len(lvl.Steps) > 0 {
			out = append(out, keyedLevel{k, lvl})
		}
	}
	return out
}

func loadLadder(guildID, field, label string) (*ladderConfig, error) {
	raw, err := database.Must().GuildConfig.Field(guildID, field)
	if err != nil && !sdk.IsNotFound(err) {
		return nil, err
	}
	var cfg ladderConfig
	if err == nil {
		if jerr := json.Unmarshal(raw, &cfg); jerr != nil {
			return nil, jerr
		}
	}
	if len(cfg.enabledLevels()) == 0 {
		return nil, &ErrLadderNotConfigured{Reason: fmt.Sprintf("No %s is set up. Create at least one enabled level with a step on the dashboard (Moderation > %s) before using this command.", label, label)}
	}
	return &cfg, nil
}

// seasonStart is when the active season began, for the season_reset offence mode.
func seasonStart(guildID string) time.Time {
	if season, err := database.Must().Season.Active(guildID); err == nil && season != nil {
		return season.StartsAt
	}
	return time.Time{}
}

// ============================================================================
// Standard punishments
// ============================================================================

// PunishmentPlan is what the punishment ladder prescribes for a player's next offence of one kind.
type PunishmentPlan struct {
	Level  string // ladder level key, stored on the punishment so later offences are counted per level
	Step   int    // 1-based
	Action string // the step's action: warning, chat_mute, vc_mute, queue_ban, server_ban...
	Span   time.Duration
}

// PlanPunishment finds the step the punishment ladder prescribes for the player's next offence under the level whose
// punishment type is ladderType ("queue_ban", "chat_mute"...). Offences are the player's earlier punishments recorded
// under that level, counted per the ladder's offence mode; past the last step the last step repeats.
func PlanPunishment(guildID, playerID, ladderType string) (*PunishmentPlan, error) {
	cfg, lvl, err := punishmentLevel(guildID, ladderType)
	if err != nil {
		return nil, err
	}
	prior, err := database.Must().Punishments.ByPlayer(guildID, playerID, false, "", sdk.Page{})
	if err != nil {
		return nil, err
	}
	since := time.Time{}
	if cfg.OffenceMode == "season_reset" {
		since = seasonStart(guildID)
	}
	now := time.Now()
	count := 0
	for _, p := range prior.Data {
		if p.LadderLevel != lvl.Key || p.IsVoided || p.IsAppealed || p.CreatedAt.Before(since) {
			continue
		}
		if cfg.OffenceMode == "active_only" && (!p.IsEnabled || p.RevokedAt != nil || (p.ExpiresAt != nil && !p.ExpiresAt.After(now))) {
			continue
		}
		count++
	}
	idx := min(count, len(lvl.Steps)-1)
	step := lvl.Steps[idx]
	return &PunishmentPlan{Level: lvl.Key, Step: idx + 1, Action: step.action(), Span: step.span()}, nil
}

// RequirePunishmentLadder fails unless the punishment ladder has an enabled level with steps for ladderType, so a
// command can refuse before it touches anything.
func RequirePunishmentLadder(guildID, ladderType string) error {
	_, _, err := punishmentLevel(guildID, ladderType)
	return err
}

func punishmentLevel(guildID, ladderType string) (*ladderConfig, *keyedLevel, error) {
	cfg, err := loadLadder(guildID, "punishment_ladder", "Punishment ladders")
	if err != nil {
		return nil, nil, err
	}
	for _, lvl := range cfg.enabledLevels() {
		if lvl.PunishmentType == ladderType {
			return cfg, &lvl, nil
		}
	}
	return nil, nil, &ErrLadderNotConfigured{Reason: fmt.Sprintf("The punishment ladder has no enabled level with steps for the %s punishment type. Add one on the dashboard (Moderation > Punishment ladders) before using this command.", strings.ReplaceAll(ladderType, "_", " "))}
}

// ============================================================================
// Strikes
// ============================================================================

// StrikePlan is what the strike ladder prescribes for a player's next strike.
type StrikePlan struct {
	Level      string
	Step       int    // the step the offence count reached (0 = below the first step: the strike is only recorded)
	Action     string // the step's action; an ELO-only action applies no punishment
	Span       time.Duration
	EloRemoval int
}

// PlanStrike finds the step the strike ladder prescribes for the player's next strike. strikeType names the level (its
// key, id or name; the first enabled level when empty); a name that matches no level is refused. The offence count is
// the player's earlier counting strikes under that level plus this one, and the step is the highest whose
// strikes_required it has reached.
func PlanStrike(guildID, playerID, strikeType string, seasonID uint) (*StrikePlan, error) {
	cfg, lvl, err := strikeLevel(guildID, strikeType)
	if err != nil {
		return nil, err
	}
	scope := uint(0)
	if cfg.OffenceMode == "season_reset" {
		scope = seasonID
	}
	prior, err := database.Must().Strikes.AllByPlayer(guildID, playerID, scope, false)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	count := 1 // the strike being issued
	for _, s := range prior {
		if s.LadderLevel != lvl.Key || s.IsVoided || s.IsAppealed {
			continue
		}
		if cfg.OffenceMode == "active_only" && s.ExpiresAt != nil && !s.ExpiresAt.After(now) {
			continue
		}
		count++
	}
	plan := &StrikePlan{Level: lvl.Key}
	best := 0
	for i, step := range lvl.Steps {
		if step.StrikesRequired <= count && step.StrikesRequired >= best {
			best = step.StrikesRequired
			plan.Step, plan.Action, plan.Span, plan.EloRemoval = i+1, step.action(), step.span(), max(0, step.EloRemoval)
		}
	}
	return plan, nil
}

// RequireStrikeLadder fails unless the strike ladder is set up and strikeType names one of its levels, so /strike can
// refuse before it touches anything.
func RequireStrikeLadder(guildID, strikeType string) error {
	_, _, err := strikeLevel(guildID, strikeType)
	return err
}

func strikeLevel(guildID, strikeType string) (*ladderConfig, *keyedLevel, error) {
	cfg, err := loadLadder(guildID, "strike_ladder", "Strike ladders")
	if err != nil {
		return nil, nil, err
	}
	wanted := strings.ToLower(strings.TrimSpace(strikeType))
	for _, lvl := range cfg.enabledLevels() {
		if wanted == "" || strings.ToLower(lvl.Key) == wanted || strings.ToLower(lvl.ID) == wanted || strings.ToLower(lvl.Name) == wanted {
			return cfg, &lvl, nil
		}
	}
	return nil, nil, &ErrLadderNotConfigured{Reason: fmt.Sprintf("The strike ladder has no enabled level matching %q. Use a level name from the dashboard (Moderation > Strike ladders), or leave the type empty for the first level.", strikeType)}
}
