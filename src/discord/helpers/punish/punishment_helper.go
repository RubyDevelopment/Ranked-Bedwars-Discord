package punish

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/base"
	"ranked-bedwars/src/types"
	baseutility "ranked-bedwars/src/utility"
)

// Shared plumbing of the restriction-style punishment commands (warn, kick, chat mute, queue ban, voice mute, server
// ban and their reversals): the specs each command declares, the ladders that drive them, the apply/revoke flows, the
// roles they hand out, the expiry sweep and the alerts they send.

// ============================================================================
// Specs
// ============================================================================

// DiscordAction is defined in package types (punishments.go).
type DiscordAction = types.DiscordAction

// ApplySpec describes how one punishment command is recorded and announced.
type ApplySpec struct {
	Action      string   // "chat mute" - used in "You cannot %s yourself"
	Adjective   string   // "chat muted" - used in "already %s"
	Summary     string   // "the chat mute" - used in the success description
	Title       string   // "Chat Mute Applied"
	Type        string   // punishment type stored in the database
	ActiveTypes []string // types that count as already active; defaults to Type
	Duration    bool     // whether the command accepts a duration
	Duplicates  bool     // whether an already-active punishment blocks a new one

	// ReasonOptional lets the command run without a reason; it is then recorded as "No reason provided".
	ReasonOptional bool

	// Ladder is the punishment-ladder type (queue_ban, chat_mute, vc_mute, server_ban, warning) whose level decides what
	// this command applies and for how long. Empty = the command applies its own action (kick).
	Ladder string

	// SkipHierarchy skips the role hierarchy check for punishments that never touch the member's roles.
	SkipHierarchy bool

	// RequireMember blocks targets who are not currently in the server.
	RequireMember bool
	// MemberActive reports whether the member already carries the Discord-side punishment.
	MemberActive func(member *discordgo.Member) bool

	// Command is the command whose settings apply: its log channels, and the role named by RoleKey.
	Command string
	// RoleKey, when set, means the punishment hands the target a role (taken off again on revoke): the one chosen for
	// Ladder in the punishment ladder settings, else the command's own role requirement (see LadderRole).
	RoleKey string

	Discord     DiscordAction
	AppliedNote string // appended when Discord reports the action applied
	FailedNote  string // appended when it does not
}

// RevokeSpec describes how one punishment reversal is recorded and announced.
type RevokeSpec struct {
	Action      string   // "Chat Mute" - shown in the success embed
	Adjective   string   // "chat muted" - used in "not currently %s"
	Type        string   // punishment type to remove
	ActiveTypes []string // types that count as active; defaults to Type
	RecordType  string   // optional history entry written after a revoke
	Latest      bool     // revoke only the newest active punishment (warnings), not all of them

	// DiscordActive reports whether the Discord-side punishment is still in effect.
	DiscordActive func(s *discordgo.Session, guildID, targetID string) bool
	Discord       DiscordAction

	// Command names the command (the one that applies this punishment) whose log channels to use.
	Command string
	// RoleKey, when set, means the punishment handed out a role, which is taken off again; Ladder names the punishment
	// ladder type whose role that is (see LadderRole).
	RoleKey string
	Ladder  string
}

func (spec *ApplySpec) activeTypes() []string {
	if len(spec.ActiveTypes) > 0 {
		return spec.ActiveTypes
	}
	return []string{spec.Type}
}

func (spec *RevokeSpec) activeTypes() []string {
	if len(spec.ActiveTypes) > 0 {
		return spec.ActiveTypes
	}
	return []string{spec.Type}
}

// ============================================================================
// Ladder configuration
// ============================================================================

// Every punishment command is driven by a ladder the guild sets up on the dashboard (Moderation > Punishment ladders /
// Strike ladders): the ladder picks the step for the player's offence count, and the step says what is applied and for
// how long. With no ladder set up the commands refuse to run.

// ErrLadderNotConfigured means the guild has not set up the ladder a command needs; its message says what to do.
type ErrLadderNotConfigured struct{ Reason string }

func (e *ErrLadderNotConfigured) Error() string { return e.Reason }

type ladderStep struct {
	Type            string  `json:"type"`   // punishment ladder
	Action          string  `json:"action"` // strike ladder
	StrikesRequired int     `json:"strikes_required"`
	Duration        float64 `json:"duration"`
	DurationUnit    string  `json:"durationUnit"`
	EloRemoval      int     `json:"elo_removal"`
	// Also lists further actions applied together with the step's own action.
	Also []ladderAction `json:"also"`
}

// ladderAction is one extra action of a step (same fields as the step's own action).
type ladderAction struct {
	Action       string  `json:"action"`
	Type         string  `json:"type"`
	Duration     float64 `json:"duration"`
	DurationUnit string  `json:"durationUnit"`
	EloRemoval   int     `json:"elo_removal"`
}

// PlannedAction is an extra action a ladder step applies together with its main one.
type PlannedAction struct {
	Action     string
	Span       time.Duration
	EloRemoval int
}

// extras returns the step's additional actions with an ELO-removal action's amount carried in EloRemoval.
func (s ladderStep) extras() []PlannedAction {
	var out []PlannedAction
	for _, a := range s.Also {
		action := a.Action
		if action == "" {
			action = a.Type
		}
		if action == "" {
			continue
		}
		span := ladderStep{Duration: a.Duration, DurationUnit: a.DurationUnit}.span()
		out = append(out, PlannedAction{Action: action, Span: span, EloRemoval: max(0, a.EloRemoval)})
	}
	return out
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
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Enabled *bool        `json:"enabled"`
	Steps   []ladderStep `json:"steps"`
}

