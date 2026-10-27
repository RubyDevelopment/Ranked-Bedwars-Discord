package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	baseutility "ranked-bedwars/src/utility"
)

// Shared plumbing of the restriction-style punishment commands (warn, kick,
// chat mute, queue ban, voice mute, server ban and their reversals): the spec
// each command declares, the apply/revoke flows, and the embeds they share.

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
	// RoleKey, when set, is that command's role requirement: the role given to the target (taken off again on revoke).
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

	// DiscordActive reports whether the Discord-side punishment is still in effect.
	DiscordActive func(s *discordgo.Session, guildID, targetID string) bool
	Discord       DiscordAction

	// Command and RoleKey name the command (the one that applies this punishment) whose log channels and role to use.
	Command string
	RoleKey string
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
// Apply Flow
// ============================================================================

// ladderSpecs maps a ladder step action to the spec that applies it; each punishment command registers its own.
var ladderSpecs = map[string]*ApplySpec{}

// RegisterLadderAction makes spec what a ladder step with the given action ("queue_ban", "chat_mute"...) applies.
func RegisterLadderAction(action string, spec *ApplySpec) { ladderSpecs[action] = spec }

// FormatSpan renders a duration the way staff type it: 30m, 2h, 7d.
func FormatSpan(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

// LadderActionSpec is the spec that applies a ladder step action, nil when a standard punishment cannot apply it.
func LadderActionSpec(action string) *ApplySpec { return ladderSpecs[action] }

// ApplyPunishment validates the target, lets the punishment ladder choose the action and duration, records the
// punishment, and runs its Discord action. Commands with no ladder category (spec.Ladder empty, e.g. kick) apply
// their own action as before.
func ApplyPunishment(s *discordgo.Session, i *discordgo.InteractionCreate, spec *ApplySpec) error {
	// 1. Validate Target User
	target := OptionUser(s, i, "user")
	if target == nil {
		return EditInteractionEmbed(s, i, punishmentInvalidTargetEmbed(spec.Action))
	}
	actor := InteractionUserID(i)
	if target.ID == actor {
		return EditInteractionEmbed(s, i, punishmentSelfTargetEmbed(spec.Action))
	}
	if target.Bot {
		return EditInteractionEmbed(s, i, punishmentBotTargetEmbed(spec.Adjective))
	}

	// 2. Validate Reason
	reason := strings.TrimSpace(OptionString(i, "reason"))
	if reason == "" {
		return EditInteractionEmbed(s, i, punishmentReasonRequiredEmbed())
	}

	// 3. Require the ladder before anything is created
	if spec.Ladder != "" {
		if err := RequirePunishmentLadder(i.GuildID, spec.Ladder); err != nil {
			return EditInteractionEmbed(s, i, ladderNotConfiguredEmbed(err))
		}
	}

	// 4. Resolve Target Profile
	playerID, profile, err := EnsureModerationTarget(actor, i.GuildID, target)
	if err != nil {
		return EditInteractionEmbed(s, i, unexpectedErrorPunishmentEmbed(spec.Action))
	}

	// 5. The ladder picks the step for this offence: its action and duration are what is applied
	level, expiresAt, durationLabel := "", "", "Permanent"
	if spec.Ladder != "" {
		plan, err := PlanPunishment(i.GuildID, sdk.UintToString(playerID), spec.Ladder)
		if err != nil {
			return EditInteractionEmbed(s, i, ladderNotConfiguredEmbed(err))
		}
		resolved := LadderActionSpec(plan.Action)
		if resolved == nil {
			return EditInteractionEmbed(s, i, ladderNotConfiguredEmbed(&ErrLadderNotConfigured{Reason: fmt.Sprintf(
				"Step %d of the punishment ladder uses the %q action, which a standard punishment cannot apply. Change it on the dashboard to warning, chat mute, voice mute, queue ban or server ban.", plan.Step, plan.Action)}))
		}
		spec, level = resolved, plan.Level
		if spec.Duration && plan.Span > 0 {
			expiresAt = time.Now().Add(plan.Span).UTC().Format(time.RFC3339)
			durationLabel = FormatSpan(plan.Span)
		}
	}

	username := target.Username
	if profile != nil && profile.Username != "" {
		username = profile.Username
	}
	embed, _ := ApplyPunishmentTo(s, i.GuildID, actor, target, playerID, username, reason, spec, level, expiresAt, durationLabel)
	return EditInteractionEmbed(s, i, embed)
}

// ApplyPunishmentTo checks the target against spec, records the punishment under the ladder level that chose it, and
// runs the Discord action. It returns the result embed and whether the punishment was recorded.
func ApplyPunishmentTo(s *discordgo.Session, guildID, actor string, target *discordgo.User, playerID uint, username, reason string, spec *ApplySpec, level, expiresAt, durationLabel string) (*discordgo.MessageEmbed, bool) {
	pid := sdk.UintToString(playerID)

	// Check Member & Role Hierarchy
	member, _ := FetchGuildMember(s, guildID, target.ID)
	if spec.RequireMember && member == nil {
		return punishmentMemberNotFoundEmbed(spec.Action), false
	}
	if !spec.SkipHierarchy {
		botMember, _ := FetchGuildMember(s, guildID, s.State.User.ID)
		if issue := MemberManageabilityIssue(s, guildID, member, botMember); issue != RoleHierarchyOK {
			return punishmentHierarchyEmbed(), false
		}
	}

	// Check Existing Punishments
	if spec.Duplicates {
		active, err := database.Must().Punishments.ByPlayer(guildID, pid, true, "", sdk.Page{})
		if err != nil {
			return unexpectedErrorPunishmentEmbed(spec.Action), false
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
		return unexpectedErrorPunishmentEmbed(spec.Action), false
	}
	TrackPunishmentExpiry(guildID, expiresAt)

	// Apply Discord Action
	applied := false
	if spec.Discord != nil {
		applied = spec.Discord(s, guildID, target.ID, reason)
	}
	if spec.RoleKey != "" {
		if roleID := SettingRole(guildID, spec.Command, spec.RoleKey); roleID != "" && s.GuildMemberRoleAdd(guildID, target.ID, roleID) == nil {
			applied = true
		}
	}

	embed := punishmentAppliedEmbed(spec, target, username, durationLabel, reason, applied)
	LogCommandResult(s, guildID, spec.Command, actor, embed)
	SendPunishmentAlert(s, guildID, spec.Command, "applied", target, username, spec.Type, durationLabel, reason)
	return embed, true
}

// ============================================================================
// Revoke Flow
// ============================================================================

// RevokePunishment removes a player's active punishment and runs its Discord reversal.
func RevokePunishment(s *discordgo.Session, i *discordgo.InteractionCreate, spec *RevokeSpec) error {
	// 1. Validate Target User
	target := OptionUser(s, i, "user")
	if target == nil {
		return EditInteractionEmbed(s, i, punishmentInvalidTargetEmbed(strings.ToLower(spec.Action)))
	}
	reason := strings.TrimSpace(OptionString(i, "reason"))
	if reason == "" {
		reason = spec.Action + " removed by staff"
	}
	actor := InteractionUserID(i)

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return EditInteractionEmbed(s, i, PunishmentPlayerNotFoundEmbed())
		}
		return EditInteractionEmbed(s, i, unexpectedErrorPunishmentEmbed(strings.ToLower(spec.Action)))
	}
	pid := sdk.UintToString(view.Player.ID)

	// 3. Remove Active Punishments
	active, err := database.Must().Punishments.ByPlayer(i.GuildID, pid, true, "", sdk.Page{})
	if err != nil {
		return EditInteractionEmbed(s, i, unexpectedErrorPunishmentEmbed(strings.ToLower(spec.Action)))
	}
	found := false
	for _, entry := range active.Data {
		if !PunishmentTypeIn(entry.Type, spec.activeTypes()) {
			continue
		}
		found = true
		if err := database.Must().Punishments.Delete(actor, i.GuildID, entry.ID); err != nil && !sdk.IsNotFound(err) {
			return EditInteractionEmbed(s, i, unexpectedErrorPunishmentEmbed(strings.ToLower(spec.Action)))
		}
	}

	// 4. Validation Checks
	discordActive := spec.DiscordActive != nil && spec.DiscordActive(s, i.GuildID, target.ID)
	if !found && !discordActive {
		return EditInteractionEmbed(s, i, punishmentNotActiveEmbed(spec.Adjective))
	}

	// 5. Reverse Discord Action & Record History
	if spec.Discord != nil {
		spec.Discord(s, i.GuildID, target.ID, reason)
	}
	if spec.RecordType != "" {
		_, _ = database.Must().Punishments.Create(actor, i.GuildID, pid, sdk.NewPunishment{
			StaffID: actor, Reason: reason, Type: spec.RecordType,
		})
	}

	if spec.RoleKey != "" {
		if roleID := SettingRole(i.GuildID, spec.Command, spec.RoleKey); roleID != "" {
			_ = s.GuildMemberRoleRemove(i.GuildID, target.ID, roleID)
		}
	}
	embed := punishmentRevokedEmbed(spec.Action, target, reason)
	LogCommandResult(s, i.GuildID, spec.Command, actor, embed)
	SendPunishmentAlert(s, i.GuildID, spec.Command, "revoked", target, target.Username, spec.Type, "removed", reason)
	return EditInteractionEmbed(s, i, embed)
}

