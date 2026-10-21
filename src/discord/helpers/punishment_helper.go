package helpers

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
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

// ApplyPunishment validates the target, records the punishment, and runs its Discord action.
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

	// 3. Validate Duration
	durationLabel := "Permanent"
	var expiresAt string
	if spec.Duration {
		if input := strings.TrimSpace(OptionString(i, "duration")); input != "" {
			duration, ok, permanent := ParseDurationMs(input)
			if !ok {
				return EditInteractionEmbed(s, i, punishmentInvalidDurationEmbed())
			}
			if !permanent {
				expiresAt = time.Now().Add(duration).UTC().Format(time.RFC3339)
				durationLabel = input
			}
		}
	}

	// 4. Resolve Target Profile
	playerID, profile, err := EnsureModerationTarget(actor, i.GuildID, target)
	if err != nil {
		return EditInteractionEmbed(s, i, unexpectedErrorPunishmentEmbed(spec.Action))
	}
	pid := sdk.UintToString(playerID)

	// 5. Check Member & Role Hierarchy
	member, _ := FetchGuildMember(s, i.GuildID, target.ID)
	if spec.RequireMember && member == nil {
		return EditInteractionEmbed(s, i, punishmentMemberNotFoundEmbed(spec.Action))
	}
	if !spec.SkipHierarchy {
		botMember, _ := FetchGuildMember(s, i.GuildID, s.State.User.ID)
		if issue := MemberManageabilityIssue(s, i.GuildID, member, botMember); issue != RoleHierarchyOK {
			return EditInteractionEmbed(s, i, punishmentHierarchyEmbed())
		}
	}

	// 6. Check Existing Punishments
	if spec.Duplicates {
		active, err := database.Must().Punishments.ByPlayer(i.GuildID, pid, true, "", sdk.Page{})
		if err != nil {
			return EditInteractionEmbed(s, i, unexpectedErrorPunishmentEmbed(spec.Action))
		}
		for _, entry := range active.Data {
			if PunishmentTypeIn(entry.Type, spec.activeTypes()) {
				return EditInteractionEmbed(s, i, punishmentAlreadyActiveEmbed(spec.Adjective))
			}
		}
		if spec.MemberActive != nil && member != nil && spec.MemberActive(member) {
			return EditInteractionEmbed(s, i, punishmentAlreadyActiveEmbed(spec.Adjective))
		}
	}

	// 7. Record Punishment in Database
	if _, err := database.Must().Punishments.Create(actor, i.GuildID, pid, sdk.NewPunishment{
		StaffID: actor, Reason: reason, Type: spec.Type, ExpiresAt: expiresAt,
	}); err != nil {
		return EditInteractionEmbed(s, i, unexpectedErrorPunishmentEmbed(spec.Action))
	}
	TrackPunishmentExpiry(i.GuildID, expiresAt)

	// 8. Apply Discord Action
	applied := false
	if spec.Discord != nil {
		applied = spec.Discord(s, i.GuildID, target.ID, reason)
	}
	if spec.RoleKey != "" {
		if roleID := SettingRole(i.GuildID, spec.Command, spec.RoleKey); roleID != "" && s.GuildMemberRoleAdd(i.GuildID, target.ID, roleID) == nil {
			applied = true
		}
	}

	username := target.Username
	if profile != nil && profile.Username != "" {
		username = profile.Username
	}
	embed := punishmentAppliedEmbed(spec, target, username, durationLabel, reason, applied)
	LogCommandResult(s, i.GuildID, spec.Command, actor, embed)
	SendPunishmentAlert(s, i.GuildID, spec.Command, "applied", target, username, spec.Type, durationLabel, reason)
	return EditInteractionEmbed(s, i, embed)
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

func unexpectedErrorPunishmentEmbed(action string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: fmt.Sprintf("An unexpected error occurred while processing the %s. Please try again later or contact support.", action),
	})
}

func punishmentInvalidTargetEmbed(action string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: fmt.Sprintf("You must specify a player to %s. Select a member from the command options.", action),
	})
}