type keyedLevel struct {
	Key string
	ladderLevel
}

type ladderConfig struct {
	OffenceMode string                 `json:"offence_mode"` // persistent (default), active_only or season_reset
	Levels      map[string]ladderLevel `json:"levels"`
	Roles       map[string]string      `json:"roles"` // punishment ladder: the role chat_mute, vc_mute and queue_ban each hand out
}

// handles reports whether one of the level's steps applies the given action, which is what makes the level usable
// from that action's command (a level that warns and then mutes shows up for both /warn and /mute).
func (l ladderLevel) handles(action string) bool {
	for _, step := range l.Steps {
		if step.action() == action {
			return true
		}
	}
	return false
}

// matches reports whether name is the level's key, id or name, ignoring case.
func (l keyedLevel) matches(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.ToLower(l.Key) == name || strings.ToLower(l.ID) == name || strings.ToLower(l.Name) == name
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

func LoadLadder(guildID, field, label string) (*ladderConfig, error) {
	if database.Get() == nil {
		return nil, &ErrLadderNotConfigured{Reason: "The database is not available."}
	}
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
// Punishment ladder
// ============================================================================

// PunishmentPlan is what the punishment ladder prescribes for a player's next offence of one kind.
type PunishmentPlan struct {
	Level  string // ladder level key, stored on the punishment so later offences are counted per level
	Step   int    // 1-based
	Action string // the step's action: warning, chat_mute, vc_mute, queue_ban, server_ban...
	Span   time.Duration
	Also   []PlannedAction // further actions applied together with Action
}

// PlanPunishment finds the step the punishment ladder prescribes for the player's next offence under the level named
// levelName (the first level that has a step for ladderType - "queue_ban", "chat_mute"... - when empty). Offences are
// the player's earlier punishments recorded under that level, counted per the ladder's offence mode; past the last step
// the last step repeats.
func PlanPunishment(guildID, playerID, ladderType, levelName string) (*PunishmentPlan, error) {
	cfg, lvl, err := punishmentLevel(guildID, ladderType, levelName)
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
	// A level can mix actions (warn, then mute, then ban). A command only walks the steps of its own action, counting
	// only its own earlier punishments, so /ban never lands on a chat mute step.
	steps := lvl.Steps[:0:0]
	for _, st := range lvl.Steps {
		if st.action() == ladderType {
			steps = append(steps, st)
		}
	}
	ownType := ""
	if sp := ladderSpecs[ladderType]; sp != nil {
		ownType = sp.Type
	}
	now := time.Now()
	count := 0
	for _, p := range prior.Data {
		if p.LadderLevel != lvl.Key || p.IsVoided || p.IsAppealed || p.CreatedAt.Before(since) || (ownType != "" && p.Type != ownType) {
			continue
		}
		if cfg.OffenceMode == "active_only" && (!p.IsEnabled || p.RevokedAt != nil || (p.ExpiresAt != nil && !p.ExpiresAt.After(now))) {
			continue
		}
		count++
	}
	idx := min(count, len(steps)-1)
	step := steps[idx]
	return &PunishmentPlan{Level: lvl.Key, Step: idx + 1, Action: step.action(), Span: step.span(), Also: step.extras()}, nil
}

// RequirePunishmentLadder fails unless the punishment ladder has an enabled level (the one named levelName, if given)
// with a step for ladderType, so a command can refuse before it touches anything.
func RequirePunishmentLadder(guildID, ladderType, levelName string) error {
	_, _, err := punishmentLevel(guildID, ladderType, levelName)
	return err
}

func punishmentLevel(guildID, ladderType, levelName string) (*ladderConfig, *keyedLevel, error) {
	cfg, err := LoadLadder(guildID, "punishment_ladder", "Punishment ladders")
	if err != nil {
		return nil, nil, err
	}
	label := strings.ReplaceAll(ladderType, "_", " ")
	if strings.TrimSpace(levelName) == "" {
		for _, lvl := range cfg.enabledLevels() {
			if lvl.handles(ladderType) {
				return cfg, &lvl, nil
			}
		}
		return nil, nil, &ErrLadderNotConfigured{Reason: fmt.Sprintf("The punishment ladder has no enabled level with a %s step. Add one on the dashboard (Moderation > Punishment ladders) before using this command.", label)}
	}
	for _, lvl := range cfg.enabledLevels() {
		if !lvl.matches(levelName) {
			continue
		}
		if !lvl.handles(ladderType) {
			return nil, nil, &ErrLadderNotConfigured{Reason: fmt.Sprintf("The level %q has no %s step, so this command cannot be used for it. Pick a level from the list, or use the command that matches its steps.", lvl.Name, label)}
		}
		return cfg, &lvl, nil
	}
	return nil, nil, &ErrLadderNotConfigured{Reason: fmt.Sprintf("The punishment ladder has no enabled level named %q. Pick one from the list (Moderation > Punishment ladders on the dashboard).", levelName)}
}

// PunishmentLevelOption is the optional "level" option every ladder punishment command takes: the offence category
// whose steps decide the punishment.
func PunishmentLevelOption() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Name:         "level",
		Description:  "Offence category from the punishment ladder (defaults to the first one for this punishment)",
		Type:         discordgo.ApplicationCommandOptionString,
		Required:     false,
		Autocomplete: true,
	}
}

