package helpers

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	dbjson "github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/utility"
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	apputil "ranked-bedwars/src/utility"
	baseutility "ranked-bedwars/src/utility"
)

// ============================================================================
// Constants & Configuration
// ============================================================================

const (
	// DefaultCommandPrefix is used when a guild has no configured prefix.
	DefaultCommandPrefix = "="
)

var (
	cooldowns             sync.Map
	errGuildNotRegistered = errors.New("guild not registered")
)

// PipelineOptions is defined in package types (commands.go).
type PipelineOptions = types.PipelineOptions

// ============================================================================
// Pipeline Execution
// ============================================================================

// RunCommandPipeline executes a command through the middleware pipeline.
//
// gc must come from a single LoadGuildContext call made by the caller (the
// prefix/slash dispatcher, which already needs its Settings for its own
// access checks before the pipeline runs) — RunCommandPipeline itself never
// touches the database, so a guild's registration, config and command
// permissions are each read exactly once per invocation rather than once per
// pipeline stage.
func RunCommandPipeline(s *discordgo.Session, i *discordgo.InteractionCreate, cmd *types.Command, gc GuildContext, opts PipelineOptions) error {
	if i.GuildID != "" {
		EnsureInteractionMember(s, i)
	}

	if !opts.SkipGuildRegistration && !gc.Registered {
		RespondGuildNotRegistered(s, i, cmd.Ephemeral)
		return nil
	}

	if denied := planDenial(gc, cmd, time.Now()); denied != nil && !opts.SkipGuildRegistration {
		respondInteractionEmbed(s, i, cmd.Ephemeral, denied, nil)
		return nil
	}

	if missing := LoadCommandRequirements(gc.Settings, cmd.Name); len(missing) > 0 {
		RespondRequirementsError(s, i, cmd, missing, LoadOptionalCommandRequirements(gc.Settings, cmd.Name))
		return nil
	}

	if denied := roleRequirementDenial(s, i, cmd, gc.Settings); denied != nil {
		respondInteractionEmbed(s, i, cmd.Ephemeral, denied, nil)
		return nil
	}

	userID := InteractionUserID(i)

	// Get command-specific cooldown from guild settings if available
	cooldownSeconds := int(cmd.Cooldown.Seconds())
	if guildCmd, ok := gc.Settings.Commands[cmd.Name]; ok && guildCmd.Cooldown > 0 {
		cooldownSeconds = guildCmd.Cooldown
	}

	if IsOnCooldown(i.GuildID, userID, cmd.Name, cooldownSeconds) {
		RespondSimpleError(s, i, cmd.Ephemeral, "Please slow down! You are on a cooldown.")
		return nil
	}

	if IsPrefixInteraction(i) {
		err := executePrefixCommand(s, i, cmd)
		logModerationIfOK(s, i, cmd, userID, err)
		return err
	}

	err := executeSlashCommand(s, i, cmd)
	logModerationIfOK(s, i, cmd, userID, err)
	return err
}

func logModerationIfOK(s *discordgo.Session, i *discordgo.InteractionCreate, cmd *types.Command, userID string, err error) {
	if err == nil && cmd.Category == "staff" {
		logModerationUsage(s, i, cmd, userID)
	}
}

func executePrefixCommand(s *discordgo.Session, i *discordgo.InteractionCreate, cmd *types.Command) error {
	if cmd.Execute == nil {
		return nil
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			_ = EditInteractionEmbed(s, i, CommandErrorEmbed())
		}
	}()

	if execErr := cmd.Execute(s, i); execErr != nil {
		logCommandError(i, cmd, execErr)
		_ = EditInteractionEmbed(s, i, CommandErrorEmbed())
		return execErr
	}
	return nil
}

func executeSlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate, cmd *types.Command) error {
	defer ClearDeferredSlashInteraction(i)

	if cmd.Execute == nil {
		return nil
	}

	if execErr := cmd.Execute(s, i); execErr != nil {
		logCommandError(i, cmd, execErr)
		_ = EditInteractionResponse(s, i, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{CommandErrorSlashEmbed()},
		})
		return execErr
	}

	return nil
}

func logCommandError(i *discordgo.InteractionCreate, cmd *types.Command, err error) {
	if err == nil || cmd == nil {
		return
	}
	cmdName := cmd.Name
	if i != nil {
		data := i.ApplicationCommandData()
		if sub := SubcommandPathFromInteraction(data); sub != "" {
			cmdName = cmdName + " " + sub
		}
	}
	fields := []zap.Field{
		zap.String("command", cmdName),
		zap.Error(err),
	}
	if i != nil {
		fields = append(fields,
			zap.String("guild_id", i.GuildID),
			zap.String("channel_id", i.ChannelID),
			zap.String("user_id", InteractionUserID(i)),
		)
	}
	apputil.DiscordLogger.Error("Command execution failed", fields...)
}

// ============================================================================
// Cooldown Management
// ============================================================================

// IsOnCooldown checks if a user is on cooldown for a command.
func IsOnCooldown(guildID, userID, cmdName string, durationSeconds int) bool {
	if durationSeconds <= 0 {
		durationSeconds = 5 // Default cooldown
	}
	key := fmt.Sprintf("%s:%s:%s", guildID, userID, cmdName)
	now := time.Now().Unix()

	if lastUsed, exists := cooldowns.Load(key); exists {
		if lastUsedInt, ok := lastUsed.(int64); ok && now-lastUsedInt < int64(durationSeconds) {
			return true
		}
	}
	cooldowns.Store(key, now)
	return false
}

// ============================================================================
// Discord Interaction Helpers
// ============================================================================

// InteractionUserID extracts the user ID from an interaction.
func InteractionUserID(i *discordgo.InteractionCreate) string {
	if i == nil || i.Interaction == nil {
		return ""
	}
	if i.User != nil {
		return i.User.ID
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	return ""
}

// EnsureInteractionMember ensures the interaction has member information.
func EnsureInteractionMember(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil || i.Member != nil {
		return
	}
	if i.User != nil && i.GuildID != "" {
		if member, err := s.GuildMember(i.GuildID, i.User.ID); err == nil && member != nil {
			i.Member = member
			if member.User == nil {
				member.User = i.User
			}
		}
	}
}

// ============================================================================
// Response Functions
// ============================================================================

// RespondGuildNotRegistered sends a guild not registered error response.
func RespondGuildNotRegistered(s *discordgo.Session, i *discordgo.InteractionCreate, ephemeral bool) {
	emb := GuildNotRegisteredEmbed()
	if IsPrefixInteraction(i) || IsDeferredSlashInteraction(i) {
		_ = EditInteractionEmbed(s, i, emb)
		return
	}
	_ = Respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{emb},
			Flags:  messageFlags(ephemeral),
		},
	})
}

// RespondSimpleError sends a simple error message response.
func RespondSimpleError(s *discordgo.Session, i *discordgo.InteractionCreate, ephemeral bool, message string) {
	if IsPrefixInteraction(i) {
		_ = EditInteractionEmbed(s, i, SimpleErrorEmbed(message))
		return
	}
	if IsDeferredSlashInteraction(i) {
		_ = EditInteractionResponse(s, i, &discordgo.WebhookEdit{Content: &message})
		return
	}
	_ = Respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
			Flags:   messageFlags(ephemeral),
		},
	})
}

// RespondRequirementsError sends a requirements validation error response.
// missing blocks the command from running; optional is shown alongside it for
// visibility (and offered via BuildRequirementFixComponents's Continue/Skip
// prompt once missing is resolved) but never blocks execution on its own.
func RespondRequirementsError(s *discordgo.Session, i *discordgo.InteractionCreate, cmd *types.Command, missing, optional []types.Requirement) {
	isAdmin := MemberIsAdministrator(s, i)
	var description string
	var components []discordgo.MessageComponent
	if isAdmin {
		description = FormatConfigurationErrorAdmin(cmd.Name, missing, optional)
		components = BuildRequirementFixComponents(cmd.Name, missing)
	} else {
		description = FormatConfigurationErrorUser(cmd.Name, missing)
	}
	respondInteractionEmbed(s, i, cmd.Ephemeral, ConfigurationErrorEmbed(description), components)
}

func respondInteractionEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, ephemeral bool, emb *discordgo.MessageEmbed, components []discordgo.MessageComponent) {
	if IsPrefixInteraction(i) || IsDeferredSlashInteraction(i) {
		if len(components) > 0 {
			_ = EditInteractionComponents(s, i, emb, components)
			return
		}
		_ = EditInteractionEmbed(s, i, emb)
		return
	}
	responseData := &discordgo.InteractionResponseData{
		Embeds: []*discordgo.MessageEmbed{emb},
		Flags:  messageFlags(ephemeral),
	}
	if len(components) > 0 {
		responseData.Components = components
	}
	_ = Respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: responseData,
	})
}

func messageFlags(ephemeral bool) discordgo.MessageFlags {
	if ephemeral {
		return discordgo.MessageFlagsEphemeral
	}
	return 0
}

// ============================================================================
// Guild Registration & Validation
// ============================================================================

func lookupGuildBySnowflake(guildID string) (*models.GuildsModel, error) {
	svc := database.Get()
	if guildID == "" || svc == nil {
		return nil, errGuildNotRegistered
	}

	guild, err := svc.Guild.Get(guildID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return nil, errGuildNotRegistered
		}
		return nil, err
	}
	return guild, nil
}

// ============================================================================
// Guild Settings Management
// ============================================================================

// GuildContext is defined in package types (commands.go).
type GuildContext = types.GuildContext

func defaultGuildSettings() types.GuildSettings {
	return types.GuildSettings{
		Prefix:        DefaultCommandPrefix,
		PrefixEnabled: true,
		SlashEnabled:  true,
		Commands:      types.GuildCommandsMap{},
	}
}

// LoadGuildContext loads everything RunCommandPipeline and the prefix/slash
// dispatchers need about guildID — the guild row and its settings — with one
// guild fetch and one config fetch, however many pipeline stages end up using
// the result. Call it once per interaction, not once per stage.
func LoadGuildContext(guildID string) GuildContext {
	guild, err := lookupGuildBySnowflake(guildID)
	registered := true
	if err != nil {
		registered = !errors.Is(err, errGuildNotRegistered) // fail open on anything but a definitive not-found
		guild = nil
	}

	svc := database.Get()
	if svc == nil || guildID == "" {
		return GuildContext{Guild: guild, Registered: registered, Settings: defaultGuildSettings()}
	}
	// The config route accepts the snowflake directly; an unregistered guild yields 404 -> defaults.
	config, cfgErr := svc.GuildConfig.Get(guildID)
	if cfgErr != nil || config == nil {
		return GuildContext{Guild: guild, Registered: registered, Settings: defaultGuildSettings()}
	}
	return GuildContext{Guild: guild, Registered: registered, Settings: settingsFromConfig(config)}
}