// ============================================================================
// Embed Builders
// ============================================================================

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

// restrictionRole is the command and role requirement a punishment type hands out.
type restrictionRole struct{ command, roleKey string }

var restrictionRoles = map[string]restrictionRole{
	"mute":     {"server_mute", "chat_muted_role"},
	"ban":      {"queue_ban", "queue_banned_role"},
	"vc_mute":  {"server_vc_mute", "vc_muted_role"},
	"restrict": {"server_vc_mute", "vc_muted_role"},
}

// punishmentWatch sweeps only guilds that have a timed punishment outstanding. TrackPunishmentExpiry adds a guild when
// one is created; a sweep drops it once none is left.
var punishmentWatch = newGuildWatch("PunishmentExpiry", punishmentSweepEvery, func(guildID string) bool {
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

// TrackPunishmentExpiry starts the expiry sweep for the guild when a just-created punishment carries an expiry.
func TrackPunishmentExpiry(guildID, expiresAt string) {
	if expiresAt != "" {
		punishmentWatch.Track(guildID)
	}
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
		go punishmentWatch.Seed(ids, guildHasTimedPunishments)
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
			if err := database.Must().Punishments.Revoke(sdk.SystemActor, guildID, p.ID, sdk.SystemActor, "Punishment expired"); err != nil {
				punishmentLogger.Warn(fmt.Sprintf("revoking expired punishment %d in %s: %v", p.ID, guildID, err))
				continue
			}
			revoked++
			undoExpiredPunishment(s, guildID, p)
		}
		if revoked == 0 || len(page.Data) < punishmentSweepBatch {
			return
		}
	}
}