// PunishmentDurationOption is the optional "duration" option of the timed punishment commands: it replaces the ladder
// step's duration, so a punishment outside the configuration is possible too.
func PunishmentDurationOption() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Name:        "duration",
		Description: "Custom duration such as 30m, 12h, 7d or permanent (replaces the ladder step's duration)",
		Type:        discordgo.ApplicationCommandOptionString,
		Required:    false,
	}
}

// PunishmentOptions is the options the apply and revoke commands share: the target user, the reason (required when
// applying, optional when revoking) and, for ladder punishments, the optional ladder level and, for timed ones, the
// optional custom duration. Discord requires the required options first, so the order is user, level, reason, duration.
func PunishmentOptions(userDescription, reasonDescription string, reasonRequired, withLevel, withDuration bool) []*discordgo.ApplicationCommandOption {
	user := &discordgo.ApplicationCommandOption{Name: "user", Description: userDescription, Type: discordgo.ApplicationCommandOptionUser, Required: true}
	reason := &discordgo.ApplicationCommandOption{Name: "reason", Description: reasonDescription, Type: discordgo.ApplicationCommandOptionString, Required: reasonRequired}
	options := []*discordgo.ApplicationCommandOption{user}
	switch {
	case reasonRequired:
		// Discord wants the required options first, so a required reason sits right after the user.
		options = append(options, reason)
		if withLevel {
			options = append(options, PunishmentLevelOption())
		}
	case withLevel:
		options = append(options, PunishmentLevelOption(), reason)
	default:
		options = append(options, reason)
	}
	if withDuration {
		options = append(options, PunishmentDurationOption())
	}
	return options
}

// PunishmentLevelAutocomplete suggests the ladder levels that have a step for ladderType.
func PunishmentLevelAutocomplete(ladderType string) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		typed := ""
		for _, opt := range i.ApplicationCommandData().Options {
			if opt.Focused {
				typed = strings.ToLower(opt.StringValue())
			}
		}
		var choices []*discordgo.ApplicationCommandOptionChoice
		if cfg, err := LoadLadder(i.GuildID, "punishment_ladder", "Punishment ladders"); err == nil {
			for _, lvl := range cfg.enabledLevels() {
				name := lvl.Name
				if name == "" {
					name = lvl.Key
				}
				if !lvl.handles(ladderType) || (typed != "" && !strings.Contains(strings.ToLower(name), typed)) {
					continue
				}
				if r := []rune(name); len(r) > 100 {
					name = string(r[:100])
				}
				choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: lvl.Key})
				if len(choices) == 25 {
					break
				}
			}
		}
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{Choices: choices},
		})
	}
}

// ============================================================================
// Strike ladder
// ============================================================================