// settingsFromConfig is LoadGuildContext's pure decode step, split out so it
// only ever runs against the one config fetch above.
func settingsFromConfig(config *models.GuildConfigsModel) types.GuildSettings {
	prefix := strings.TrimSpace(config.Prefix)
	if prefix == "" {
		prefix = DefaultCommandPrefix
	}

	settings := types.GuildSettings{
		Prefix:        prefix,
		PrefixEnabled: config.IsPrefixEnabled,
		SlashEnabled:  config.IsSlashEnabled,
	}

	commandsMap := decodeCommandsMap(config.Commands)

	// Convert SDK CommandConfig to GuildCommandPermission
	convertedCommands := make(types.GuildCommandsMap)
	for cmdName, cmdConfig := range commandsMap {
		disabled := !cmdConfig.IsEnabled
		slashDisabled := !cmdConfig.IsSlashEnabled
		prefixDisabled := !cmdConfig.IsPrefixEnabled

		custom, staffOnly := len(cmdConfig.Builder) > 0, false
		if custom {
			var declared struct {
				Requirements []struct {
					Type string `json:"type"`
					Key  string `json:"key"`
				} `json:"requirements"`
			}
			if json.Unmarshal(cmdConfig.Builder, &declared) == nil {
				for _, q := range declared.Requirements {
					staffOnly = staffOnly || (q.Type == string(types.ReqRole) && q.Key == types.StaffRoleKey)
				}
			}
		}

		convertedCommands[cmdName] = types.GuildCommandPermission{
			Custom:             custom,
			StaffOnly:          staffOnly,
			Disabled:           &disabled,
			SlashDisabled:      &slashDisabled,
			PrefixDisabled:     &prefixDisabled,
			Aliases:            cmdConfig.Aliases,
			Cooldown:           cmdConfig.Cooldown,
			AllowedChannels:    convertMapToSlice(cmdConfig.AllowedChannels),
			DeniedChannels:     convertMapToSlice(cmdConfig.DeniedChannels),
			AllowedRoles:       convertMapToSlice(cmdConfig.AllowedRoles),
			DeniedRoles:        convertMapToSlice(cmdConfig.DeniedRoles),
			RequiredChannels:   cmdConfig.RequiredChannels,
			RequiredRoles:      cmdConfig.RequiredRoles,
			RequiredCategories: cmdConfig.RequiredCategories,
		}
	}
	settings.Commands = convertedCommands

	return settings
}

// decodeCommandsMap decodes a guild config's "commands" column, never returning nil.
func decodeCommandsMap(raw dbjson.JSON) map[string]types.CommandConfig {
	m := map[string]types.CommandConfig{}
	if !raw.IsEmpty() {
		_ = raw.Unmarshal(&m)
	}
	return m
}

func convertMapToSlice(m map[string]bool) []string {
	var result []string
	for key, value := range m {
		if value {
			result = append(result, key)
		}
	}
	return result
}

// ============================================================================
// Access Control
// ============================================================================

// CheckSlashAccess validates guild slash command permissions.
func CheckSlashAccess(settings types.GuildSettings, commandName, subcommandPath, channelID string, member *discordgo.Member, isAdmin bool) types.AccessDecision {
	if !settings.SlashEnabled {
		return types.AccessDecision{Allowed: false, Title: "Slash Commands Disabled", Message: "Slash commands are disabled in this server. Please try again later or contact a server administrator."}
	}

	// A command with no settings has no allowed roles, so only administrators get through (see checkRoleAccess).
	perm := settings.Commands[commandName]

	return checkAccess(perm, subcommandPath, channelID, member, isAdmin, false, roleGateFor(settings, commandName))
}

// CheckPrefixAccess validates guild prefix command permissions.
func CheckPrefixAccess(settings types.GuildSettings, commandName, subcommandPath, channelID string, member *discordgo.Member, isAdmin bool) types.AccessDecision {
	if !settings.PrefixEnabled {
		return types.AccessDecision{Allowed: false, Title: "Prefix Commands Disabled", Message: "Prefix commands are disabled in this server. Please try again later or contact a server administrator."}
	}

	// A command with no settings has no allowed roles, so only administrators get through (see checkRoleAccess).
	perm := settings.Commands[commandName]

	return checkAccess(perm, subcommandPath, channelID, member, isAdmin, true, roleGateFor(settings, commandName))
}

func checkAccess(perm types.GuildCommandPermission, subcommandPath, channelID string, member *discordgo.Member, isAdmin, isPrefix bool, gate roleGate) types.AccessDecision {
	// Check if command is enabled
	if perm.Disabled != nil && *perm.Disabled {
		return types.AccessDecision{Allowed: false, Title: "Command Disabled", Message: "This command is disabled in this server."}
	}

	// Check if slash/prefix is enabled
	if isPrefix {
		if perm.PrefixDisabled != nil && *perm.PrefixDisabled {
			return types.AccessDecision{Allowed: false, Title: "Prefix Disabled", Message: "This command is disabled for prefix usage in this server."}
		}
	} else {
		if perm.SlashDisabled != nil && *perm.SlashDisabled {
			return types.AccessDecision{Allowed: false, Title: "Slash Disabled", Message: "This command is disabled for slash usage in this server."}
		}
	}

	// Check subcommand permissions if applicable
	if subcommandPath != "" && perm.Subcommands != nil {
		if sub, ok := perm.Subcommands[subcommandPath]; ok {
			subEnabled := true
			if sub.Disabled != nil {
				subEnabled = !*sub.Disabled
			}

			subSlash := true
			if sub.SlashDisabled != nil {
				subSlash = !*sub.SlashDisabled
			}

			subPrefix := true
			if sub.PrefixDisabled != nil {
				subPrefix = !*sub.PrefixDisabled
			}

			if !subEnabled {
				return types.AccessDecision{Allowed: false, Title: "Subcommand Disabled", Message: fmt.Sprintf("The `%s` subcommand is disabled in this server.", subcommandPath)}
			}
			if isPrefix {
				if !subPrefix {
					return types.AccessDecision{Allowed: false, Title: "Prefix Disabled", Message: fmt.Sprintf("The `%s` subcommand is disabled for prefix usage.", subcommandPath)}
				}
			} else if !subSlash {
				return types.AccessDecision{Allowed: false, Title: "Slash Disabled", Message: fmt.Sprintf("The `%s` subcommand is disabled for slash usage.", subcommandPath)}
			}
			if decision := checkChannelAccess(sub.AllowedChannels, nil, channelID); !decision.Allowed {
				return decision
			}
			if len(sub.AllowedRoles) > 0 {
				return checkRoleAccess(sub.AllowedRoles, nil, member, isAdmin, gate)
			}
			return checkRoleAccess(perm.AllowedRoles, perm.DeniedRoles, member, isAdmin, gate)
		}
	}

	if decision := checkChannelAccess(perm.AllowedChannels, perm.DeniedChannels, channelID); !decision.Allowed {
		return decision
	}
	return checkRoleAccess(perm.AllowedRoles, perm.DeniedRoles, member, isAdmin, gate)
}

func checkChannelAccess(allowed, denied []string, channelID string) types.AccessDecision {
	if channelID == "" {
		return types.AccessDecision{Allowed: true}
	}
	for _, id := range denied {
		if strings.TrimSpace(id) == channelID {
			return types.AccessDecision{Allowed: false, Title: "Channel Not Allowed", Message: "This command cannot be used in this channel."}
		}
	}
	if len(allowed) == 0 {
		return types.AccessDecision{Allowed: true}
	}
	for _, id := range allowed {
		if strings.TrimSpace(id) == channelID {
			return types.AccessDecision{Allowed: true}
		}
	}
	return types.AccessDecision{Allowed: false, Title: "Channel Not Allowed", Message: "This command cannot be used in this channel."}
}

func checkRoleAccess(allowed, denied []string, member *discordgo.Member, isAdmin bool, gate roleGate) types.AccessDecision {
	if isAdmin {
		return types.AccessDecision{Allowed: true}
	}
	if member == nil {
		return types.AccessDecision{Allowed: false, Title: "Permission Denied", Message: noAccessMessage}
	}

	roleSet := make(map[string]struct{}, len(member.Roles))
	for _, roleID := range member.Roles {
		roleSet[roleID] = struct{}{}
	}

	for _, roleID := range denied {
		roleID = strings.TrimSpace(roleID)
		if roleID == "" {
			continue
		}
		if _, ok := roleSet[roleID]; ok {
			return types.AccessDecision{Allowed: false, Title: "Permission Denied", Message: noAccessMessage}
		}
	}

	// Staff commands need the staff role first. Allowed roles then narrow it further: when any are set the member must
	// hold one of them as well as the staff role.
	if gate.staff && !MemberHasRole(member, gate.staffID) {
		return types.AccessDecision{Allowed: false, Title: "Staff Only", Message: "This command is restricted to staff members. Please contact a staff member for assistance."}
	}

	// With no allowed roles, general commands are open to everyone and staff commands to the staff role. Any other
	// command is for administrators until the dashboard opens it up.
	if len(allowed) == 0 && (gate.staff || gate.openByDefault) {
		return types.AccessDecision{Allowed: true}
	}
	for _, roleID := range allowed {
		roleID = strings.TrimSpace(roleID)
		if roleID == "" {
			continue
		}
		if _, ok := roleSet[roleID]; ok {
			return types.AccessDecision{Allowed: true}
		}
	}
	return types.AccessDecision{Allowed: false, Title: "Permission Denied", Message: noAccessMessage}
}

// ============================================================================
// Command Requirements Validation
// ============================================================================

// LoadCommandRequirements returns cmdName's Required* slots that are marked
// Required but have no snowflake ID assigned yet — i.e. the command cannot run
// until an admin configures them (see settingsFromConfig for how a
// CommandConfig's Required* maps get here, and RespondRequirementsError/
// BuildRequirementFixComponents for how an admin resolves one inline). No
// database access of its own; settings is a single LoadGuildContext load.
func LoadCommandRequirements(settings types.GuildSettings, cmdName string) []types.Requirement {
	cmdConfig, ok := settings.Commands[cmdName]
	if !ok {
		return nil
	}

	var missing []types.Requirement
	for key, slot := range cmdConfig.RequiredChannels {
		if slot.Required && slot.ID == "" {
			missing = append(missing, types.Requirement{Type: types.ReqChannel, Key: key, ErrorMessage: fmt.Sprintf("Required channel %q is not configured", key)})
		}
	}
	for key, slot := range cmdConfig.RequiredRoles {
		if slot.Required && slot.ID == "" {
			missing = append(missing, types.Requirement{Type: types.ReqRole, Key: key, ErrorMessage: fmt.Sprintf("Required role %q is not configured", key)})
		}
	}
	for key, slot := range cmdConfig.RequiredCategories {
		if slot.Required && slot.ID == "" {
			missing = append(missing, types.Requirement{Type: types.ReqCategory, Key: key, ErrorMessage: fmt.Sprintf("Required category %q is not configured", key)})
		}
	}
	return missing
}

// LoadOptionalCommandRequirements returns cmdName's Required* slots that are
// marked optional (Required: false) and have no snowflake ID assigned yet.
// These never block execution — see LoadCommandRequirements for the slots
// that do — but are surfaced alongside a missing-requirements error, and
// offered via BuildRequirementFixComponents's post-setup Continue/Skip
// prompt, so an admin can configure them in the same pass if they want to.
func LoadOptionalCommandRequirements(settings types.GuildSettings, cmdName string) []types.Requirement {
	cmdConfig, ok := settings.Commands[cmdName]
	if !ok {
		return nil
	}

	var optional []types.Requirement
	for key, slot := range cmdConfig.RequiredChannels {
		if !slot.Required && slot.ID == "" {
			optional = append(optional, types.Requirement{Type: types.ReqChannel, Key: key, Optional: true})
		}
	}
	for key, slot := range cmdConfig.RequiredRoles {
		if !slot.Required && slot.ID == "" {
			optional = append(optional, types.Requirement{Type: types.ReqRole, Key: key, Optional: true})
		}
	}
	for key, slot := range cmdConfig.RequiredCategories {
		if !slot.Required && slot.ID == "" {
			optional = append(optional, types.Requirement{Type: types.ReqCategory, Key: key, Optional: true})
		}
	}
	return optional
}

// ============================================================================
// Prefix Command Resolution
// ============================================================================

// ResolvePrefixRoute resolves a prefix trigger and remaining args to a command target.
func ResolvePrefixRoute(registry map[string]*types.Command, settings types.GuildSettings, trigger string, args []string) (*types.PrefixCommandRoute, *types.Command, string) {
	trigger = strings.ToLower(strings.TrimSpace(trigger))
	if trigger == "" {
		return nil, nil, ""
	}

	if route, cmd := resolveSubcommandAlias(registry, settings, trigger, args); route != nil {
		return route, cmd, ""
	}

	cmd := resolveCommandTrigger(registry, settings, trigger)
	if cmd == nil {
		return nil, nil, ""
	}

	route, errMsg := buildRouteFromArgs(cmd, settings.Commands[cmd.Name], args)
	if route == nil {
		return nil, cmd, errMsg
	}
	return route, cmd, ""
}