func punishmentSelfTargetEmbed(action string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: fmt.Sprintf("You cannot %s yourself. Select a different member to moderate.", action),
	})
}

func punishmentBotTargetEmbed(adjective string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: fmt.Sprintf("Automated bot accounts cannot be %s. Target a registered player instead.", adjective),
	})
}

func punishmentReasonRequiredEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Reason Required",
		Description: "You must provide a reason when issuing a punishment. Include a clear explanation in the reason field.",
	})
}

func punishmentInvalidDurationEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Invalid Duration",
		Description: "That duration format is not recognized. Use values like `30m`, `1d`, `2w`, or `perm`.",
	})
}

func punishmentMemberNotFoundEmbed(action string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: fmt.Sprintf("That user is not currently in this server. They must be in the server to apply a %s.", action),
	})
}

func punishmentHierarchyEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Hierarchy Error",
		Description: "That member cannot be moderated because their highest role is above or equal to mine. Adjust the role hierarchy or ask a higher-ranked moderator.",
	})
}

// PunishmentPlayerNotFoundEmbed is shown when the target has no player profile in this guild.
func PunishmentPlayerNotFoundEmbed() *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "That player is not registered in this guild. They must register first before punishments can be managed.",
	})
}

func punishmentAlreadyActiveEmbed(adjective string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Punishment Already Active",
		Description: fmt.Sprintf("That player is already **%s**. Check `/player_punishments` for details.", adjective),
	})
}

func punishmentNotActiveEmbed(adjective string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Punishment Not Active",
		Description: fmt.Sprintf("That player is not currently **%s**. Verify their status with `/player_punishments`.", adjective),
	})
}

func punishmentAppliedEmbed(spec *ApplySpec, user *discordgo.User, username, duration, reason string, applied bool) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully recorded %s. This action has been logged for staff review.\n\n> **Player:** <@%s> (**%s**)\n",
		spec.Summary, user.ID, username,
	)
	if spec.Duration {
		description += fmt.Sprintf("> **Duration:** `%s`\n", duration)
	}
	description += fmt.Sprintf("> **Reason:** %s", reason)

	if applied && spec.AppliedNote != "" {
		description += "\n\n" + spec.AppliedNote
	} else if !applied && spec.FailedNote != "" {
		description += "\n\n" + spec.FailedNote
	}
	return Preset("success", types.PresetOptions{
		Title:       spec.Title,
		Description: description,
	})
}

func punishmentRevokedEmbed(action string, user *discordgo.User, reason string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title: action + " Removed",
		Description: fmt.Sprintf(
			"Successfully removed the %s. This action has been logged for staff review.\n\n> **Player:** <@%s>\n> **Reason:** %s",
			strings.ToLower(action), user.ID, reason,
		),
	})
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
	channelID, err := RequirementSlotID(guildID, command, types.ReqChannel, types.ModerationLogsKey)
	if err != nil || channelID == "" {
		return
	}
	_, _ = s.ChannelMessageSendEmbed(channelID, &discordgo.MessageEmbed{
		Title:       "Punishment Expired",
		Description: fmt.Sprintf("> **Player:** <@%s>\n> **Type:** `%s`\n> **Reason:** %s", userID, p.Type, p.Reason),
		Timestamp:   time.Now().Format(time.RFC3339),
	})
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

	title := "Punishment Applied"
	if action == "revoked" {
		title = "Punishment Revoked"
	} else if action == "expired" {
		title = "Punishment Expired"
	}

	description := fmt.Sprintf("> **Player:** <@%s> (**%s**)\n> **Type:** `%s`\n", user.ID, username, pType)
	if duration != "" && duration != "removed" {
		description += fmt.Sprintf("> **Duration:** `%s`\n", duration)
	}
	description += fmt.Sprintf("> **Reason:** %s", reason)

	_, _ = s.ChannelMessageSendEmbed(alertChannelID, &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Timestamp:   time.Now().Format(time.RFC3339),
	})
}