// StrikePlan is what the strike ladder prescribes for a player's next strike.
type StrikePlan struct {
	Level      string
	Step       int    // the step the offence count reached (0 = below the first step: the strike is only recorded)
	Action     string // the step's action; an ELO-only action applies no punishment
	Span       time.Duration
	EloRemoval int             // total ELO to take: the step's own amount plus every elo_removal action
	Also       []PlannedAction // further actions applied together with Action
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
	count := 1 // the strike being issued (strikes never expire: persistent, or per season)
	for _, s := range prior {
		if s.LadderLevel != lvl.Key || s.IsVoided || s.IsAppealed {
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
			plan.Also = step.extras()
			for _, a := range plan.Also {
				plan.EloRemoval += a.EloRemoval
			}
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
	cfg, err := LoadLadder(guildID, "strike_ladder", "Strike ladders")
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

// ============================================================================
// Apply flow
// ============================================================================

// ladderSpecs maps a ladder step action to the spec that applies it; each punishment command registers its own.
var ladderSpecs = map[string]*ApplySpec{}

// RegisterLadderAction makes spec what a ladder step with the given action ("queue_ban", "chat_mute"...) applies.
func RegisterLadderAction(action string, spec *ApplySpec) { ladderSpecs[action] = spec }

// ParseSpan reads a duration as staff type it: 30m, 12h, 7d, 2w, or permanent (perm, 0, never).
func ParseSpan(text string) (time.Duration, error) {
	text = strings.ToLower(strings.TrimSpace(text))
	switch text {
	case "0", "perm", "permanent", "never":
		return 0, nil
	}
	units := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}
	if len(text) < 2 || units[text[len(text)-1]] == 0 {
		return 0, fmt.Errorf("use a number and a unit, such as 30m, 12h, 7d or 2w, or `permanent`")
	}
	n, err := strconv.Atoi(text[:len(text)-1])
	if err != nil || n <= 0 || n > 100000 {
		return 0, fmt.Errorf("use a whole number above zero before the unit, such as 30m, 12h, 7d or 2w")
	}
	return time.Duration(n) * units[text[len(text)-1]], nil
}

// LadderActionSpec is the spec that applies a ladder step action, nil when a standard punishment cannot apply it.
func LadderActionSpec(action string) *ApplySpec { return ladderSpecs[action] }

// ApplyPunishment validates the target, lets the punishment ladder choose the action and duration, records the
// punishment, and runs its Discord action. Commands with no ladder category (spec.Ladder empty, e.g. kick) apply
// their own action as before.
func ApplyPunishment(s *discordgo.Session, i *discordgo.InteractionCreate, spec *ApplySpec) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, punishmentInvalidTargetEmbed(spec.Action))
	}
	actor := helpers.InteractionUserID(i)
	if target.ID == actor {
		return helpers.EditInteractionEmbed(s, i, punishmentSelfTargetEmbed(spec.Action))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, punishmentBotTargetEmbed(spec.Adjective))
	}

	// 2. Validate Reason
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	if reason == "" {
		if !spec.ReasonOptional {
			return helpers.EditInteractionEmbed(s, i, punishmentReasonRequiredEmbed())
		}
		reason = "No reason provided"
	}

	// 3. A custom duration (the optional "duration" option) replaces the ladder step's; check it before anything is created
	customSpan, customDuration := time.Duration(0), strings.TrimSpace(helpers.OptionString(i, "duration")) != ""
	if customDuration {
		var err error
		if customSpan, err = ParseSpan(helpers.OptionString(i, "duration")); err != nil {
			return helpers.EditInteractionEmbed(s, i, punishmentInvalidDurationEmbed(err))
		}
	}

	// 4. Require the ladder before anything is created. A custom duration with no level chosen may go without one: the
	// command then applies its own action for that long, recorded under no ladder level.
	useLadder := spec.Ladder != ""
	if useLadder {
		level := helpers.OptionString(i, "level")
		if err := RequirePunishmentLadder(i.GuildID, spec.Ladder, level); err != nil {
			if !(customDuration && strings.TrimSpace(level) == "" && IsLadderNotConfigured(err)) {
				return helpers.EditInteractionEmbed(s, i, LadderNotConfiguredEmbedOf(err))
			}
			useLadder = false
		}
	}

	// 5. Resolve Target Profile
	playerID, profile, err := helpers.EnsureModerationTarget(actor, i.GuildID, target)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPunishmentEmbed(spec.Action))
	}

	// 6. The ladder picks the step for this offence: its action and duration are what is applied (a custom duration
	// replaces the duration only)
	level, expiresAt, durationLabel := "", "", "Permanent"
	span := customSpan
	var extras []PlannedAction
	if useLadder {
		plan, err := PlanPunishment(i.GuildID, sdk.UintToString(playerID), spec.Ladder, helpers.OptionString(i, "level"))
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, LadderNotConfiguredEmbedOf(err))
		}
		resolved := LadderActionSpec(plan.Action)
		if resolved == nil {
			return helpers.EditInteractionEmbed(s, i, LadderNotConfiguredEmbedOf(&ErrLadderNotConfigured{Reason: fmt.Sprintf(
				"Step %d of the punishment ladder uses the %q action, which a standard punishment cannot apply. Change it on the dashboard to warning, chat mute, voice mute, queue ban or server ban.", plan.Step, plan.Action)}))
		}
		spec, level, extras = resolved, plan.Level, plan.Also
		if !customDuration {
			span = plan.Span
		}
	}
	if spec.Duration && span > 0 {
		expiresAt = time.Now().Add(span).UTC().Format(time.RFC3339)
		durationLabel = helpers.FormatSpan(span)
	}

	username := target.Username
	if profile != nil && profile.Username != "" {
		username = profile.Username
	}
	embed, ok := ApplyPunishmentTo(s, i.GuildID, actor, target, playerID, username, reason, spec, level, expiresAt, durationLabel)
	if ok {
		// Extra actions of the step run too; they are recorded under no ladder level so the offence is counted once.
		embed.Description += ApplyExtraActions(s, i.GuildID, actor, target, playerID, username, reason, extras)
	}
	return helpers.EditInteractionEmbed(s, i, embed)
}

// ApplyExtraActions applies the extra actions of a ladder step (warning, mute, ban...) and returns a note listing what
// happened for each, to append to the result embed. ELO-removal actions are the caller's job and are skipped here.
func ApplyExtraActions(s *discordgo.Session, guildID, actor string, target *discordgo.User, playerID uint, username, reason string, extras []PlannedAction) string {
	notes := ""
	for _, a := range extras {
		spec := LadderActionSpec(a.Action)
		if spec == nil {
			continue
		}
		expiresAt, label := "", "Permanent"
		if spec.Duration && a.Span > 0 {
			expiresAt, label = time.Now().Add(a.Span).UTC().Format(time.RFC3339), helpers.FormatSpan(a.Span)
		}
		if _, ok := ApplyPunishmentTo(s, guildID, actor, target, playerID, username, reason, spec, "", expiresAt, label); ok {
			notes += fmt.Sprintf("\n> **Also applied:** %s (%s)", spec.Title, label)
		} else {
			notes += fmt.Sprintf("\n> **Also:** %s could not be applied (already active, or the member cannot be moderated).", spec.Action)
		}
	}
	return notes
}