func resolveCommandTrigger(commandRegistry map[string]*types.Command, settings types.GuildSettings, trigger string) *types.Command {
	if cmd, ok := commandRegistry[trigger]; ok && cmd.Data != nil {
		return cmd
	}
	for name, perm := range settings.Commands {
		for _, alias := range perm.Aliases {
			if strings.EqualFold(alias, trigger) {
				if cmd, ok := commandRegistry[name]; ok {
					return cmd
				}
			}
		}
	}
	return nil
}

func resolveSubcommandAlias(commandRegistry map[string]*types.Command, settings types.GuildSettings, trigger string, args []string) (*types.PrefixCommandRoute, *types.Command) {
	for commandName, perm := range settings.Commands {
		if perm.Subcommands == nil {
			continue
		}
		for subPath, sub := range perm.Subcommands {
			for _, alias := range sub.Aliases {
				if !strings.EqualFold(alias, trigger) {
					continue
				}
				cmd, ok := commandRegistry[commandName]
				if !ok || cmd.Data == nil {
					continue
				}
				return &types.PrefixCommandRoute{
					CommandName:    commandName,
					SubcommandPath: subPath,
					RemainingArgs:  append([]string{}, args...),
				}, cmd
			}
		}
	}
	return nil, nil
}

func buildRouteFromArgs(cmd *types.Command, perm types.GuildCommandPermission, args []string) (*types.PrefixCommandRoute, string) {
	if cmd == nil || cmd.Data == nil {
		return nil, "Could not resolve that command."
	}
	if len(cmd.Data.Options) == 0 {
		return &types.PrefixCommandRoute{
			CommandName:    cmd.Data.Name,
			SubcommandPath: "",
			RemainingArgs:  append([]string{}, args...),
		}, ""
	}

	remaining := append([]string{}, args...)
	consumed := make([]string, 0)
	current := cmd.Data.Options
	groupName := ""

	for len(current) > 0 && isSubcommandContainer(current[0].Type) {
		if len(remaining) == 0 {
			names := make([]string, 0, len(current))
			for _, opt := range current {
				names = append(names, "`"+opt.Name+"`")
			}
			return nil, "Missing subcommand. Expected one of: " + strings.Join(names, ", ")
		}

		trigger := strings.ToLower(strings.TrimSpace(remaining[0]))
		matched := matchSubcommandOption(current, perm, groupName, trigger)
		if matched == nil {
			return nil, fmt.Sprintf("Invalid subcommand for `%s`.", cmd.Data.Name)
		}

		consumed = append(consumed, trigger)
		remaining = remaining[1:]

		if matched.Type == discordgo.ApplicationCommandOptionSubCommandGroup {
			groupName = matched.Name
			current = matched.Options
			continue
		}
		break
	}

	subPath := subPathFromConsumed(cmd.Data.Options, consumed)
	return &types.PrefixCommandRoute{
		CommandName:    cmd.Data.Name,
		SubcommandPath: subPath,
		RemainingArgs:  remaining,
	}, ""
}

func matchSubcommandOption(options []*discordgo.ApplicationCommandOption, perm types.GuildCommandPermission, groupName, trigger string) *discordgo.ApplicationCommandOption {
	for _, opt := range options {
		if opt.Name == trigger {
			return opt
		}
	}
	if perm.Subcommands == nil {
		return nil
	}
	for _, opt := range options {
		key := opt.Name
		if groupName != "" {
			key = groupName + "." + opt.Name
		}
		sub, ok := perm.Subcommands[key]
		if !ok {
			continue
		}
		for _, alias := range sub.Aliases {
			if strings.EqualFold(alias, trigger) {
				return opt
			}
		}
	}
	return nil
}

func subPathFromConsumed(options []*discordgo.ApplicationCommandOption, consumed []string) string {
	if len(consumed) == 0 {
		return ""
	}
	current := options
	group := ""
	for _, token := range consumed {
		var matched *discordgo.ApplicationCommandOption
		for _, opt := range current {
			if opt.Name == token {
				matched = opt
				break
			}
		}
		if matched == nil {
			break
		}
		switch matched.Type {
		case discordgo.ApplicationCommandOptionSubCommandGroup:
			group = matched.Name
			current = matched.Options
		case discordgo.ApplicationCommandOptionSubCommand:
			if group != "" {
				return group + "." + matched.Name
			}
			return matched.Name
		}
	}
	return group
}

func isSubcommandContainer(optType discordgo.ApplicationCommandOptionType) bool {
	return optType == discordgo.ApplicationCommandOptionSubCommand || optType == discordgo.ApplicationCommandOptionSubCommandGroup
}

// SubcommandPathFromInteraction returns the subcommand path from interaction data.
func SubcommandPathFromInteraction(data discordgo.ApplicationCommandInteractionData) string {
	if len(data.Options) == 0 {
		return ""
	}
	opt := data.Options[0]
	switch opt.Type {
	case discordgo.ApplicationCommandOptionSubCommand:
		return opt.Name
	case discordgo.ApplicationCommandOptionSubCommandGroup:
		if len(opt.Options) == 0 {
			return opt.Name
		}
		if opt.Options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
			return opt.Name + "." + opt.Options[0].Name
		}
		return opt.Name
	default:
		return ""
	}
}

const noAccessMessage = "You aren't allowed to use this command. If you think this is a mistake, contact a server administrator."

// RespondAccessDenied answers a denied slash command with the access-denied embed.
func RespondAccessDenied(s *discordgo.Session, i *discordgo.InteractionCreate, ephemeral bool, decision types.AccessDecision) {
	if IsDeferredSlashInteraction(i) {
		_ = EditInteractionResponse(s, i, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{AccessDeniedEmbed(decision.Title, decision.Message)}})
		return
	}
	_ = Respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{AccessDeniedEmbed(decision.Title, decision.Message)},
			Flags:  messageFlags(ephemeral),
		},
	})
}

// RunSlashCommand runs cmd for the interaction through the same checks and pipeline as a typed slash command: it
// defers the reply (privately when ephemeral), then applies the guild's access rules and requirements.
func RunSlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate, cmd *types.Command, ephemeral bool) {
	if cmd == nil || cmd.Execute == nil {
		return
	}
	if cmd.Disabled {
		RespondSimpleError(s, i, true, "This command is currently disabled globally.")
		return
	}
	if err := DeferSlashInteraction(s, i, ephemeral); err != nil {
		return
	}
	defer ClearDeferredSlashInteraction(i)

	EnsureInteractionMember(s, i)

	// One guild+config fetch for the whole invocation: settings drives the access checks here, and gc is passed
	// straight into the pipeline instead of it loading the same data again.
	gc := LoadGuildContext(i.GuildID)
	if !gc.Settings.SlashEnabled {
		RespondSimpleError(s, i, ephemeral, "Slash commands are disabled in this server.")
		return
	}

	subPath := SubcommandPathFromInteraction(i.ApplicationCommandData())
	if decision := CheckSlashAccess(gc.Settings, cmd.Name, subPath, i.ChannelID, i.Member, MemberIsAdministrator(s, i)); !decision.Allowed {
		RespondAccessDenied(s, i, true, decision)
		return
	}

	_ = RunCommandPipeline(s, i, cmd, gc, PipelineOptions{SkipGuildRegistration: cmd.SkipGuildCheck})
}