// undoExpiredPunishment lifts the Discord side of p, unless another active punishment of the same kind still holds it.
func undoExpiredPunishment(s *discordgo.Session, guildID string, p models.PlayerPunishmentsModel) {
	view, err := database.Must().Player.Get(guildID, sdk.UintToString(p.PlayerID))
	if err != nil || view.Player == nil {
		return
	}
	userID := view.Player.SnowflakeID

	active, err := database.Must().Punishments.ByPlayer(guildID, userID, true, "", sdk.Page{})
	if err != nil {
		return // can't tell whether it is still held; leave Discord as it is rather than lift a live punishment
	}
	stillHeld := func(match func(typ string) bool) bool {
		for _, a := range active.Data {
			if match(a.Type) {
				return true
			}
		}
		return false
	}

	command := ""
	switch rr, ok := restrictionRoles[p.Type]; {
	case ok:
		command = rr.command
		sameRole := func(typ string) bool { return restrictionRoles[typ] == rr }
		if !stillHeld(sameRole) {
			if roleID := SettingRole(guildID, rr.command, rr.roleKey); roleID != "" {
				_ = s.GuildMemberRoleRemove(guildID, userID, roleID)
			}
			if rr.roleKey == "vc_muted_role" {
				_ = s.GuildMemberMute(guildID, userID, false)
			}
		}
	case p.Type == "server_ban":
		command = "serverunban"
		if !stillHeld(func(typ string) bool { return typ == "server_ban" }) {
			if err := s.GuildBanDelete(guildID, userID); err != nil {
				punishmentLogger.Warn(fmt.Sprintf("unbanning %s in %s after expiry: %v", userID, guildID, err))
			}
		}
	}
	logPunishmentExpiry(s, guildID, command, userID, p)
	SendPunishmentAlert(s, guildID, command, "expired", &discordgo.User{ID: userID}, userID, p.Type, "expired", p.Reason)
}

// logPunishmentExpiry posts the expiry to the moderation log channel, when the command has one assigned.
func logPunishmentExpiry(s *discordgo.Session, guildID, command, userID string, p models.PlayerPunishmentsModel) {
	if command == "" {
		return
	}
	channelID, err := RequirementSlotID(guildID, command, types.ReqChannel, types.CommandLogsKey)
	if err != nil || channelID == "" {
		return
	}
	_, _ = s.ChannelMessageSendEmbed(channelID, PunishmentExpiredLogEmbed(userID, p))
}

// SendPunishmentAlert sends an alert to the moderation alerts channel when a punishment is applied or revoked.
// Different punishment types have their own alert channels (e.g., queue_ban_alerts for queue_ban/queue_unban).
func SendPunishmentAlert(s *discordgo.Session, guildID, command, action string, user *discordgo.User, username, pType, duration, reason string) {
	// Map commands to their alert channel keys
	alertKey := types.ModerationAlertsKey
	switch command {
	case "queue_ban", "queue_unban":
		alertKey = "queue_ban_alerts"
	case "server_ban", "server_unban":
		alertKey = "server_ban_alerts"
	case "server_mute", "server_unmute":
		alertKey = "server_mute_alerts"
	case "server_vc_mute", "server_vc_unmute":
		alertKey = "server_vc_mute_alerts"
	case "server_kick":
		alertKey = "server_kick_alerts"
	}

	alertChannelID, err := RequirementSlotID(guildID, command, types.ReqChannel, alertKey)
	if err != nil || alertChannelID == "" {
		// Fall back to general moderation alerts if specific channel not set
		alertChannelID, err = RequirementSlotID(guildID, command, types.ReqChannel, types.ModerationAlertsKey)
		if err != nil || alertChannelID == "" {
			return
		}
	}

	_, _ = s.ChannelMessageSendEmbed(alertChannelID, PunishmentAlertEmbed(action, user, username, pType, duration, reason))
}

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