// ApplyPunishmentTo checks the target against spec, records the punishment under the ladder level that chose it, and
// runs the Discord action. It returns the result embed and whether the punishment was recorded.
func ApplyPunishmentTo(s *discordgo.Session, guildID, actor string, target *discordgo.User, playerID uint, username, reason string, spec *ApplySpec, level, expiresAt, durationLabel string) (*discordgo.MessageEmbed, bool) {
	pid := sdk.UintToString(playerID)

	// Check Member & Role Hierarchy
	member, _ := helpers.FetchGuildMember(s, guildID, target.ID)
	if spec.RequireMember && member == nil {
		return punishmentMemberNotFoundEmbed(spec.Action), false
	}
	if !spec.SkipHierarchy {
		botMember, _ := helpers.FetchGuildMember(s, guildID, s.State.User.ID)
		if issue := helpers.MemberManageabilityIssue(s, guildID, member, botMember); issue != helpers.RoleHierarchyOK {
			return punishmentHierarchyEmbed(), false
		}
	}

	// Check Existing Punishments
	if spec.Duplicates {
		active, err := database.Must().Punishments.ByPlayer(guildID, pid, true, "", sdk.Page{})
		if err != nil {
			return UnexpectedErrorPunishmentEmbed(spec.Action), false
		}
		for _, entry := range active.Data {
			if PunishmentTypeIn(entry.Type, spec.activeTypes()) {
				return punishmentAlreadyActiveEmbed(spec.Adjective), false
			}
		}
		if spec.MemberActive != nil && member != nil && spec.MemberActive(member) {
			return punishmentAlreadyActiveEmbed(spec.Adjective), false
		}
	}

	// Record Punishment in Database
	if _, err := database.Must().Punishments.Create(actor, guildID, pid, sdk.NewPunishment{
		StaffID: actor, Reason: reason, Type: spec.Type, ExpiresAt: expiresAt, LadderLevel: level,
	}); err != nil {
		return UnexpectedErrorPunishmentEmbed(spec.Action), false
	}
	helpers.TrackPunishmentExpiry(guildID, expiresAt)

	// Apply Discord Action
	applied := false
	if spec.Discord != nil {
		applied = spec.Discord(s, guildID, target.ID, reason)
	}
	if spec.RoleKey != "" {
		if roleID := LadderRole(guildID, spec.Ladder); roleID != "" && s.GuildMemberRoleAdd(guildID, target.ID, roleID) == nil {
			applied = true
		}
	}

	embed := punishmentAppliedEmbed(spec, target, username, durationLabel, expiresAt, reason, actor, applied)
	helpers.LogCommandResult(s, guildID, spec.Command, actor, embed)
	notice := PunishmentNotice{Command: spec.Command, Action: "applied", User: target, Username: username, Type: spec.Type, Reason: reason, By: actor}
	if spec.Duration {
		notice.Duration, notice.Expires = durationLabel, expiresAt
	}
	SendPunishmentAlert(s, guildID, notice)
	return embed, true
}

// ============================================================================
// Revoke flow
// ============================================================================

// RevokePunishment removes a player's active punishment and runs its Discord reversal.
func RevokePunishment(s *discordgo.Session, i *discordgo.InteractionCreate, spec *RevokeSpec) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, punishmentInvalidTargetEmbed(strings.ToLower(spec.Action)))
	}
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	if reason == "" {
		reason = spec.Action + " removed by staff"
	}
	actor := helpers.InteractionUserID(i)

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, PunishmentPlayerNotFoundEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPunishmentEmbed(strings.ToLower(spec.Action)))
	}
	pid := sdk.UintToString(view.Player.ID)

	// 3. Find the active punishments of this kind (a warning is removed one at a time, the newest first)
	active, err := database.Must().Punishments.ByPlayer(i.GuildID, pid, true, "", sdk.Page{})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPunishmentEmbed(strings.ToLower(spec.Action)))
	}
	var held []models.PlayerPunishmentsModel
	for _, entry := range active.Data {
		if !PunishmentTypeIn(entry.Type, spec.activeTypes()) {
			continue
		}
		if spec.Latest && len(held) > 0 {
			if entry.ID < held[0].ID {
				continue
			}
			held = held[:0]
		}
		held = append(held, entry)
	}

	// 4. Validation Checks: something is recorded, or it is still in force on Discord (a voice mute, or the role - which
	// also covers a punishment that ran out a moment ago and has not been swept yet)
	roleID := ""
	if spec.RoleKey != "" {
		roleID = LadderRole(i.GuildID, spec.Ladder)
	}
	discordActive := spec.DiscordActive != nil && spec.DiscordActive(s, i.GuildID, target.ID)
	if !discordActive && roleID != "" {
		if member, _ := helpers.FetchGuildMember(s, i.GuildID, target.ID); member != nil && slices.Contains(member.Roles, roleID) {
			discordActive = true
		}
	}
	if len(held) == 0 && !discordActive {
		return helpers.EditInteractionEmbed(s, i, punishmentNotActiveEmbed(spec.Adjective))
	}

	// 5. Reverse the Discord side first: if the role cannot be taken off the punishment stays recorded as active, so
	// staff can fix the bot's permissions and run the command again.
	if spec.Discord != nil {
		spec.Discord(s, i.GuildID, target.ID, reason)
	}
	if roleID != "" {
		if err := s.GuildMemberRoleRemove(i.GuildID, target.ID, roleID); err != nil && !IsDiscordNotFound(err) {
			punishmentLogger.Warn(fmt.Sprintf("removing role %s from %s in %s: %v", roleID, target.ID, i.GuildID, err))
			return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPunishmentEmbed(strings.ToLower(spec.Action)))
		}
	}

	// 6. Revoke, don't delete: the rows stay in history and keep counting toward the ladder's offence count.
	for _, entry := range held {
		if err := database.Must().Punishments.Revoke(actor, i.GuildID, entry.ID, actor, reason); err != nil && !sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPunishmentEmbed(strings.ToLower(spec.Action)))
		}
	}
	if spec.RecordType != "" {
		_, _ = database.Must().Punishments.Create(actor, i.GuildID, pid, sdk.NewPunishment{
			StaffID: actor, Reason: reason, Type: spec.RecordType,
		})
	}
	embed := punishmentRevokedEmbed(spec.Action, spec.Type, target, reason, actor)
	helpers.LogCommandResult(s, i.GuildID, spec.Command, actor, embed)
	SendPunishmentAlert(s, i.GuildID, PunishmentNotice{Command: spec.Command, Action: "revoked", User: target, Username: target.Username, Type: spec.Type, Reason: reason, By: actor})
	return helpers.EditInteractionEmbed(s, i, embed)
}