// logModerationUsage posts the command, its options and the moderator to the optional moderation_command_logs channel.
func logModerationUsage(s *discordgo.Session, i *discordgo.InteractionCreate, cmd *types.Command, userID string) {
	channelID, err := RequirementSlotID(i.GuildID, cmd.Name, types.ReqChannel, types.ModerationLogsKey)
	if err != nil || channelID == "" {
		return
	}
	var args []string
	for _, opt := range i.ApplicationCommandData().Options {
		val := fmt.Sprint(opt.Value)
		if opt.Type == discordgo.ApplicationCommandOptionUser {
			val = "<@" + val + ">"
		}
		args = append(args, opt.Name+": "+val)
	}
	_, _ = s.ChannelMessageSendEmbed(channelID, &discordgo.MessageEmbed{
		Title:       "/" + cmd.Name,
		Description: strings.Join(args, "\n"),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Moderator", Value: "<@" + userID + ">", Inline: true},
			{Name: "Channel", Value: "<#" + i.ChannelID + ">", Inline: true},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

// FindCommandFor resolves a slash command name for a guild: a built-in command, or one of the guild's own custom commands.
func FindCommandFor(guildID, name string) *types.Command {
	if c := FindCommand(name); c != nil {
		return c
	}
	return FindCustomCommand(guildID, name)
}

// HandleSlashCommand runs the slash command the interaction invokes.
func HandleSlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cmd := FindCommandFor(i.GuildID, i.ApplicationCommandData().Name)
	if cmd == nil {
		return
	}
	ephemeral := cmd.Ephemeral
	if e := entries(i.GuildID)[cmd.Name]; e != nil && e.cmd == cmd {
		// A custom command is private or public by the called subcommand's own default reply.
		sub, _ := customCommandPath(i.ApplicationCommandData(), &e.b)
		if b := e.b.bodyFor(sub); b != nil {
			ephemeral = b.Reply.Ephemeral
		}
	}
	RunSlashCommand(s, i, cmd, ephemeral)
}

// HandleAutocomplete answers an autocomplete request of the command it belongs to.
func HandleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cmd := FindCommandFor(i.GuildID, i.ApplicationCommandData().Name)
	if cmd == nil || cmd.Autocomplete == nil || cmd.Disabled {
		return
	}
	cmd.Autocomplete(s, i)
}

// ErrRequirementNotConfigured means the channel a command posts to has not been assigned yet.
var ErrRequirementNotConfigured = errors.New("command channel requirement is not configured")

// SendCommandChannelMessage posts msg to the channel an admin assigned to
// cmdName's channel requirement `key` (see /config channel set). The
// notifications of commands like /party go through here: their
// channel is a declared requirement of the command, so it is always the one
// the server configured.
func SendCommandChannelMessage(s *discordgo.Session, guildID, cmdName, key string, msg *discordgo.MessageSend) error {
	channelID, err := RequirementSlotID(guildID, cmdName, types.ReqChannel, key)
	if err != nil {
		return err
	}
	if channelID == "" {
		return ErrRequirementNotConfigured
	}
	_, err = s.ChannelMessageSendComplex(channelID, msg)
	return err
}

// ============================================================================
// Message component (button/select) handlers registered by commands
// ============================================================================

// ComponentHandler is defined in package types (commands.go).
type ComponentHandler = types.ComponentHandler

var (
	componentMu       sync.RWMutex
	componentHandlers []ComponentHandler
)

// RegisterComponentHandler adds a handler for buttons/selects a command posts.
// Call it from the command file's init().
func RegisterComponentHandler(h ComponentHandler) {
	componentMu.Lock()
	componentHandlers = append(componentHandlers, h)
	componentMu.Unlock()
}

// HandleRegisteredComponent offers customID to every registered handler.
func HandleRegisteredComponent(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	componentMu.RLock()
	handlers := append([]ComponentHandler(nil), componentHandlers...)
	componentMu.RUnlock()
	for _, h := range handlers {
		if h(s, i, customID) {
			return true
		}
	}
	return false
}

// SettingRole returns the role an admin assigned to cmdName's role requirement `key` ("" when unset).
func SettingRole(guildID, cmdName, key string) string {
	id, err := RequirementSlotID(guildID, cmdName, types.ReqRole, key)
	if err != nil {
		return ""
	}
	return id
}

// LogCommandResult posts embed, with the moderator who ran it, to every log channel assigned to cmdName (a pair like
// chatmute/chatunmute shares the first command's channels). Nothing is posted when none is set.
func LogCommandResult(s *discordgo.Session, guildID, cmdName, actorID string, embed *discordgo.MessageEmbed) {
	slots, _, err := CommandRequirementSlots(guildID, cmdName, types.ReqChannel)
	if err != nil || embed == nil {
		return
	}
	logged := *embed
	logged.Fields = append(append([]*discordgo.MessageEmbedField{}, embed.Fields...), &discordgo.MessageEmbedField{Name: "Moderator", Value: "<@" + actorID + ">", Inline: true})
	sent := map[string]bool{}
	for key, slot := range slots {
		if key == types.ModerationLogsKey || slot.ID == "" || sent[slot.ID] {
			continue
		}
		sent[slot.ID] = true
		_, _ = s.ChannelMessageSendEmbed(slot.ID, &logged)
	}
}

// ============================================================================
// Running a command
// ============================================================================

// Steps run in order; a failing or stopping step answers with its message and ends the command. Then the first
// reply whose condition holds (or the default reply) is sent with {{variables}} filled in.

type runner struct {
	s       *discordgo.Session
	i       *discordgo.InteractionCreate
	e       *entry
	name    string
	invoker *discordgo.User
	vars    map[string]string
	users   map[string]*discordgo.User // user and player options by option name

	// ephemeral is what the reply was deferred as: the called subcommand's default reply decides it before any step runs.
	ephemeral bool
}

func run(s *discordgo.Session, i *discordgo.InteractionCreate, name string) error {
	e := entries(i.GuildID)[name]
	if e == nil {
		return EditInteractionEmbed(s, i, SimpleErrorEmbed("This command no longer exists."))
	}
	data := i.ApplicationCommandData()
	opts, sub := data.Options, ""
	if len(e.b.SubcommandGroups) > 0 {
		if len(opts) == 0 || len(opts[0].Options) == 0 {
			return EditInteractionEmbed(s, i, SimpleErrorEmbed("Pick a subcommand to run."))
		}
		sub, opts = opts[0].Name+" "+opts[0].Options[0].Name, opts[0].Options[0].Options
	} else if len(e.b.Subcommands) > 0 {
		if len(opts) == 0 {
			return EditInteractionEmbed(s, i, SimpleErrorEmbed("Pick a subcommand to run."))
		}
		sub, opts = opts[0].Name, opts[0].Options
	}
	b := e.b.bodyFor(sub)
	if b == nil {
		return EditInteractionEmbed(s, i, SimpleErrorEmbed("That subcommand no longer exists."))
	}

	invoker := i.User
	if i.Member != nil && i.Member.User != nil {
		invoker = i.Member.User
	}
	r := &runner{s: s, i: i, e: e, name: name, invoker: invoker, users: map[string]*discordgo.User{}, ephemeral: b.Reply.Ephemeral}
	r.vars = r.baseVars(sub)
	r.readOptions(b, opts, data)

	for _, st := range b.Steps {
		if st.When != nil && !r.holds(*st.When) {
			continue
		}
		if msg, stopped := r.exec(st); stopped {
			return EditInteractionEmbed(s, i, SimpleErrorEmbed(r.fill(msg)))
		}
	}

	chosen := b.Reply
	for _, rule := range b.ReplyRules {
		if r.holds(rule.When) {
			chosen = rule.Reply
			break
		}
	}
	return r.send(chosen)
}

// ============================================================================
// Variables
// ============================================================================

func (r *runner) fill(text string) string { return FillVars(text, r.vars) }

func (r *runner) baseVars(sub string) map[string]string {
	v := map[string]string(GuildVars(r.s, r.i.GuildID, r.invoker))
	now := time.Now().UTC()
	v["time.date"], v["time.time"] = now.Format("2006-01-02"), now.Format("15:04")
	v["time.unix"], v["time.relative"] = itoa(int(now.Unix())), fmt.Sprintf("<t:%d:R>", now.Unix())
	v["channel.id"], v["channel.mention"] = r.i.ChannelID, "<#"+r.i.ChannelID+">"
	if ch, err := r.s.State.Channel(r.i.ChannelID); err == nil && ch != nil {
		v["channel.name"] = ch.Name
	}
	v["command.name"], v["command.subcommand"] = r.name, sub
	v["command.prefix"] = LoadGuildContext(r.i.GuildID).Settings.Prefix
	if r.invoker != nil {
		v["user.id"] = r.invoker.ID
	}
	return v
}

func (r *runner) readOptions(b *body, given []*discordgo.ApplicationCommandInteractionDataOption, data discordgo.ApplicationCommandInteractionData) {
	byName := map[string]*discordgo.ApplicationCommandInteractionDataOption{}
	for _, o := range given {
		byName[o.Name] = o
	}
	for _, def := range b.Options {
		o := byName[def.Name]
		key := "opt." + def.Name
		if o == nil {
			r.vars[key] = ""
			continue
		}
		switch o.Type {
		case discordgo.ApplicationCommandOptionUser, discordgo.ApplicationCommandOptionMentionable:
			u := o.UserValue(r.s)
			r.users[def.Name] = u
			r.vars[key], r.vars[key+".id"], r.vars[key+".mention"] = u.Username, u.ID, "<@"+u.ID+">"
			if def.Type == "player" {
				if view, err := database.Must().Player.Get(r.i.GuildID, u.ID); err == nil && view.Config != nil {
					r.vars[key] = view.Config.Username
				}
			}
		case discordgo.ApplicationCommandOptionRole:
			id := fmt.Sprint(o.Value)
			r.vars[key], r.vars[key+".id"], r.vars[key+".mention"] = "<@&"+id+">", id, "<@&"+id+">"
		case discordgo.ApplicationCommandOptionChannel:
			id := fmt.Sprint(o.Value)
			r.vars[key], r.vars[key+".id"], r.vars[key+".mention"] = "<#"+id+">", id, "<#"+id+">"
		case discordgo.ApplicationCommandOptionAttachment:
			id := fmt.Sprint(o.Value)
			if data.Resolved != nil && data.Resolved.Attachments[id] != nil {
				r.vars[key] = data.Resolved.Attachments[id].URL
			}
		case discordgo.ApplicationCommandOptionNumber:
			r.vars[key] = ftoa(o.FloatValue())
		case discordgo.ApplicationCommandOptionInteger:
			r.vars[key] = itoa(int(o.IntValue()))
		default:
			r.vars[key] = fmt.Sprint(o.Value)
		}
	}
}

// who resolves a step's target: the invoker, or a user/player option.
func (r *runner) who(target string) *discordgo.User {
	if target == "" || target == "@self" {
		return r.invoker
	}
	return r.users[strings.TrimPrefix(target, "opt:")]
}

// ============================================================================
// Conditions
// ============================================================================

func (r *runner) holds(c condition) bool {
	left, right := strings.TrimSpace(r.fill(c.Left)), strings.TrimSpace(r.fill(c.Right))
	ln, lerr := strconv.ParseFloat(left, 64)
	rn, rerr := strconv.ParseFloat(right, 64)
	numeric := lerr == nil && rerr == nil
	switch c.Op {
	case "eq":
		if numeric {
			return ln == rn
		}
		return strings.EqualFold(left, right)
	case "ne":
		if numeric {
			return ln != rn
		}
		return !strings.EqualFold(left, right)
	case "gt":
		return numeric && ln > rn
	case "gte":
		return numeric && ln >= rn
	case "lt":
		return numeric && ln < rn
	case "lte":
		return numeric && ln <= rn
	case "contains":
		return strings.Contains(strings.ToLower(left), strings.ToLower(right))
	case "not_empty":
		return left != ""
	case "empty":
		return left == ""
	}
	return false
}

// ============================================================================
// Steps
// ============================================================================

// exec runs one step. It reports true with the message to answer when the step stops the command.
func (r *runner) exec(st step) (string, bool) {
	stop := func(def string) (string, bool) {
		if st.Message != "" {
			return st.Message, true
		}
		return def, true
	}
	guildID := r.i.GuildID

	switch st.Type {
	case "require_player", "load_stats":
		u := r.who(st.Target)
		if u == nil {
			return stop("I could not tell who that is.")
		}
		view, err := database.Must().Player.Get(guildID, u.ID)
		if err != nil {
			if sdk.IsNotFound(err) {
				return stop(u.Mention() + " is not registered.")
			}
			return stop("Something went wrong. Please try again later.")
		}
		r.vars["player.name"], r.vars["player.id"], r.vars["player.mention"] = view.Config.Username, u.ID, u.Mention()
		r.vars["player.avatar"], r.vars["player.head"], r.vars["player.body"] = AvatarOf(u), HeadURL(view.Config.Username), BodyURL(view.Config.Username)
		r.vars["player.joined"] = view.Config.CreatedAt.Format("2006-01-02")
		if st.Type == "load_stats" {
			seasonKey, seasonID, err := ActiveSeasonKey(guildID)
			if err != nil || seasonKey == "" {
				return stop("There is no active season.")
			}
			playerID := sdk.UintToString(view.Player.ID)
			stats, err := database.Must().Stats.GetOrCreate(guildID, playerID, seasonID)
			if err != nil {
				return stop("Something went wrong. Please try again later.")
			}
			position := 0
			if rank, err := database.Must().Stats.Rank(guildID, playerID, seasonID); err == nil {
				position = rank.Position
			}
			for k, v := range StatsVars(u, view.Config, stats, position, seasonKey, seasonID) {
				r.vars[k] = v
			}
		}

	case "load_leaderboard":
		seasonKey, seasonID, err := ActiveSeasonKey(guildID)
		if err != nil || seasonKey == "" {
			return stop("There is no active season.")
		}
		limit := min(max(st.Limit, 1), 10)
		rows, err := database.Must().Stats.Leaderboard(guildID, seasonID, sdk.Page{Limit: limit})
		if err != nil {
			return stop("Something went wrong. Please try again later.")
		}
		vars := Vars{}
		LeaderboardRowVars(r.s, guildID, rows.Data, vars)
		for k, v := range vars {
			r.vars[k] = v
		}
		r.vars["lb.title"], r.vars["season.name"], r.vars["season.id"] = seasonKey+" Leaderboard", seasonKey, itoa(int(seasonID))

	case "load_game":
		number := atoi(strings.TrimSpace(r.fill(st.Left)))
		game, ok := Games.ByNumber(guildID, number)
		if !ok {
			return stop(fmt.Sprintf("Game #%d was not found.", number))
		}
		r.vars["game.number"], r.vars["game.status"], r.vars["game.map"] = itoa(game.Number), string(game.State), game.Map
		r.vars["game.mode"], r.vars["game.bot"] = game.Mode.Name, game.Bot
		r.vars["game.started_at"], r.vars["game.ended_at"] = "", ""
		if !game.StartedAt.IsZero() {
			r.vars["game.started_at"] = fmt.Sprintf("<t:%d:R>", game.StartedAt.Unix())
		}
		if (game.State == GameCleanup || game.State == GameVoided) && !game.StateAt.IsZero() {
			r.vars["game.ended_at"] = fmt.Sprintf("<t:%d:R>", game.StateAt.Unix())
		}

	case "load_recent_games":
		u := r.who(st.Target)
		if u == nil {
			return stop("I could not tell who that is.")
		}
		view, err := database.Must().Player.Get(guildID, u.ID)
		if err != nil {
			return stop(u.Mention() + " is not registered.")
		}
		_, seasonID, _ := ActiveSeasonKey(guildID)
		all, err := database.Must().Player.AllGames(guildID, sdk.UintToString(view.Player.ID))
		if err != nil {
			return stop("Something went wrong. Please try again later.")
		}
		for n := 1; n <= 5; n++ { // rows the player does not have are blank, not left as {{recent.N.*}}
			k := fmt.Sprintf("recent.%d.", n)
			r.vars[k+"number"], r.vars[k+"result"], r.vars[k+"map"] = "", "", ""
		}
		for n, h := range FilterGameHistoryEntries(all, seasonID, 0, false) {
			if n >= min(max(st.Limit, 1), 5) {
				break
			}
			result := "Loss"
			if h.IsWinner {
				result = "Win"
			}
			k := fmt.Sprintf("recent.%d.", n+1)
			r.vars[k+"number"], r.vars[k+"result"], r.vars[k+"map"] = itoa(h.Game.GameNumber), result, h.Game.MapName
		}

	case "load_commands":
		r.vars["commands.list"], r.vars["commands.count"] = r.commandList(strings.TrimSpace(st.Value))

	case "check":
		if !r.holds(condition{Left: st.Left, Op: st.Op, Right: st.Right}) {
			return stop("That is not allowed right now.")
		}
	case "stop":
		return stop("Stopped.")

	case "set_var":
		r.vars["var."+st.Name] = r.fill(st.Value)
	case "math":
		a, b := atof(strings.TrimSpace(r.fill(st.Left))), atof(strings.TrimSpace(r.fill(st.Right)))
		var out float64
		switch st.MathOp {
		case "+":
			out = a + b
		case "-":
			out = a - b
		case "*":
			out = a * b
		case "/", "%":
			if b == 0 {
				return stop("You cannot divide by zero.")
			}
			out = a / b
			if st.MathOp == "%" {
				out = math.Mod(a, b)
			}
		}
		r.vars["var."+st.Name] = ftoa(math.Round(out*1e6) / 1e6)
	case "random_pick":
		var lines []string
		for _, l := range strings.Split(st.Value, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) > 0 {
			r.vars["var."+st.Name] = r.fill(lines[rand.Intn(len(lines))])
		}
	case "random_number":
		lo, hi := atoi(strings.TrimSpace(r.fill(st.Left))), atoi(strings.TrimSpace(r.fill(st.Right)))
		if hi < lo {
			lo, hi = hi, lo
		}
		r.vars["var."+st.Name] = itoa(lo + rand.Intn(hi-lo+1))

	case "require_role":
		id := r.e.cfg.RequiredRoles[st.Name].ID
		if id == "" || !hasRole(r.i.Member, id) {
			if id == "" {
				return stop("That role requirement is not configured.")
			}
			return stop("You need the <@&" + id + "> role for this.")
		}
	case "check_role":
		u := r.who(st.Target)
		id := r.e.cfg.RequiredRoles[st.Name].ID
		if u == nil || id == "" {
			return stop("I could not check that user's role. Check the user option and configured role.")
		}
		member := r.i.Member
		if member == nil || member.User == nil || member.User.ID != u.ID {
			var err error
			member, err = FetchGuildMember(r.s, guildID, u.ID)
			if err != nil || member == nil {
				return stop("I could not find that member in this server.")
			}
		}
		if !hasRole(member, id) {
			return stop("That user does not have the <@&" + id + "> role.")
		}
	case "add_role", "remove_role":
		u := r.who(st.Target)
		id := r.e.cfg.RequiredRoles[st.Name].ID
		if u == nil || id == "" {
			return stop("I could not change that role.")
		}
		var err error
		if st.Type == "add_role" {
			err = r.s.GuildMemberRoleAdd(r.i.GuildID, u.ID, id)
		} else {
			err = r.s.GuildMemberRoleRemove(r.i.GuildID, u.ID, id)
		}
		if err != nil {
			return stop("I could not change that role. Check my permissions and role order.")
		}
	case "send_message":
		if id := r.e.cfg.RequiredChannels[st.Name].ID; id != "" {
			if _, err := r.s.ChannelMessageSendComplex(id, &discordgo.MessageSend{Content: r.fill(st.Value), AllowedMentions: &discordgo.MessageAllowedMentions{}}); err != nil {
				logger.Warn(fmt.Sprintf("sending custom command message in %s: %v", id, err))
				return stop("I could not send the message. Check the bot's channel permissions.")
			}
		}
	case "send_payload":
		id := r.e.cfg.RequiredChannels[st.Name].ID
		if id == "" {
			return stop("That channel is not configured.")
		}
		var payload map[string]any
		if st.Reply.MessageID != "" {
			payload = r.savedMessage(st.Reply.MessageID)
		} else {
			payload = r.replyPayload(st.Reply)
		}
		if payload == nil {
			return stop("I could not load the selected rich message.")
		}
		if _, err := SendPayload(r.s, id, payload); err != nil {
			logger.Warn(fmt.Sprintf("sending custom command rich message in %s: %v", id, err))
			return stop("I could not send the rich message. Check the bot's channel permissions.")
		}
	case "dm_user":
		u := r.who(st.Target)
		if u == nil {
			return stop("I could not find the user to message.")
		}
		dm, err := r.s.UserChannelCreate(u.ID)
		if err == nil {
			_, err = r.s.ChannelMessageSendComplex(dm.ID, &discordgo.MessageSend{Content: r.fill(st.Value), AllowedMentions: &discordgo.MessageAllowedMentions{}})
		}
		if err != nil {
			logger.Warn(fmt.Sprintf("sending custom command DM to %s: %v", u.ID, err))
			return stop("I could not send that DM. The user may have DMs disabled.")
		}
	}
	return "", false
}

func hasRole(m *discordgo.Member, id string) bool {
	if m == nil {
		return false
	}
	for _, r := range m.Roles {
		if r == id {
			return true
		}
	}
	return false
}

// commandList is every enabled command the invoker may use, one line each, and how many. category filters by
// the command's category (or its start, so "general" takes "general/game" too).
func (r *runner) commandList(category string) (string, string) {
	gc := LoadGuildContext(r.i.GuildID)
	admin := MemberIsAdministrator(r.s, r.i)
	all := map[string]*types.Command{}
	for _, c := range CommandRegistry {
		if c != nil && c.Data != nil && !c.Disabled {
			all[c.Data.Name] = c
		}
	}
	for name, c := range CustomCommandsFor(r.i.GuildID) {
		all[name] = c
	}
	var lines []string
	for name, c := range all {
		if category != "" && !strings.HasPrefix(c.Category, category) {
			continue
		}
		if perm, ok := gc.Settings.Commands[name]; ok && perm.Disabled != nil && *perm.Disabled {
			continue
		}
		if !CheckSlashAccess(gc.Settings, name, "", r.i.ChannelID, r.i.Member, admin).Allowed {
			continue
		}
		lines = append(lines, fmt.Sprintf("`/%s` %s", name, c.Description))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), itoa(len(lines))
}

// ============================================================================
// Replies
// ============================================================================

func (r *runner) buttonRow(rep reply) []discordgo.MessageComponent {
	var row []discordgo.MessageComponent
	for _, b := range rep.Buttons {
		if url := r.fill(b.URL); url != "" {
			button := discordgo.Button{Style: discordgo.LinkButton, Label: r.fill(b.Label), URL: url, Disabled: b.Disabled}
			if b.Emoji != "" {
				button.Emoji = &discordgo.ComponentEmoji{Name: b.Emoji}
			}
			row = append(row, button)
		}
	}
	if len(row) == 0 {
		return nil
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: row}}
}

