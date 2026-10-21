package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
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

	if denied := planDenial(gc, i.GuildID, cmd, time.Now()); denied != nil && !opts.SkipGuildRegistration {
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

// logModerationUsage posts the command, its options and the moderator to the optional staff_command_logs channel.
func logModerationUsage(s *discordgo.Session, i *discordgo.InteractionCreate, cmd *types.Command, userID string) {
	channelID, err := RequirementSlotID(i.GuildID, cmd.Name, types.ReqChannel, types.StaffCommandLogsKey)
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

// A guild's own custom commands live in package features, which imports helpers, so helpers reaches them through these
// hooks; features sets them when it loads.
var (
	// FindCustomCommand returns the guild's custom command called name, or nil.
	FindCustomCommand = func(guildID, name string) *types.Command { return nil }
	// CustomCommandsFor returns every custom command of the guild by name.
	CustomCommandsFor = func(guildID string) map[string]*types.Command { return nil }
	// CustomCommandEphemeral says whether the called custom command replies privately; ok is false for any other command.
	CustomCommandEphemeral = func(i *discordgo.InteractionCreate, cmd *types.Command) (ephemeral, ok bool) { return false, false }
)

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
	if e, ok := CustomCommandEphemeral(i, cmd); ok {
		ephemeral = e
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
	if planBlocksComponent(s, i) {
		return true
	}
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
		if key == types.StaffCommandLogsKey || slot.ID == "" || sent[slot.ID] {
			continue
		}
		sent[slot.ID] = true
		_, _ = s.ChannelMessageSendEmbed(slot.ID, &logged)
	}
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
	// Saving goes through several database calls; acknowledge now so Discord's 3-second window never runs out.
	ackRequirementFix(s, i)

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
	ackRequirementFix(s, i)

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

// ackRequirementFix acknowledges a component click without changing the message (updateRequirementFixMessage edits it later).
func ackRequirementFix(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
}

func updateRequirementFixMessage(s *discordgo.Session, i *discordgo.InteractionCreate, emb *discordgo.MessageEmbed, components []discordgo.MessageComponent) {
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	embeds := []*discordgo.MessageEmbed{emb}
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embeds, Components: &components})
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
	// Every command in the staff category needs the staff role, whether or not it still declares the requirement.
	if cmd.Category == "staff" || types.HasRequirementKey(cmd.Requirements, types.ReqRole, types.StaffRoleKey) {
		gate.staff = true
		gate.staffID = settings.Commands[commandName].RequiredRoles[types.StaffRoleKey].ID
	}
	return gate
}