// PunishmentTypeIn reports whether match is contained in candidates.
func PunishmentTypeIn(match string, candidates []string) bool {
	for _, t := range candidates {
		if match == t {
			return true
		}
	}
	return false
}

// VoiceMutePunishmentTypes returns the punishment types that count as a voice mute.
func VoiceMutePunishmentTypes() []string {
	return []string{"restrict", "vc_mute"}
}

// ============================================================================
// Restriction roles
// ============================================================================

// restrictionRole pairs a command with a name: for restrictionRoles it is the command that applied a punishment type
// and the ladder type whose role it hands out; for ladderRoleCommands it is a command and its role requirement key.
type restrictionRole struct{ command, name string }

var restrictionRoles = map[string]restrictionRole{
	"mute":     {"mute", "chat_mute"},
	"ban":      {"ban", "queue_ban"},
	"vc_mute":  {"vcmute", "vc_mute"},
	"restrict": {"vcmute", "vc_mute"},
}

// ladderRoleCommands is the command whose role requirement stands in for a ladder type with no role chosen in the
// punishment ladder settings (the assignment made before those settings had role pickers).
var ladderRoleCommands = map[string]restrictionRole{
	"chat_mute": {"mute", "chat_muted_role"},
	"queue_ban": {"ban", "queue_banned_role"},
	"vc_mute":   {"vcmute", "vc_muted_role"},
}

// LadderRole is the role a punishment of the given ladder type hands out ("" when none is set): the one chosen in the
// punishment ladder settings, else the role the command itself has assigned.
func LadderRole(guildID, ladderType string) string {
	if raw, err := database.Must().GuildConfig.Field(guildID, "punishment_ladder"); err == nil {
		var cfg ladderConfig
		if json.Unmarshal(raw, &cfg) == nil && cfg.Roles[ladderType] != "" {
			return cfg.Roles[ladderType]
		}
	}
	if fallback, ok := ladderRoleCommands[ladderType]; ok {
		return helpers.SettingRole(guildID, fallback.command, fallback.name)
	}
	return ""
}

// ============================================================================
// Punishment expiry
// ============================================================================

// Timed punishments only record an expires_at; this sweep is what ends them. Each expired punishment is revoked in
// the database, then its Discord side is undone: the role the command handed out, the voice mute or the server ban.

var (
	punishmentLogger  = baseutility.CreateModuleLogger("PUNISHMENT_EXPIRY", "discord")
	punishmentSession atomic.Pointer[discordgo.Session]
	punishmentOnce    sync.Once
)

const (
	punishmentSweepEvery = time.Minute
	punishmentSweepBatch = 100
)

// PunishmentWatch sweeps only guilds that have a timed punishment outstanding. TrackPunishmentExpiry adds a guild when
// one is created; a sweep drops it once none is left.
var PunishmentWatch = base.NewGuildWatch("PunishmentExpiry", punishmentSweepEvery, func(guildID string) bool {
	s := punishmentSession.Load()
	if s == nil {
		return true
	}
	expireGuildPunishments(s, guildID)
	return guildHasTimedPunishments(guildID)
})

// guildHasTimedPunishments reports whether a punishment of the guild is still waiting to expire (or to be revoked).
// An error counts as yes, so a database hiccup never drops a guild.
func guildHasTimedPunishments(guildID string) bool {
	if upcoming, err := database.Must().Punishments.HasUpcoming(guildID); err != nil || upcoming {
		return true
	}
	left, err := database.Must().Punishments.Expired(guildID, sdk.Page{Limit: 1})
	return err != nil || len(left.Data) > 0
}

// StartPunishmentExpiry tracks every guild the session is in that already holds a timed punishment; the ready event
// calls it. Guilds with none are not swept until one is created.
func StartPunishmentExpiry(s *discordgo.Session) {
	punishmentSession.Store(s)
	punishmentOnce.Do(func() {
		ids := make([]string, 0, len(s.State.Guilds))
		for _, g := range s.State.Guilds {
			ids = append(ids, g.ID)
		}
		go PunishmentWatch.Seed(ids, guildHasTimedPunishments)
	})
}