func (r *runner) replyPayload(rep reply) map[string]any {
	doc := &MessageDoc{AllowMentions: false}
	switch rep.Type {
	case "component":
		doc.Mode = "v2"
		doc.V2 = rep.Components
	case "embed":
		doc.Mode = "embeds"
		doc.Content = rep.Content
		doc.Embeds = rep.Embeds
		if len(doc.Embeds) == 0 {
			legacy := Embed{Title: rep.Title, Description: rep.Text, Color: rep.Color, Thumbnail: rep.Thumbnail}
			legacy.Footer.Text = rep.Footer
			doc.Embeds = []Embed{legacy}
		}
		doc.Buttons = rep.Buttons
	default:
		doc.Mode = "embeds"
		doc.Content = rep.Content
		if doc.Content == "" {
			doc.Content = rep.Text
		}
		doc.Buttons = rep.Buttons
	}
	return doc.Payload(r.fill)
}

// embedOf is the embed an "embed" reply describes, with its variables filled in.
func (r *runner) embedOf(rep reply) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{Title: r.fill(rep.Title), Description: r.fill(rep.Text)}
	if len(rep.Color) == 7 && rep.Color[0] == '#' {
		if n, err := strconv.ParseInt(rep.Color[1:], 16, 32); err == nil {
			e.Color = int(n)
		}
	}
	if rep.Footer != "" {
		e.Footer = &discordgo.MessageEmbedFooter{Text: r.fill(rep.Footer)}
	}
	if rep.Thumbnail != "" {
		e.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: r.fill(rep.Thumbnail)}
	}
	return e
}

// sendAs answers with a reply whose privacy differs from the one the response was deferred with (a reply rule that is
// private while the default is public, or the other way round): Discord cannot change that afterwards, so the deferred
// message is removed and the reply goes out as a follow-up with the privacy it asked for.
func (r *runner) sendAs(rep reply) error {
	s, i := r.s, r.i
	var flags discordgo.MessageFlags
	if rep.Ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}

	if rep.Type == "message" {
		if payload := r.savedMessage(rep.MessageID); payload != nil {
			bits, _ := payload["flags"].(int)
			payload["flags"] = bits | int(flags)
			_ = s.InteractionResponseDelete(i.Interaction)
			_, err := s.RequestWithBucketID("POST", discordgo.EndpointWebhookToken(i.AppID, i.Token), payload, discordgo.EndpointWebhookToken(i.AppID, ""))
			return err
		}
	}
	if rep.Type == "message" || rep.Type == "embed" || rep.Type == "component" {
		payload := r.replyPayload(rep)
		if flags != 0 {
			bits, _ := payload["flags"].(int)
			payload["flags"] = bits | int(flags)
		}
		_ = s.InteractionResponseDelete(i.Interaction)
		_, err := s.RequestWithBucketID("POST", discordgo.EndpointWebhookToken(i.AppID, i.Token), payload, discordgo.EndpointWebhookToken(i.AppID, ""))
		return err
	}

	params := &discordgo.WebhookParams{Flags: flags, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	switch {
	case rep.Type == "message":
		params.Embeds = []*discordgo.MessageEmbed{SimpleErrorEmbed("The saved message for this reply was deleted.")}
	case rep.Type == "embed":
		params.Embeds = []*discordgo.MessageEmbed{r.embedOf(rep)}
	default:
		if img := r.bannerIf(rep); img != nil {
			params.Content = r.fill(rep.Text)
			params.Files = []*discordgo.File{{Name: r.name + ".png", ContentType: "image/png", Reader: bytes.NewReader(img)}}
		} else if params.Content = r.fill(rep.Text); strings.TrimSpace(params.Content) == "" {
			params.Content = "Done."
		}
	}
	if row := r.buttonRow(rep); row != nil && params.Files == nil {
		params.Components = row
	}
	_ = s.InteractionResponseDelete(i.Interaction)
	_, err := s.FollowupMessageCreate(i.Interaction, true, params)
	return err
}

// bannerIf is the banner of a "banner" reply, or nil for any other kind of reply.
func (r *runner) bannerIf(rep reply) []byte {
	if rep.Type != "banner" {
		return nil
	}
	return r.banner()
}