// expireGuildPunishments revokes every expired punishment of the guild and undoes it on Discord. A revoked one
// leaves the expired list, so batches are drawn until it runs dry or nothing in a batch could be revoked.
func expireGuildPunishments(s *discordgo.Session, guildID string) {
	for {
		page, err := database.Must().Punishments.Expired(guildID, sdk.Page{Limit: punishmentSweepBatch})
		if err != nil || len(page.Data) == 0 {
			return
		}
		revoked := 0
		for _, p := range page.Data {
			// Lift it on Discord first: a failed unban/role removal leaves the punishment on the expired list so the
			// next sweep retries it, instead of revoking the row and leaving the player punished for good.
			userID, ok := liftPunishment(s, guildID, p)
			if !ok {
				continue
			}
			if err := database.Must().Punishments.Revoke(sdk.SystemActor, guildID, p.ID, sdk.SystemActor, "Punishment expired"); err != nil {
				punishmentLogger.Warn(fmt.Sprintf("revoking expired punishment %d in %s: %v", p.ID, guildID, err))
				continue
			}
			revoked++
			announceExpiredPunishment(s, guildID, userID, p)
		}
		if revoked == 0 || len(page.Data) < punishmentSweepBatch {
			return
		}
	}
}

// IsDiscordNotFound reports an error meaning the thing to undo is already gone (unknown member, role or ban).
func IsDiscordNotFound(err error) bool {
	var restErr *discordgo.RESTError
	return errors.As(err, &restErr) && restErr.Response != nil && restErr.Response.StatusCode == 404
}

// liftPunishment lifts the Discord side of p, unless another active punishment of the same kind still holds it.
// It returns the player's Discord ID, and false when the lift failed and should be retried on the next sweep.
func liftPunishment(s *discordgo.Session, guildID string, p models.PlayerPunishmentsModel) (string, bool) {
	view, err := database.Must().Player.Get(guildID, sdk.UintToString(p.PlayerID))
	if sdk.IsNotFound(err) {
		return "", true // the player is gone; nothing to lift, just close the row
	}
	if err != nil || view.Player == nil {
		return "", false
	}
	userID := view.Player.SnowflakeID

	active, err := database.Must().Punishments.ByPlayer(guildID, userID, true, "", sdk.Page{})
	if err != nil {
		return "", false // can't tell whether it is still held; leave Discord as it is rather than lift a live punishment
	}
	stillHeld := func(match func(typ string) bool) bool {
		for _, a := range active.Data {
			if a.ID != p.ID && match(a.Type) {
				return true
			}
		}
		return false
	}

	ok := true
	fail := func(what string, err error) {
		if err != nil && !IsDiscordNotFound(err) {
			punishmentLogger.Warn(fmt.Sprintf("%s for %s in %s after expiry: %v", what, userID, guildID, err))
			ok = false
		}
	}
	switch rr, isRole := restrictionRoles[p.Type]; {
	case isRole:
		sameRole := func(typ string) bool { return restrictionRoles[typ] == rr }
		if !stillHeld(sameRole) {
			if roleID := LadderRole(guildID, rr.name); roleID != "" {
				fail("removing role", s.GuildMemberRoleRemove(guildID, userID, roleID))
			}
			if rr.name == "vc_mute" {
				_ = s.GuildMemberMute(guildID, userID, false) // 400s when the member is not in voice; not worth retrying
			}
		}
	case p.Type == "server_ban":
		if !stillHeld(func(typ string) bool { return typ == "server_ban" }) {
			fail("unbanning", s.GuildBanDelete(guildID, userID))
		}
	}
	return userID, ok
}

// announceExpiredPunishment posts the expiry to the moderation log and the player's alerts channel.
func announceExpiredPunishment(s *discordgo.Session, guildID, userID string, p models.PlayerPunishmentsModel) {
	if userID == "" {
		return
	}
	command := ""
	if rr, ok := restrictionRoles[p.Type]; ok {
		command = rr.command
	} else if p.Type == "server_ban" {
		command = "serverban"
	}
	logPunishmentExpiry(s, guildID, command, userID, p)
	SendPunishmentAlert(s, guildID, PunishmentNotice{Command: command, Action: "expired", User: &discordgo.User{ID: userID}, Username: userID, Type: p.Type, Reason: p.Reason, By: p.StaffID})
}

// ReapplyPunishments gives a member who just (re)joined the roles of the punishments still in force on them, so leaving
// and rejoining does not shake off a mute or a queue ban.
func ReapplyPunishments(s *discordgo.Session, guildID, userID string) {
	active, err := database.Must().Punishments.ByPlayer(guildID, userID, true, "", sdk.Page{})
	if err != nil {
		return // not a known player: nothing recorded against them
	}
	for _, p := range active.Data {
		rr, ok := restrictionRoles[p.Type]
		if !ok {
			continue
		}
		if roleID := LadderRole(guildID, rr.name); roleID != "" {
			if err := s.GuildMemberRoleAdd(guildID, userID, roleID); err != nil {
				punishmentLogger.Warn(fmt.Sprintf("re-applying role %s to %s in %s: %v", roleID, userID, guildID, err))
			}
		}
	}
}

// ReapplyVoiceMute server-mutes a member who joins a voice channel while a voice mute is in force; Discord only allows
// the mute once they are in voice, so a mute given while they were elsewhere takes hold here.
func ReapplyVoiceMute(s *discordgo.Session, guildID, userID string) {
	active, err := database.Must().Punishments.ByPlayer(guildID, userID, true, "", sdk.Page{})
	if err != nil {
		return
	}
	for _, p := range active.Data {
		if PunishmentTypeIn(p.Type, VoiceMutePunishmentTypes()) {
			_ = s.GuildMemberMute(guildID, userID, true)
			return
		}
	}
}

// RevokeStrikePunishments ends the punishments a strike applied (they carry the reason "Strike #<id>: ...") and lifts
// them on Discord, so removing a strike also removes its mute, queue ban or ban. The rows stay in history. It returns how
// many were ended.
func RevokeStrikePunishments(s *discordgo.Session, guildID, actor string, playerID, strikeID uint) int {
	active, err := database.Must().Punishments.ByPlayer(guildID, sdk.UintToString(playerID), true, "", sdk.Page{})
	if err != nil {
		return 0
	}
	prefix := "Strike #" + sdk.UintToString(strikeID) + ":"
	ended := 0
	for _, p := range active.Data {
		if !strings.HasPrefix(p.Reason, prefix) {
			continue
		}
		if _, ok := liftPunishment(s, guildID, p); !ok {
			continue
		}
		if err := database.Must().Punishments.Revoke(actor, guildID, p.ID, actor, "Strike #"+sdk.UintToString(strikeID)+" removed"); err == nil {
			ended++
		}
	}
	return ended
}

// ============================================================================
// Logs & alerts
// ============================================================================

// logPunishmentExpiry posts the expiry to the same log channels the command's applications and revocations go to.
func logPunishmentExpiry(s *discordgo.Session, guildID, command, userID string, p models.PlayerPunishmentsModel) {
	if command == "" {
		return
	}
	emb := PunishmentExpiredLogEmbed(userID, p)
	helpers.WithGuildFooterIconForGuild(s, guildID, emb)
	for _, channelID := range helpers.CommandLogChannels(guildID, command) {
		_, _ = s.ChannelMessageSendEmbed(channelID, emb)
	}
}

// PunishmentExpiryEventName is the event that ends timed punishments; its moderation_alerts channel is the catch-all
// alerts channel for punishments whose own alerts channel is not set.
const PunishmentExpiryEventName = "Punishment Expiry"

// punishmentAlertKeys is the optional alerts channel each punishment command (and its reversal) declares.
var punishmentAlertKeys = map[string]string{
	"mute": "chat_mute_alerts", "unmute": "chat_mute_alerts",
	"vcmute": "vc_mute_alerts", "vcunmute": "vc_mute_alerts",
	"ban": "queue_ban_alerts", "unban": "queue_ban_alerts",
	"serverban": "server_ban_alerts", "serverunban": "server_ban_alerts",
	"warn": "warn_alerts", "unwarn": "warn_alerts",
	"kick":   "server_kick_alerts",
	"strike": "strike_alerts", "unstrike": "strike_alerts",
}

// punishmentExpiry is how a punishment message says when the punishment ends, from an RFC 3339 time ("" = never).
func punishmentExpiry(expiresAt string) string {
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return "Never"
	}
	return fmt.Sprintf("<t:%d:f> (<t:%d:R>)", t.Unix(), t.Unix())
}

// PunishmentNotice is what a punishment alert says: who was punished, what with, why, for how long (or how much ELO),
// until when, and who did it (left out when the bot did).
type PunishmentNotice struct {
	Command  string
	Action   string // applied, revoked or expired
	User     *discordgo.User
	Username string
	Type     string
	Duration string // "" for a punishment with no length (a warning, a strike)
	Expires  string // RFC 3339; with a Duration, "" means it never ends
	Elo      string // ELO taken, e.g. "`25` (`1200` → `1175`)"
	Reason   string
	By       string // the staff member's ID
}

// SendPunishmentAlert tells the player in the command's alerts channel (chat_mute_alerts, queue_ban_alerts...) that a
// punishment was applied, revoked or has expired, pinging them. Nothing is sent when the channel is not assigned.
func SendPunishmentAlert(s *discordgo.Session, guildID string, n PunishmentNotice) {
	command, user := n.Command, n.User
	key, ok := punishmentAlertKeys[command]
	if !ok {
		return
	}
	channelID, err := helpers.RequirementSlotID(guildID, command, types.ReqChannel, key)
	if err != nil || channelID == "" {
		channelID = helpers.EventRequirementID(guildID, PunishmentExpiryEventName, types.ReqChannel, types.ModerationAlertsKey)
	}
	if channelID == "" {
		return
	}
	alert := PunishmentAlertEmbed(n)
	helpers.WithGuildFooterIconForGuild(s, guildID, alert)
	_, _ = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:         "<@" + user.ID + ">",
		Embeds:          []*discordgo.MessageEmbed{alert},
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{user.ID}},
	})
}

// findPrefixLevel looks for a ladder level's key, id or name written out in tokens (the longest run wins) and returns
// where it sits and the level's key; end == start when there is none.
func findPrefixLevel(guildID string, tokens []string) (start, end int, key string) {
	cfg, err := LoadLadder(guildID, "punishment_ladder", "Punishment ladders")
	if err != nil {
		return 0, 0, ""
	}
	levels := cfg.enabledLevels()
	for n := len(tokens); n >= 1; n-- {
		for s := 0; s+n <= len(tokens); s++ {
			phrase := strings.Join(tokens[s:s+n], " ")
			for _, lvl := range levels {
				if lvl.matches(phrase) {
					return s, s + n, lvl.Key
				}
			}
		}
	}
	return 0, 0, ""
}

func init() {
	helpers.FindPrefixLevel = findPrefixLevel
	helpers.OnPunishmentTimed = PunishmentWatch.Track
}