func (r *runner) send(rep reply) error {
	s, i := r.s, r.i
	if rep.Ephemeral != r.ephemeral && !IsPrefixInteraction(i) {
		return r.sendAs(rep)
	}
	switch rep.Type {
	case "message":
		if payload := r.savedMessage(rep.MessageID); payload != nil {
			if IsPrefixInteraction(i) {
				_, err := SendPayload(s, i.ChannelID, payload)
				return err
			}
			return EditOriginalPayload(s, i.Interaction, payload)
		}
		if rep.MessageID != "" {
			return EditInteractionEmbed(s, i, SimpleErrorEmbed("The saved message for this reply was deleted."))
		}
		payload := r.replyPayload(rep)
		if IsPrefixInteraction(i) {
			_, err := SendPayload(s, i.ChannelID, payload)
			return err
		}
		return EditOriginalPayload(s, i.Interaction, payload)

	case "embed", "component":
		payload := r.replyPayload(rep)
		if IsPrefixInteraction(i) {
			_, err := SendPayload(s, i.ChannelID, payload)
			return err
		}
		return EditOriginalPayload(s, i.Interaction, payload)

	case "banner":
		if img := r.banner(); img != nil {
			return EditInteractionFile(s, i, r.fill(rep.Text), r.name+".png", img)
		}
	}
	text := r.fill(rep.Text)
	if strings.TrimSpace(text) == "" {
		text = "Done."
	}
	edit := &discordgo.WebhookEdit{Content: &text, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	if row := r.buttonRow(rep); row != nil {
		edit.Components = &row
	}
	return EditInteractionResponse(s, i, edit)
}

// banner draws the guild's banner style for this command, or nil when there is none or drawing fails.
func (r *runner) banner() []byte {
	doc := LoadBanners(r.i.GuildID)
	if doc == nil {
		return nil
	}
	viewer := BannerViewer(r.invoker.ID, r.i.Member)
	b := doc.Choose("custom:"+r.name, viewer, "")
	if b == nil {
		return nil
	}
	img, err := RenderBanner(b, Vars(r.vars))
	if err != nil {
		logger.Warn(fmt.Sprintf("banner of /%s in %s: %v", r.name, r.i.GuildID, err))
		return nil
	}
	return img
}

// savedMessage is the Embed Builder message with the id, as a payload with the command's variables filled in.
func (r *runner) savedMessage(id string) map[string]any {
	cfg, err := database.Must().GuildConfig.Get(r.i.GuildID)
	if err != nil {
		return nil
	}
	var sections struct {
		Messages []MessageDoc `json:"messages"`
	}
	if cfg.Panels.IsEmpty() || cfg.Panels.Unmarshal(&sections) != nil {
		return nil
	}
	for idx := range sections.Messages {
		if sections.Messages[idx].ID == id {
			return sections.Messages[idx].Payload(VarFiller(r.vars))
		}
	}
	return nil
}

type option struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Choices     []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"choices"`
	Min *float64 `json:"min"`
	Max *float64 `json:"max"`
}

type condition struct {
	Left  string `json:"left"`
	Op    string `json:"op"`
	Right string `json:"right"`
}

type step struct {
	ID      string     `json:"id"`
	Type    string     `json:"type"`
	Target  string     `json:"target"`
	Limit   int        `json:"limit"`
	Message string     `json:"message"`
	Left    string     `json:"left"`
	Op      string     `json:"op"`
	Right   string     `json:"right"`
	MathOp  string     `json:"mathOp"`
	Name    string     `json:"name"`
	Value   string     `json:"value"`
	When    *condition `json:"when"`
	Reply   reply      `json:"reply"`
}

type replyButton = types.LinkButton

type reply struct {
	Type       string         `json:"type"`
	Text       string         `json:"text"`
	Content    string         `json:"content"`
	MessageID  string         `json:"messageId"`
	Ephemeral  bool           `json:"ephemeral"`
	Title      string         `json:"title"`
	Color      string         `json:"color"`
	Footer     string         `json:"footer"`
	Thumbnail  string         `json:"thumbnail"`
	Buttons    []replyButton  `json:"buttons"`
	Embeds     []types.Embed  `json:"embeds"`
	Components []types.V2Item `json:"components"`
}

type replyRule struct {
	When  condition `json:"when"`
	Reply reply     `json:"reply"`
}

// body is everything a command, or one subcommand of it, does when run.
type body struct {
	Options    []option    `json:"options"`
	Steps      []step      `json:"steps"`
	Reply      reply       `json:"reply"`
	ReplyRules []replyRule `json:"replyRules"`
}

type subcommand struct {
	body
	Name        string `json:"name"`
	Description string `json:"description"`
}

type builder struct {
	body
	Requirements []struct {
		Type     string `json:"type"`
		Key      string `json:"key"`
		Optional bool   `json:"optional"`
	} `json:"requirements"`
	Subcommands      []subcommand      `json:"subcommands"`
	SubcommandGroups []subcommandGroup `json:"subcommandGroups"`
}

type subcommandGroup struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Subcommands []subcommand `json:"subcommands"`
}

// bodyFor is the body a call runs: the named subcommand's, or the command's own.
func (b *builder) bodyFor(sub string) *body {
	if strings.Contains(sub, " ") {
		parts := strings.SplitN(sub, " ", 2)
		for groupIndex := range b.SubcommandGroups {
			group := &b.SubcommandGroups[groupIndex]
			if group.Name != parts[0] {
				continue
			}
			for commandIndex := range group.Subcommands {
				if group.Subcommands[commandIndex].Name == parts[1] {
					return &group.Subcommands[commandIndex].body
				}
			}
			return nil
		}
		return nil
	}
	if len(b.Subcommands) == 0 {
		if len(b.SubcommandGroups) == 0 {
			return &b.body
		}
		return nil
	}
	for idx := range b.Subcommands {
		if b.Subcommands[idx].Name == sub {
			return &b.Subcommands[idx].body
		}
	}
	return nil
}

// defaultReply is the one whose ephemerality Discord is told when the reply is deferred.
func (b *builder) defaultReply() reply {
	if len(b.SubcommandGroups) > 0 && len(b.SubcommandGroups[0].Subcommands) > 0 {
		return b.SubcommandGroups[0].Subcommands[0].Reply
	}
	if len(b.Subcommands) > 0 {
		return b.Subcommands[0].Reply
	}
	return b.Reply
}

func optionType(t string) (discordgo.ApplicationCommandOptionType, bool) {
	switch t {
	case "player", "user":
		return discordgo.ApplicationCommandOptionUser, true
	case "string":
		return discordgo.ApplicationCommandOptionString, true
	case "integer":
		return discordgo.ApplicationCommandOptionInteger, true
	case "number":
		return discordgo.ApplicationCommandOptionNumber, true
	case "boolean":
		return discordgo.ApplicationCommandOptionBoolean, true
	case "role":
		return discordgo.ApplicationCommandOptionRole, true
	case "channel":
		return discordgo.ApplicationCommandOptionChannel, true
	case "mentionable":
		return discordgo.ApplicationCommandOptionMentionable, true
	case "attachment":
		return discordgo.ApplicationCommandOptionAttachment, true
	}
	return 0, false
}

func describe(text, fallback string) string {
	if t := strings.TrimSpace(text); t != "" {
		if r := []rune(t); len(r) > 100 {
			return string(r[:100])
		}
		return t
	}
	return fallback
}

// slashOptions turns builder options into Discord's.
func slashOptions(opts []option) []*discordgo.ApplicationCommandOption {
	out := make([]*discordgo.ApplicationCommandOption, 0, len(opts))
	for _, o := range opts {
		t, ok := optionType(o.Type)
		if !ok || o.Name == "" {
			continue
		}
		d := &discordgo.ApplicationCommandOption{Name: o.Name, Description: describe(o.Description, o.Name), Type: t, Required: o.Required}
		for _, c := range o.Choices {
			ch := &discordgo.ApplicationCommandOptionChoice{Name: c.Name, Value: c.Value}
			switch t {
			case discordgo.ApplicationCommandOptionInteger:
				ch.Value = atoi(c.Value)
			case discordgo.ApplicationCommandOptionNumber:
				ch.Value = atof(c.Value)
			}
			d.Choices = append(d.Choices, ch)
		}
		switch t {
		case discordgo.ApplicationCommandOptionString:
			if o.Min != nil {
				m := int(*o.Min)
				d.MinLength = &m
			}
			if o.Max != nil {
				d.MaxLength = int(*o.Max)
			}
		case discordgo.ApplicationCommandOptionInteger, discordgo.ApplicationCommandOptionNumber:
			d.MinValue = o.Min
			if o.Max != nil {
				d.MaxValue = *o.Max
			}
		}
		out = append(out, d)
	}
	return out
}

// applicationOptions is the command's option list: its own, or one sub-command per subcommand.
func (b *builder) applicationOptions() []*discordgo.ApplicationCommandOption {
	if len(b.SubcommandGroups) > 0 {
		out := make([]*discordgo.ApplicationCommandOption, 0, len(b.SubcommandGroups))
		for _, group := range b.SubcommandGroups {
			groupOption := &discordgo.ApplicationCommandOption{
				Name: group.Name, Description: describe(group.Description, group.Name),
				Type: discordgo.ApplicationCommandOptionSubCommandGroup,
			}
			for _, sc := range group.Subcommands {
				groupOption.Options = append(groupOption.Options, &discordgo.ApplicationCommandOption{
					Name: sc.Name, Description: describe(sc.Description, sc.Name),
					Type: discordgo.ApplicationCommandOptionSubCommand, Options: slashOptions(sc.Options),
				})
			}
			out = append(out, groupOption)
		}
		return out
	}
	if len(b.Subcommands) == 0 {
		return slashOptions(b.Options)
	}
	out := make([]*discordgo.ApplicationCommandOption, 0, len(b.Subcommands))
	for _, sc := range b.Subcommands {
		out = append(out, &discordgo.ApplicationCommandOption{
			Name: sc.Name, Description: describe(sc.Description, sc.Name), Type: discordgo.ApplicationCommandOptionSubCommand, Options: slashOptions(sc.Options),
		})
	}
	return out
}

var logger = baseutility.CreateModuleLogger("CUSTOM_COMMANDS", "discord")

func atoi(s string) int     { n, _ := strconv.Atoi(s); return n }
func atof(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
func itoa(n int) string     { return strconv.Itoa(n) }
func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// cacheFor is how long a guild's commands are reused before the config is read again.
const cacheFor = 15 * time.Second

type entry struct {
	cfg types.CommandConfig
	b   builder
	cmd *types.Command
}

type guildCommands struct {
	at   time.Time
	byID map[string]*entry
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*guildCommands{}
)

// entries returns the guild's custom commands by name. When the config cannot be read the last known set is used.
func entries(guildID string) map[string]*entry {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if c := cache[guildID]; c != nil && time.Since(c.at) < cacheFor {
		return c.byID
	}
	cfg, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		if c := cache[guildID]; c != nil {
			return c.byID
		}
		return nil
	}
	raw := map[string]types.CommandConfig{}
	if !cfg.Commands.IsEmpty() {
		_ = cfg.Commands.Unmarshal(&raw)
	}
	out := map[string]*entry{}
	for name, c := range raw {
		if len(c.Builder) == 0 {
			continue
		}
		var b builder
		if err := json.Unmarshal(c.Builder, &b); err != nil {
			logger.Warn(fmt.Sprintf("custom command %q in %s: %v", name, guildID, err))
			continue
		}
		if c.Name == "" {
			c.Name = name
		}
		out[name] = &entry{cfg: c, b: b, cmd: build(name, c, &b)}
	}
	cache[guildID] = &guildCommands{at: time.Now(), byID: out}
	return out
}

// FindCustomCommand returns the guild's custom command called name, or nil.
func FindCustomCommand(guildID, name string) *types.Command {
	if e := entries(guildID)[name]; e != nil && FindCommand(name) == nil {
		return e.cmd
	}
	return nil
}

// CustomCommandsFor returns every custom command of the guild by name.
func CustomCommandsFor(guildID string) map[string]*types.Command {
	out := map[string]*types.Command{}
	for name, e := range entries(guildID) {
		if FindCommand(name) == nil {
			out[name] = e.cmd
		}
	}
	return out
}

func build(name string, c types.CommandConfig, b *builder) *types.Command {
	cmd := &types.Command{
		Name:        name,
		Description: describe(c.Description, "Custom command"),
		Category:    "custom",
		Ephemeral:   b.defaultReply().Ephemeral,
		Data:        &discordgo.ApplicationCommand{Name: name, Description: describe(c.Description, "Custom command"), Options: b.applicationOptions(), DefaultMemberPermissions: c.DefaultMemberPermissions},
	}
	for _, r := range b.Requirements {
		cmd.Requirements = append(cmd.Requirements, types.Requirement{Type: types.RequirementType(r.Type), Key: r.Key, Optional: r.Optional})
	}
	cmd.Execute = func(s *discordgo.Session, i *discordgo.InteractionCreate) error { return run(s, i, name) }
	return cmd
}

func customCommandPath(data discordgo.ApplicationCommandInteractionData, b *builder) (string, []*discordgo.ApplicationCommandInteractionDataOption) {
	options := data.Options
	if len(options) == 0 {
		return "", nil
	}
	if len(b.SubcommandGroups) > 0 && options[0].Type == discordgo.ApplicationCommandOptionSubCommandGroup {
		if len(options[0].Options) == 0 {
			return options[0].Name, nil
		}
		command := options[0].Options[0]
		return options[0].Name + " " + command.Name, command.Options
	}
	if len(b.Subcommands) > 0 && options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		return options[0].Name, options[0].Options
	}
	return "", options
}

// ============================================================================
// Registering with Discord
// ============================================================================

// syncEvery is how often changes made on the dashboard reach Discord. Nothing is sent when a guild's list is unchanged
// (see the hash in syncGuild), so a short period only costs a config read per guild.
const syncEvery = 30 * time.Second

var (
	syncOnce sync.Once
	syncMu   sync.Mutex
	synced   = map[string][32]byte{} // guildID -> hash of the command list last sent
)

// StartCustomCommandSync registers every guild's custom commands with Discord now and then keeps them current. base is the
// built-in command list: with a development guild those are registered there too, and a guild-level overwrite
// there must not drop them.
func StartCustomCommandSync(s *discordgo.Session, appID, devGuildID string, base []*discordgo.ApplicationCommand) {
	run := func() {
		for _, g := range s.State.Guilds {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error(fmt.Sprintf("custom command sync panic in %s: %v", g.ID, r))
					}
				}()
				syncGuild(s, appID, g.ID, devGuildID, base)
			}()
		}
	}
	go run()
	syncOnce.Do(func() {
		go func() {
			for range time.Tick(syncEvery) {
				run()
			}
		}()
	})
}

func syncGuild(s *discordgo.Session, appID, guildID, devGuildID string, base []*discordgo.ApplicationCommand) {
	var desired []*discordgo.ApplicationCommand
	customs := 0
	for name, e := range entries(guildID) {
		if FindCommand(name) != nil || !e.cfg.IsEnabled || !e.cfg.IsSlashEnabled || e.cmd.Data == nil {
			continue
		}
		desired = append(desired, e.cmd.Data)
		customs++
	}
	if guildID == devGuildID {
		desired = append(append([]*discordgo.ApplicationCommand{}, base...), desired...)
	}
	raw, _ := json.Marshal(desired)
	hash := sha256.Sum256(raw)

	syncMu.Lock()
	last, seen := synced[guildID]
	syncMu.Unlock()
	if seen && last == hash {
		return
	}
	if !seen && customs == 0 && guildID != devGuildID {
		// Nothing to register; only clear what an earlier run left behind.
		if existing, err := s.ApplicationCommands(appID, guildID); err != nil || len(existing) == 0 {
			if err == nil {
				syncMu.Lock()
				synced[guildID] = hash
				syncMu.Unlock()
			}
			return
		}
	}
	if desired == nil {
		desired = []*discordgo.ApplicationCommand{}
	}
	if _, err := s.ApplicationCommandBulkOverwrite(appID, guildID, desired); err != nil {
		logger.Warn(fmt.Sprintf("registering custom commands in %s: %v", guildID, err))
		return
	}
	syncMu.Lock()
	synced[guildID] = hash
	syncMu.Unlock()
}

// CommandRegistry holds every registered command.
var CommandRegistry []*types.Command

// EventRegistry holds every registered event.
var EventRegistry []*types.Event

// Register adds a command to the registry (alias for RegisterCommand matching init blocks).
func Register(cmd *types.Command) {
	RegisterCommand(cmd)
}

// RegisterCommand adds a command to the registry, sets default categories, and prepares Discord Data.
func RegisterCommand(cmd *types.Command) {
	if cmd == nil {
		return
	}

	// Prevent duplicate command registrations
	for _, existing := range CommandRegistry {
		if existing.Name == cmd.Name {
			log.Printf("[Warning] Duplicate command registration detected: '%s'", cmd.Name)
			return
		}
	}

	// Automatically infer category from package directory name if not set
	if cmd.Category == "" {
		if _, file, _, ok := runtime.Caller(2); ok {
			cmd.Category = filepath.Base(filepath.Dir(file))
		}
	}

	prepareCommandData(cmd)
	CommandRegistry = append(CommandRegistry, cmd)
}

// RegisterEvent adds an event to the registry with duplicate protection.
func RegisterEvent(event *types.Event) {
	if event == nil {
		return
	}

	for _, existing := range EventRegistry {
		if existing.Name == event.Name {
			log.Printf("[Warning] Duplicate event registration detected: '%s'", event.Name)
			return
		}
	}

	EventRegistry = append(EventRegistry, event)
}

// prepareCommandData synchronizes types.Command fields with the underlying Data block.
func prepareCommandData(cmd *types.Command) {
	if cmd.Data == nil {
		cmd.Data = &discordgo.ApplicationCommand{}
	}

	// Ensure Name and Description are synchronized on Data if omitted
	if cmd.Data.Name == "" {
		cmd.Data.Name = cmd.Name
	}
	if cmd.Data.Description == "" {
		cmd.Data.Description = cmd.Description
	}

	// Apply native permissions overrides if present
	if cmd.DefaultMemberPermissions != nil {
		cmd.Data.DefaultMemberPermissions = cmd.DefaultMemberPermissions
	}
	if cmd.DMPermission != nil {
		cmd.Data.DMPermission = cmd.DMPermission
	}

	// Auto-wire subcommands into Data.Options if declared
	if len(cmd.Subcommands) > 0 && len(cmd.Data.Options) == 0 {
		options := make([]*discordgo.ApplicationCommandOption, len(cmd.Subcommands))
		for idx, sub := range cmd.Subcommands {
			options[idx] = &discordgo.ApplicationCommandOption{
				Name:        sub.Name,
				Description: sub.Description,
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options:     buildSlashOptions(sub.Options),
			}
		}
		cmd.Data.Options = options

		// Default fallback router for subcommands if no custom Execute function is declared
		if cmd.Execute == nil {
			subs := cmd.Subcommands
			cmd.Execute = func(s *discordgo.Session, i *discordgo.InteractionCreate) error {
				name, _ := types.SelectedSubcommand(i)
				for _, sub := range subs {
					if sub.Name == name && sub.Run != nil {
						return sub.Run(s, i)
					}
				}
				return nil
			}
		}
	}
}

// buildSlashOptions translates custom SlashOptions to Discord API structures.
func buildSlashOptions(opts []types.SlashOption) []*discordgo.ApplicationCommandOption {
	out := make([]*discordgo.ApplicationCommandOption, len(opts))
	for idx, o := range opts {
		out[idx] = &discordgo.ApplicationCommandOption{
			Name:         o.Name,
			Description:  o.Description,
			Type:         o.Type,
			Required:     o.Required,
			Choices:      o.Choices,
			Autocomplete: o.Autocomplete,
		}
	}
	return out
}

// FindCommand looks up a registered command by name.
func FindCommand(name string) *types.Command {
	for _, c := range CommandRegistry {
		if c != nil && (c.Name == name || (c.Data != nil && c.Data.Name == name)) {
			return c
		}
	}
	return nil
}

func HandlePrefixCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Message == nil || m.Author == nil || m.GuildID == "" || m.Content == "" {
		return
	}
	// Allow other bots to invoke prefix commands, but never react to our own
	// messages (prevents self-triggered command loops).
	if s.State != nil && s.State.User != nil && m.Author.ID == s.State.User.ID {
		return
	}
	if m.WebhookID != "" {
		return
	}
	if !isLikelyPrefixCharSequence(m.Content) {
		return
	}

	// One guild+config fetch for the whole invocation: settings drives prefix
	// resolution and access checks here, and gc is passed straight into the
	// pipeline below instead of it loading the same data again.
	gc := LoadGuildContext(m.GuildID)
	settings := gc.Settings

	prefix := settings.Prefix
	if strings.TrimSpace(prefix) == "" {
		prefix = DefaultCommandPrefix
	}
	if !settings.PrefixEnabled || !strings.HasPrefix(m.Content, prefix) {
		return
	}

	fullContent := strings.TrimSpace(m.Content[len(prefix):])
	if fullContent == "" {
		return
	}

	tokens := tokenizePrefixArguments(fullContent)
	if len(tokens) == 0 {
		return
	}
	trigger := strings.ToLower(tokens[0])
	args := tokens[1:]

	// Build command map from registry
	cmdMap := make(map[string]*types.Command, len(CommandRegistry))
	for _, cmd := range CommandRegistry {
		if cmd != nil && cmd.Data != nil {
			cmdMap[cmd.Data.Name] = cmd
		}
	}
	for name, cmd := range CustomCommandsFor(m.GuildID) {
		if cmd.Data != nil && cmdMap[name] == nil {
			cmdMap[name] = cmd
		}
	}

	route, cmd, errMsg := ResolvePrefixRoute(cmdMap, settings, trigger, args)
	if cmd == nil {
		return
	}
	if cmd.Disabled {
		replyPrefixEmbed(s, m, AccessDeniedEmbed(
			"Command Disabled",
			"This command is currently disabled globally and cannot be used.",
		))
		return
	}
	if errMsg != "" {
		replyPrefixEmbed(s, m, SimpleErrorEmbed(errMsg))
		return
	}
	if route == nil {
		return
	}

	interaction := BuildPrefixInteraction(s, m.Message, cmd, route)
	isAdmin := MemberIsAdministrator(s, interaction)
	if decision := CheckPrefixAccess(settings, cmd.Name, route.SubcommandPath, m.ChannelID, interaction.Member, isAdmin); !decision.Allowed {
		replyPrefixEmbed(s, m, AccessDeniedEmbed(decision.Title, decision.Message))
		return
	}

	_ = RunCommandPipeline(s, interaction, cmd, gc, PipelineOptions{
		SkipGuildRegistration: cmd.SkipGuildCheck,
	})
}

func isLikelyPrefixCharSequence(content string) bool {
	if content == "" {
		return false
	}
	ch := content[0]
	if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == ' ' || ch == '\t' {
		return false
	}
	return true
}

// tokenizePrefixArguments splits on spaces while preserving double-quoted groups and handling backslash escapes.
func tokenizePrefixArguments(input string) []string {
	var tokens []string
	var current strings.Builder
	inQuotes := false
	escaped := false

	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}

	for i := 0; i < len(input); i++ {
		ch := input[i]

		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}

		switch ch {
		case '\\':
			escaped = true
		case '"':
			inQuotes = !inQuotes
		case ' ', '\t':
			if inQuotes {
				current.WriteByte(ch)
			} else {
				flush()
			}
		default:
			current.WriteByte(ch)
		}
	}
	flush()
	return tokens
}

func replyPrefixEmbed(s *discordgo.Session, m *discordgo.MessageCreate, emb *discordgo.MessageEmbed) {
	if s == nil || m == nil || emb == nil {
		return
	}
	_, _ = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{emb},
		Reference: &discordgo.MessageReference{
			MessageID: m.ID,
			ChannelID: m.ChannelID,
			GuildID:   m.GuildID,
		},
	})
}

// requirementFixPrefix identifies a requirement-fix select menu's custom ID:
// reqfix1|<phase>|<cmdName>|<type>:<key>|<type>:<key>|... — phase is "req" or
// "opt" (see phaseRequired/phaseOptional) and says what happens once the list
// is fully resolved; the first type:key pair is what the menu itself resolves,
// the rest are what's still left after it.
const requirementFixPrefix = "reqfix1|"

// requirementPromptPrefix identifies the Continue/Skip buttons offered once
// every required slot is configured but optional ones are still available:
// reqfixp|<action>|<cmdName>|<type>:<key>|... — action is "continue" or "skip".
const requirementPromptPrefix = "reqfixp|"

const (
	phaseRequired = "req"
	phaseOptional = "opt"
)

func requirementFixMinValues() *int { n := 1; return &n }

// BuildRequirementFixComponents builds a select menu that lets an administrator
// resolve cmdName's first missing REQUIRED requirement directly from the
// configuration error message. Picking a value saves its snowflake ID onto
// that requirement's slot in the command's own config (see
// SetRequirementSlotID) and advances the same message to the next missing
// requirement until none remain — at which point, if cmdName also has
// unconfigured optional requirements, a Continue/Skip prompt offers to
// configure those too (see handleRequirementSelectPick).
func BuildRequirementFixComponents(cmdName string, missing []types.Requirement) []discordgo.MessageComponent {
	return buildRequirementSelectComponents(phaseRequired, cmdName, missing)
}

func buildRequirementSelectComponents(phase, cmdName string, items []types.Requirement) []discordgo.MessageComponent {
	if len(items) == 0 {
		return nil
	}
	next := items[0]
	if !supportsInlineRequirementFix(next.Type) {
		return nil // no assignment UI for this requirement type (e.g. ReqThread)
	}

	menu := &discordgo.SelectMenu{
		CustomID:    encodeRequirementFixCustomID(phase, cmdName, items),
		Placeholder: fmt.Sprintf("Select the %s for %q", next.Type, next.Key),
		MinValues:   requirementFixMinValues(),
		MaxValues:   1,
	}
	switch next.Type {
	case types.ReqRole:
		menu.MenuType = discordgo.RoleSelectMenu
	case types.ReqChannel:
		menu.MenuType = discordgo.ChannelSelectMenu
		menu.ChannelTypes = []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews}
	case types.ReqCategory:
		menu.MenuType = discordgo.ChannelSelectMenu
		menu.ChannelTypes = []discordgo.ChannelType{discordgo.ChannelTypeGuildCategory}
	}
	return []discordgo.MessageComponent{&discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}}}
}

// buildOptionalPromptComponents builds the Continue/Skip buttons offered once
// every required requirement is configured, when cmdName also has unconfigured
// optional ones available (see optionalAvailableEmbed for the paired embed).
func buildOptionalPromptComponents(cmdName string, optional []types.Requirement) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.Button{Label: "Continue", Style: discordgo.SuccessButton, CustomID: encodePromptCustomID("continue", cmdName, optional)},
		&discordgo.Button{Label: "Skip", Style: discordgo.SecondaryButton, CustomID: encodePromptCustomID("skip", cmdName, nil)},
	}}}
}

// optionalAvailableEmbed tells an administrator every required slot is now
// configured and lists cmdName's remaining optional ones, pairing with
// buildOptionalPromptComponents' Continue/Skip buttons.
func optionalAvailableEmbed(cmdName string, optional []types.Requirement) *discordgo.MessageEmbed {
	description := fmt.Sprintf("All required resources for `%s` are now configured, and it's ready to use.\n\n`%s` also has these optional resources available:\n", cmdName, cmdName)
	for _, req := range optional {
		description += fmt.Sprintf("- %s: %s\n", req.Type, req.Key)
	}
	description += "\nWould you like to configure them now, or skip for now? You're able to configure these at any time using `/server_config`."
	return Preset("caution", types.PresetOptions{
		Title:       "Optional Configuration Available",
		Description: description,
	})
}

// HandleRequirementFixSelect responds to a pick from BuildRequirementFixComponents
// or a click on buildOptionalPromptComponents' Continue/Skip buttons. It
// reports false when customID isn't one of these, so the caller can fall
// through to other component handling.
func HandleRequirementFixSelect(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	switch {
	case strings.HasPrefix(customID, requirementFixPrefix):
		return handleRequirementSelectPick(s, i, customID)
	case strings.HasPrefix(customID, requirementPromptPrefix):
		return handleOptionalPrompt(s, i, customID)
	default:
		return false
	}
}

func handleRequirementSelectPick(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	phase, cmdName, remaining, ok := decodeRequirementFixCustomID(customID)
	if !ok || len(remaining) == 0 {
		return false
	}

	if !MemberIsAdministrator(s, i) {
		updateRequirementFixMessage(s, i, AccessDeniedEmbed("Permission Denied", "Only administrators can configure this."), nil)
		return true
	}

	current := remaining[0]
	if !supportsInlineRequirementFix(current.Type) {
		return false
	}

	data := i.MessageComponentData()
	if len(data.Values) == 0 {
		return false
	}
	pickedID := data.Values[0]

	switch current.Type {
	case types.ReqRole:
		if data.Resolved.Roles[pickedID] == nil {
			return false
		}
	default: // ReqChannel, ReqCategory
		if data.Resolved.Channels[pickedID] == nil {
			return false
		}
	}

	if err := SetRequirementSlotID(InteractionUserID(i), i.GuildID, cmdName, current.Type, current.Key, pickedID); err != nil {
		updateRequirementFixMessage(s, i, ConfigurationErrorEmbed("Could not save that assignment. Please try again or use `/server_config`."), nil)
		return true
	}

	stillRemaining := remaining[1:]
	if len(stillRemaining) > 0 {
		updateRequirementFixMessage(s, i,
			ConfigurationErrorEmbed(requirementListDescription(cmdName, phase, stillRemaining)),
			buildRequirementSelectComponents(phase, cmdName, stillRemaining),
		)
		return true
	}

	// This phase's items are all resolved. After the required phase, offer to
	// configure any optional ones too before declaring the command ready.
	if phase == phaseRequired {
		if optional := LoadOptionalCommandRequirements(LoadGuildContext(i.GuildID).Settings, cmdName); len(optional) > 0 {
			updateRequirementFixMessage(s, i, optionalAvailableEmbed(cmdName, optional), buildOptionalPromptComponents(cmdName, optional))
			return true
		}
	}

	updateRequirementFixMessage(s, i, configurationCompleteEmbed(cmdName), nil)
	return true
}

func handleOptionalPrompt(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	action, cmdName, optional, ok := decodePromptCustomID(customID)
	if !ok {
		return false
	}

	if !MemberIsAdministrator(s, i) {
		updateRequirementFixMessage(s, i, AccessDeniedEmbed("Permission Denied", "Only administrators can configure this."), nil)
		return true
	}

	if action != "continue" || len(optional) == 0 {
		updateRequirementFixMessage(s, i, configurationCompleteEmbed(cmdName), nil)
		return true
	}

	updateRequirementFixMessage(s, i,
		ConfigurationErrorEmbed(requirementListDescription(cmdName, phaseOptional, optional)),
		buildRequirementSelectComponents(phaseOptional, cmdName, optional),
	)
	return true
}

func requirementListDescription(cmdName, phase string, items []types.Requirement) string {
	if phase == phaseOptional {
		return FormatConfigurationErrorAdmin(cmdName, nil, items)
	}
	return FormatConfigurationErrorAdmin(cmdName, items, nil)
}

func configurationCompleteEmbed(cmdName string) *discordgo.MessageEmbed {
	return Preset("success", types.PresetOptions{
		Title:       "Configuration Complete",
		Description: fmt.Sprintf("`%s` is ready to use. Please re-run the command. You're able to configure it further at any time using `/server_config`.", cmdName),
	})
}

func updateRequirementFixMessage(s *discordgo.Session, i *discordgo.InteractionCreate, emb *discordgo.MessageEmbed, components []discordgo.MessageComponent) {
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: components,
		},
	})
}

func encodeRequirementFixCustomID(phase, cmdName string, remaining []types.Requirement) string {
	parts := make([]string, 0, 3+len(remaining))
	parts = append(parts, "reqfix1", phase, cmdName)
	for _, r := range remaining {
		parts = append(parts, string(r.Type)+":"+r.Key)
	}
	return strings.Join(parts, "|")
}

func decodeRequirementFixCustomID(customID string) (phase, cmdName string, remaining []types.Requirement, ok bool) {
	if !strings.HasPrefix(customID, requirementFixPrefix) {
		return "", "", nil, false
	}
	parts := strings.Split(customID, "|")
	if len(parts) < 4 {
		return "", "", nil, false
	}
	phase = parts[1]
	cmdName = parts[2]
	for _, p := range parts[3:] {
		typ, key, found := strings.Cut(p, ":")
		if !found {
			continue
		}
		remaining = append(remaining, types.Requirement{Type: types.RequirementType(typ), Key: key})
	}
	if len(remaining) == 0 {
		return "", "", nil, false
	}
	return phase, cmdName, remaining, true
}

func encodePromptCustomID(action, cmdName string, optional []types.Requirement) string {
	parts := make([]string, 0, 3+len(optional))
	parts = append(parts, "reqfixp", action, cmdName)
	for _, r := range optional {
		parts = append(parts, string(r.Type)+":"+r.Key)
	}
	return strings.Join(parts, "|")
}

func decodePromptCustomID(customID string) (action, cmdName string, optional []types.Requirement, ok bool) {
	if !strings.HasPrefix(customID, requirementPromptPrefix) {
		return "", "", nil, false
	}
	parts := strings.Split(customID, "|")
	if len(parts) < 3 {
		return "", "", nil, false
	}
	action = parts[1]
	cmdName = parts[2]
	for _, p := range parts[3:] {
		typ, key, found := strings.Cut(p, ":")
		if !found {
			continue
		}
		optional = append(optional, types.Requirement{Type: types.RequirementType(typ), Key: key})
	}
	return action, cmdName, optional, true
}

// supportsInlineRequirementFix reports whether reqType has a Discord select
// menu it can resolve to (role/channel/category); ReqThread and any future
// type without one falls through to a manual /config fix instead.
func supportsInlineRequirementFix(reqType types.RequirementType) bool {
	switch reqType {
	case types.ReqRole, types.ReqChannel, types.ReqCategory:
		return true
	default:
		return false
	}
}

// roleRequirementDenial checks the member against the role requirements the command declares. Staff commands need
// the staff role (and, when the guild set allowed roles for the command, one of those too); general commands need the
// registered role. It returns the embed to answer with when the member is turned away, or nil when they may run the
// command. Roles that are handed out by a command (muted, banned) are only configuration and are never checked here.
func roleRequirementDenial(s *discordgo.Session, i *discordgo.InteractionCreate, cmd *types.Command, settings types.GuildSettings) *discordgo.MessageEmbed {
	if i == nil || i.Member == nil || i.GuildID == "" {
		return nil
	}

	gate := roleGateFor(settings, cmd.Name)
	if gate.staff {
		perm := settings.Commands[cmd.Name]
		if decision := checkRoleAccess(perm.AllowedRoles, perm.DeniedRoles, i.Member, MemberIsAdministrator(s, i), gate); !decision.Allowed {
			return Preset("error", types.PresetOptions{Title: decision.Title, Description: decision.Message})
		}
	}

	if types.RequiresRole(cmd.Requirements, types.RegisteredRoleKey) {
		id := settings.Commands[cmd.Name].RequiredRoles[types.RegisteredRoleKey].ID
		if id == "" {
			id = RegisteredRoleID(i.GuildID)
		}
		if id != "" && !MemberHasRole(i.Member, id) {
			return Preset("error", types.PresetOptions{
				Title:       "Registration Required",
				Description: "You must be registered to use this command. Run `/player_register` to link your Minecraft account first, or `/player_refresh` if you are already registered.",
			})
		}
	}
	return nil
}

// roleGate is what a command's own declaration adds to the role checks: whether it is a staff command (and which role
// that means) and whether it is open to everyone until the guild narrows it down.
type roleGate struct {
	staff         bool
	staffID       string
	openByDefault bool
}

// roleGateFor reads the gate of a registered command from its requirements and the guild's role assignments.
func roleGateFor(settings types.GuildSettings, commandName string) roleGate {
	cmd := FindCommand(commandName)
	if cmd == nil {
		// A command made on the dashboard is open to everyone until allowed roles are set, unless it declares the staff role.
		perm := settings.Commands[commandName]
		if !perm.Custom {
			return roleGate{}
		}
		gate := roleGate{openByDefault: true}
		if perm.StaffOnly {
			gate.staff = true
			gate.staffID = perm.RequiredRoles[types.StaffRoleKey].ID
		}
		return gate
	}
	gate := roleGate{openByDefault: cmd.Category == "general"}
	if types.HasRequirementKey(cmd.Requirements, types.ReqRole, types.StaffRoleKey) {
		gate.staff = true
		gate.staffID = settings.Commands[commandName].RequiredRoles[types.StaffRoleKey].ID
	}
	return gate
}
