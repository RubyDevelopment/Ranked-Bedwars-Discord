package helpers

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	baseutility "ranked-bedwars/src/utility"
)

// LFGConfig is defined in package types (guilds.go).
type LFGConfig = types.LFGConfig

// LoadLFGConfig returns the guild's LFG settings. A missing or empty config
// resolves to default zero values.
func LoadLFGConfig(guildID string) (*LFGConfig, error) {
	lfg := &LFGConfig{}
	raw, err := database.Must().GuildConfig.Field(guildID, "lfg")
	if sdk.IsNotFound(err) {
		return lfg, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, lfg); err != nil {
			return nil, err
		}
	}
	return lfg, nil
}

// SaveLFGConfig persists LFG settings into the guild's `lfg` config section.
func SaveLFGConfig(actorID, guildID string, lfg *LFGConfig) error {
	if lfg == nil {
		return fmt.Errorf("lfg config is nil")
	}
	_, err := database.Must().GuildConfig.SaveField(actorID, guildID, "lfg", lfg)
	return err
}

// decodeGameConfigField reads one field of the guild's game config (maps, modes,
// ranks, bots, ...) and decodes it into T, treating a missing config as empty
// rather than an error (a guild is provisioned with one the moment it registers,
// so this only happens for a guild that no longer exists).
func decodeGameConfigField[T any](guildID, field string) (T, error) {
	var out T
	raw, err := database.Must().GameConfig.Field(guildID, field)
	if err != nil {
		if sdk.IsNotFound(err) {
			return out, nil
		}
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

// GuildMetaMaps loads map configuration for a guild, treating missing config as empty.
func GuildMetaMaps(guildID string) ([]types.Map, error) {
	return decodeGameConfigField[[]types.Map](guildID, "maps")
}

// GuildMetaModes loads mode configuration for a guild, treating missing config as empty.
func GuildMetaModes(guildID string) ([]types.Mode, error) {
	return decodeGameConfigField[[]types.Mode](guildID, "modes")
}

// GuildMetaRanks loads rank configuration for a guild, treating missing config as empty.
func GuildMetaRanks(guildID string) ([]types.Rank, error) {
	return decodeGameConfigField[[]types.Rank](guildID, "ranks")
}

// GuildMetaBots loads bot configuration for a guild, treating missing config as empty.
func GuildMetaBots(guildID string) ([]types.Bot, error) {
	return decodeGameConfigField[[]types.Bot](guildID, "bots")
}

// GuildStateRoles loads the guild's role-key map (e.g. "guild_role_registered" ->
// role), treating a missing state as empty.
func GuildStateRoles(guildID string) (map[string]types.DiscordRole, error) {
	var out map[string]types.DiscordRole
	raw, err := database.Must().GuildState.Field(guildID, "roles")
	if err != nil {
		if sdk.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

// ActiveModeNames lists enabled game mode names for a guild.
func ActiveModeNames(modes []types.Mode) []string {
	names := make([]string, 0, len(modes))
	for _, mode := range modes {
		if mode.IsEnabled && strings.TrimSpace(mode.Name) != "" {
			names = append(names, mode.Name)
		}
	}
	return names
}

// ============================================================================
// Guild Setup & Lifecycle
// ============================================================================

// EnsureGuildSetup registers the guild (with its config, state and game config
// scaffolds) and rebuilds the Discord role/channel snapshot. It is idempotent
// and self-healing: calling it again on an already-registered guild is a no-op
// that also backfills any of those rows that went missing.
func EnsureGuildSetup(s *discordgo.Session, guildID string) (*types.SetupResult, error) {
	result := &types.SetupResult{}

	if database.Get() == nil {
		return nil, fmt.Errorf("database service not initialized")
	}

	guild, err := s.Guild(guildID)
	if err != nil || guild == nil {
		return nil, fmt.Errorf("could not load guild %s: %w", guildID, err)
	}

	// Create also restores a soft-deleted guild (and its config/state/game config)
	// with the same snowflake, and backfills any of those rows a live guild is
	// somehow missing — so this is safe and sufficient to call every time.
	_, restored, err := database.Must().Guild.Create(sdk.SystemActor, guildID, guild.Name, "")
	if err != nil {
		return nil, fmt.Errorf("ensure guild record: %w", err)
	}
	fresh := !restored
	result.GuildCreated = fresh
	result.ConfigCreated = fresh
	result.SnapshotCreated = fresh
	result.MetaCreated = fresh
	result.GameConfigCreated = fresh

	if err := SyncGuildSnapshot(s, guildID); err == nil {
		result.SnapshotSynced = true
	}

	if err := ensureGuildInvite(s, guildID); err != nil {
		baseutility.CreateModuleLogger("GUILD_SETUP", "Invite").Error(fmt.Sprintf("Guild invite for %s: %v", guildID, err))
	}

	if err := SeedCommandConfigs(guildID); err != nil {
		return nil, fmt.Errorf("seed command configs: %w", err)
	}
	if err := SeedEventConfigs(guildID); err != nil {
		return nil, fmt.Errorf("seed event configs: %w", err)
	}

	return result, nil
}

// ensureGuildInvite keeps one permanent, unlimited invite per guild in guild_configs.invite_code:
// the stored one is reused while Discord still has it and it can still be used; otherwise a new one
// is created in the first text channel the bot may invite from, and stored.
func ensureGuildInvite(s *discordgo.Session, guildID string) error {
	config, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		return err
	}

	if config.InviteCode != "" {
		inv, err := s.Invite(config.InviteCode)
		usable := err == nil && inv.ExpiresAt == nil && (inv.MaxUses == 0 || inv.Uses < inv.MaxUses)
		if usable {
			return nil
		}
	}

	channels, err := s.GuildChannels(guildID)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		if ch.Type != discordgo.ChannelTypeGuildText {
			continue
		}
		if perms, err := s.UserChannelPermissions(s.State.User.ID, ch.ID); err != nil || perms&discordgo.PermissionCreateInstantInvite == 0 {
			continue
		}
		inv, err := s.ChannelInviteCreate(ch.ID, discordgo.Invite{MaxAge: 0, MaxUses: 0, Unique: true})
		if err != nil {
			return err
		}
		_, err = database.Must().GuildConfig.SaveField(sdk.SystemActor, guildID, "invite_code", inv.Code)
		return err
	}
	return fmt.Errorf("no channel the bot can create an invite in")
}

// ============================================================================
// Command Configuration Seeding
// ============================================================================

// SeedCommandConfigs brings the guild's commands map in line with the registered commands: every registered
// command that has no entry gets a default one, the category of existing entries follows the command's package, an
// entry of a renamed command moves to its new name (keeping the admin's settings), and an entry of a command that
// no longer exists is removed. Custom commands (made on the dashboard, they carry a builder) are never removed.
func SeedCommandConfigs(guildID string) error {
	config, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		return err
	}

	existing := decodeCommandsMap(config.Commands)
	if !reconcileCommandConfigs(existing, CommandRegistry) {
		return nil
	}
	_, err = database.Must().GuildConfig.SaveField(sdk.SystemActor, guildID, "commands", existing)
	return err
}

// renamedCommands maps the old name of a command to its current one, so an admin's settings survive the rename.
// Add a line here whenever a command is renamed.
var renamedCommands = map[string]string{"claimelo": "claim", "voidmatch": "void"}

// reconcileCommandConfigs edits `existing` to match the registry and reports whether it changed anything.
func reconcileCommandConfigs(existing map[string]types.CommandConfig, registry []*types.Command) bool {
	changed := false

	live := map[string]bool{}
	for _, cmd := range registry {
		if cmd != nil && cmd.Name != "" {
			live[cmd.Name] = true
		}
	}

	for old, current := range renamedCommands {
		cfg, ok := existing[old]
		if !ok || len(cfg.Builder) > 0 || !live[current] {
			continue
		}
		if _, taken := existing[current]; !taken {
			cfg.Name = current
			existing[current] = cfg
		}
		delete(existing, old)
		changed = true
	}

	for _, cmd := range registry {
		if cmd == nil || cmd.Data == nil || cmd.Name == "" {
			continue
		}
		cfg, ok := existing[cmd.Name]
		if !ok {
			existing[cmd.Name] = defaultCommandConfig(cmd)
			changed = true
			continue
		}
		updated := reconcileRequirements(&cfg, cmd.Requirements)
		if cmd.Category != "" && cfg.Category != cmd.Category {
			cfg.Category = cmd.Category
			updated = true
		}
		if cfg.Name != cmd.Name {
			cfg.Name = cmd.Name
			updated = true
		}
		if cmd.Description != "" && cfg.Description != cmd.Description {
			cfg.Description = cmd.Description
			updated = true
		}
		if want := responsesOf(cmd.Name); !slices.Equal(cfg.Responses, want) {
			cfg.Responses = want
			updated = true
		}
		if updated {
			existing[cmd.Name] = cfg
			changed = true
		}
	}

	// An empty registry means the commands did not load: that must never wipe a guild's settings.
	if len(live) > 0 {
		for name, cfg := range existing {
			if !live[name] && len(cfg.Builder) == 0 {
				delete(existing, name)
				changed = true
			}
		}
	}
	return changed
}

func defaultCommandConfig(cmd *types.Command) types.CommandConfig {
	cfg := types.CommandConfig{
		Name:            cmd.Name,
		Category:        cmd.Category,
		Description:     cmd.Description,
		Aliases:         append([]string{}, cmd.PrefixAliases...),
		IsEnabled:       !cmd.Disabled,
		IsSlashEnabled:  true,
		IsPrefixEnabled: true,
		Cooldown:        int(cmd.Cooldown.Seconds()),
	}
	reconcileRequirements(&cfg, cmd.Requirements)
	cfg.Responses = responsesOf(cmd.Name)
	return cfg
}

// reconcileRequirements syncs cfg's Required* maps to match requirements

func reconcileRequirements(cfg *types.CommandConfig, requirements []types.Requirement) bool {
	changed := false
	changed = reconcileRequirementMap(&cfg.RequiredRoles, requirements, types.ReqRole) || changed
	changed = reconcileRequirementMap(&cfg.RequiredChannels, requirements, types.ReqChannel) || changed
	changed = reconcileRequirementMap(&cfg.RequiredCategories, requirements, types.ReqCategory) || changed
	return changed
}

// renamedRequirements maps the old key of a requirement to its current one, so an admin's assignment survives the rename.
var renamedRequirements = map[string]string{"chat_mute": "chat_muted_role", "queue_ban": "queue_banned_role", "chat_vc": "vc_muted_role"}

func reconcileRequirementMap(m *map[string]types.RequirementSlot, requirements []types.Requirement, reqType types.RequirementType) bool {
	declared := map[string]bool{} // key -> Required
	for _, req := range requirements {
		if req.Type != reqType || req.Key == "" {
			continue
		}
		declared[req.Key] = !req.Optional
	}

	changed := false
	for old, current := range renamedRequirements {
		if _, wanted := declared[current]; !wanted {
			continue
		}
		if slot, had := (*m)[old]; had {
			if _, has := (*m)[current]; !has {
				(*m)[current] = slot
				changed = true
			}
		}
	}
	for key, required := range declared {
		slot, ok := (*m)[key]
		if !ok {
			if *m == nil {
				*m = map[string]types.RequirementSlot{}
			}
			(*m)[key] = types.RequirementSlot{Required: required}
			changed = true
			continue
		}
		if slot.Required != required {
			slot.Required = required
			(*m)[key] = slot
			changed = true
		}
	}
	for key := range *m {
		if _, ok := declared[key]; !ok {
			delete(*m, key)
			changed = true
		}
	}
	return changed
}

// CommandRequirementSlots returns cmdName's Required* map for the given
func CommandRequirementSlots(guildID, cmdName string, reqType types.RequirementType) (map[string]types.RequirementSlot, bool, error) {
	config, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		return nil, false, err
	}

	cfg, ok := decodeCommandsMap(config.Commands)[cmdName]
	if !ok {
		return nil, false, nil
	}
	switch reqType {
	case types.ReqRole:
		return cfg.RequiredRoles, true, nil
	case types.ReqChannel:
		return cfg.RequiredChannels, true, nil
	case types.ReqCategory:
		return cfg.RequiredCategories, true, nil
	default:
		return nil, true, nil
	}
}

// RequirementSlotID returns the snowflake ID assigned to cmdName's requirement
// key of the given type (empty if unset, or if the command/key don't exist).
func RequirementSlotID(guildID, cmdName string, reqType types.RequirementType, key string) (string, error) {
	slots, _, err := CommandRequirementSlots(guildID, cmdName, reqType)
	if err != nil {
		return "", err
	}
	return slots[key].ID, nil
}

// SetRequirementSlotID assigns id as the snowflake for cmdName's requirement
// key of the given type, leaving the rest of its CommandConfig untouched. A
// requirement is identified by its type and key, so the same assignment is
// made on every other command that declares it (chatmute and chatunmute share
// the chat-muted role), exactly as the dashboard does. Used when an admin
// resolves a missing requirement inline (see requirement_fix.go's
// HandleRequirementFixSelect) or explicitly via /config role|channel|category set.
func SetRequirementSlotID(actor, guildID, cmdName string, reqType types.RequirementType, key, id string) error {
	config, err := database.Must().GuildConfig.Get(guildID)
	if err != nil {
		return err
	}

	commandsMap := decodeCommandsMap(config.Commands)
	if _, ok := commandsMap[cmdName]; !ok {
		return fmt.Errorf("no command config found for %q", cmdName)
	}
	if !assignRequirementSlot(commandsMap, cmdName, reqType, key, id) {
		return fmt.Errorf("unsupported requirement type %q", reqType)
	}

	_, err = database.Must().GuildConfig.SaveField(actor, guildID, "commands", commandsMap)
	return err
}

// assignRequirementSlot points the slot (reqType, key) at id on cmdName and on every command that already has that
// slot. It reports false for an unknown requirement type.
func assignRequirementSlot(commandsMap map[string]types.CommandConfig, cmdName string, reqType types.RequirementType, key, id string) bool {
	slotsOf := func(cfg *types.CommandConfig) *map[string]types.RequirementSlot {
		switch reqType {
		case types.ReqRole:
			return &cfg.RequiredRoles
		case types.ReqChannel:
			return &cfg.RequiredChannels
		case types.ReqCategory:
			return &cfg.RequiredCategories
		}
		return nil
	}
	for name, cfg := range commandsMap {
		m := slotsOf(&cfg)
		if m == nil {
			return false
		}
		slot, has := (*m)[key]
		if name != cmdName && !has {
			continue
		}
		if *m == nil {
			*m = map[string]types.RequirementSlot{}
		}
		slot.ID = id
		(*m)[key] = slot
		commandsMap[name] = cfg
	}
	return true
}

// ============================================================================
// Snapshot Synchronization
// ============================================================================

// SyncGuildSnapshot rebuilds the guild state role/channel/category maps from Discord.
func SyncGuildSnapshot(s *discordgo.Session, guildSnowflake string) error {
	roles := make(map[string]types.DiscordRole)
	roleList, err := s.GuildRoles(guildSnowflake)
	if err != nil {
		// Never overwrite a good snapshot with an empty one on a Discord failure.
		return fmt.Errorf("fetch guild roles: %w", err)
	}
	for _, role := range roleList {
		if role == nil {
			continue
		}
		key := "role_" + SanitizeSnapshotKey(role.Name)
		roles[key] = types.DiscordRole{
			ID:       role.ID,
			Name:     role.Name,
			Color:    role.Color,
			Position: role.Position,
		}
	}

	channels := make(map[string]types.DiscordChannel)
	categories := make(map[string]types.DiscordCategory)
	channelList, err := s.GuildChannels(guildSnowflake)
	if err != nil {
		return fmt.Errorf("fetch guild channels: %w", err)
	}
	for _, channel := range channelList {
		if channel == nil {
			continue
		}
		key := "channel_" + SanitizeSnapshotKey(channel.Name)

		if channel.Type == discordgo.ChannelTypeGuildCategory {
			categories[key] = types.DiscordCategory{
				ID:       channel.ID,
				Name:     channel.Name,
				Type:     int(channel.Type),
				ParentID: channel.ParentID,
				Position: channel.Position,
			}
			continue
		}

		if channel.Type == discordgo.ChannelTypeGuildPublicThread || channel.Type == discordgo.ChannelTypeGuildPrivateThread {
			continue
		}

		channels[key] = types.DiscordChannel{
			ID:       channel.ID,
			Name:     channel.Name,
			Type:     int(channel.Type),
			ParentID: channel.ParentID,
			Position: channel.Position,
		}
	}

	// A failed member fetch keeps the stored staff list instead of emptying it.
	fields := map[string]any{"channels": channels, "categories": categories, "roles": roles}
	if staff, err := GuildStaff(s, guildSnowflake, roleList); err == nil {
		fields["staff"] = staff
	}
	// PATCH only the collections we rebuilt so stored threads are kept.
	_, err = database.Must().GuildState.Patch(sdk.SystemActor, guildSnowflake, fields)
	return err
}

// ============================================================================
// Key Sanitization & Helpers
// ============================================================================

// SanitizeSnapshotKey converts a Discord name into a stable lowercase snapshot key.
func SanitizeSnapshotKey(name string) string {
	var sb strings.Builder
	sb.Grow(len(name))

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			sb.WriteRune(r + ('a' - 'A'))
		default:
			sb.WriteRune('_')
		}
	}

	if sb.Len() == 0 {
		return "unknown"
	}
	return sb.String()
}

// initialSetupComplete flips once the ready-time guild setup has run, so the
// guild_create events Discord sends at startup don't repeat it.
var initialSetupComplete = false

func HandleGuildJoin(s *discordgo.Session, g *discordgo.GuildCreate) {
	logger := baseutility.CreateModuleLogger("SNAPSHOT_SYNC", "GuildCreate")
	if g == nil || g.Guild.ID == "" {
		return
	}

	// Skip automatic setup during initial startup (handled by ready event)
	// Only run setup for guilds joined after startup
	if !initialSetupComplete {
		logger.Info(fmt.Sprintf("Skipping initial guild setup for %s (%s) - will be handled by ready event", g.Guild.Name, g.Guild.ID))
		return
	}

	logger.Info(fmt.Sprintf("Bot joined guild %s (%s), running automatic setup", g.Guild.Name, g.Guild.ID))

	result, err := EnsureGuildSetup(s, g.Guild.ID)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to run automatic guild setup for %s (%s): %v", g.Guild.Name, g.Guild.ID, err))
		return
	}

	logger.Info(fmt.Sprintf("Automatic guild setup completed for %s (%s) - GuildCreated: %v, ConfigCreated: %v, SnapshotCreated: %v, SnapshotSynced: %v, MetaCreated: %v, GameConfigCreated: %v",
		g.Guild.Name, g.Guild.ID, result.GuildCreated, result.ConfigCreated, result.SnapshotCreated, result.SnapshotSynced, result.MetaCreated, result.GameConfigCreated))
}

// setInitialSetupComplete marks the initial setup as complete (called by the guild setup event).
func setInitialSetupComplete() {
	initialSetupComplete = true
}

func HandleGuildSetup(s *discordgo.Session, r *discordgo.Ready) {
	logger := baseutility.CreateModuleLogger("GUILD_SETUP", "GuildSetup")

	if database.Get() == nil {
		logger.Error("Database service not initialized")
		return
	}

	// Get all guilds the bot is in (Discord caps a page at 200)
	var guilds []*discordgo.UserGuild
	after := ""
	for {
		page, err := s.UserGuilds(200, "", after, false)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to fetch guilds: %v", err))
			return
		}
		guilds = append(guilds, page...)
		if len(page) < 200 {
			break
		}
		after = page[len(page)-1].ID
	}

	setupCount := 0
	syncCount := 0
	errorCount := 0

	// EnsureGuildSetup is idempotent: it creates whatever is missing and re-syncs the snapshot.
	for _, guild := range guilds {
		if guild == nil {
			continue
		}

		result, err := EnsureGuildSetup(s, guild.ID)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to set up guild %s (%s): %v", guild.Name, guild.ID, err))
			errorCount++
			continue
		}

		if result.GuildCreated {
			setupCount++
		} else {
			syncCount++
		}
	}

	if errorCount > 0 {
		logger.Error(fmt.Sprintf("Guild setup check complete. New setups: %d, Syncs: %d, Errors: %d", setupCount, syncCount, errorCount))
	}

	// Mark initial setup as complete so guild_create events can now process new guilds
	setInitialSetupComplete()
	logger.Info("Initial guild setup complete, guild_create events will now process new guild joins")
}

// ============================================================================
// Globals & Initialization
// ============================================================================

// Shared by the channel and role sync events: they keep the guild state's
// channel/category/role snapshot in step with Discord.

// stateMu serializes load-modify-save cycles so concurrent Discord events don't lose each other's edits.
var stateMu sync.Mutex

// ============================================================================
// State Helpers
// ============================================================================

func loadGuildState(guildSnowflake string) (*models.GuildStatesModel, bool) {
	svc := database.Get()
	if svc == nil {
		return nil, false
	}

	// The state route accepts the Discord snowflake directly, so no separate guild lookup is needed.
	state, err := svc.GuildState.Get(guildSnowflake)
	if err != nil || state == nil {
		return nil, false
	}

	return state, true
}

// saveGuildState patches only the given collections ("channels", "categories",
// "roles", "threads") so an event touching one collection never overwrites the
// others with stale data. current is the row loadGuildState already read for
// this same event, so this can save without re-reading it just to satisfy
// If-Match.
func saveGuildState(guildSnowflake string, current *models.GuildStatesModel, fields map[string]any) error {
	svc := database.Get()
	if svc == nil {
		return fmt.Errorf("database service not initialized")
	}
	_, err := svc.GuildState.PatchFrom(sdk.SystemActor, guildSnowflake, current, fields)
	return err
}

func stateChannels(state *models.GuildStatesModel) map[string]types.DiscordChannel {
	out := map[string]types.DiscordChannel{}
	if state != nil {
		_ = state.Channels.Unmarshal(&out)
	}
	return out
}

func stateCategories(state *models.GuildStatesModel) map[string]types.DiscordCategory {
	out := map[string]types.DiscordCategory{}
	if state != nil {
		_ = state.Categories.Unmarshal(&out)
	}
	return out
}

func stateRoles(state *models.GuildStatesModel) map[string]types.DiscordRole {
	out := map[string]types.DiscordRole{}
	if state != nil {
		_ = state.Roles.Unmarshal(&out)
	}
	return out
}

func HandleChannelCreate(s *discordgo.Session, c *discordgo.ChannelCreate) {
	logger := baseutility.CreateModuleLogger("SNAPSHOT_SYNC", "ChannelCreate")
	if c == nil || c.GuildID == "" {
		return
	}

	// Skip threads for now - they'll be handled separately if needed
	if c.Type == discordgo.ChannelTypeGuildPublicThread || c.Type == discordgo.ChannelTypeGuildPrivateThread {
		return
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	state, ok := loadGuildState(c.GuildID)
	if !ok {
		return
	}

	key := "channel_" + SanitizeSnapshotKey(c.Name)
	channels := stateChannels(state)
	categories := stateCategories(state)

	switch c.Type {
	case discordgo.ChannelTypeGuildCategory:
		categories[key] = types.DiscordCategory{
			ID:       c.ID,
			Name:     c.Name,
			Type:     int(c.Type),
			ParentID: c.ParentID,
			Position: c.Position,
		}
	default:
		channels[key] = types.DiscordChannel{
			ID:       c.ID,
			Name:     c.Name,
			Type:     int(c.Type),
			ParentID: c.ParentID,
			Position: c.Position,
		}
	}

	if err := saveGuildState(c.GuildID, state, map[string]any{"channels": channels, "categories": categories}); err != nil {
		logger.Error(fmt.Sprintf("Failed to update state for channel create %s: %v", c.Name, err))
	}
}

func HandleChannelUpdate(s *discordgo.Session, c *discordgo.ChannelUpdate) {
	logger := baseutility.CreateModuleLogger("SNAPSHOT_SYNC", "ChannelUpdate")
	if c == nil || c.GuildID == "" {
		return
	}

	// Skip threads for now - they'll be handled separately if needed
	if c.Type == discordgo.ChannelTypeGuildPublicThread || c.Type == discordgo.ChannelTypeGuildPrivateThread {
		return
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	state, ok := loadGuildState(c.GuildID)
	if !ok {
		return
	}

	newKey := "channel_" + SanitizeSnapshotKey(c.Name)

	// First, remove old entries if name changed
	channels := stateChannels(state)
	for key, channelData := range channels {
		if channelData.ID == c.ID && key != newKey {
			delete(channels, key)
			break
		}
	}

	categories := stateCategories(state)
	for key, categoryData := range categories {
		if categoryData.ID == c.ID && key != newKey {
			delete(categories, key)
			break
		}
	}

	// Now add/update with new data
	switch c.Type {
	case discordgo.ChannelTypeGuildCategory:
		categories[newKey] = types.DiscordCategory{
			ID:       c.ID,
			Name:     c.Name,
			Type:     int(c.Type),
			ParentID: c.ParentID,
			Position: c.Position,
		}
	default:
		channels[newKey] = types.DiscordChannel{
			ID:       c.ID,
			Name:     c.Name,
			Type:     int(c.Type),
			ParentID: c.ParentID,
			Position: c.Position,
		}
	}

	if err := saveGuildState(c.GuildID, state, map[string]any{"channels": channels, "categories": categories}); err != nil {
		logger.Error(fmt.Sprintf("Failed to update state for channel update %s: %v", c.Name, err))
	}
}

func HandleChannelDelete(s *discordgo.Session, c *discordgo.ChannelDelete) {
	logger := baseutility.CreateModuleLogger("SNAPSHOT_SYNC", "ChannelDelete")
	if c == nil || c.GuildID == "" {
		return
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	state, ok := loadGuildState(c.GuildID)
	if !ok {
		return
	}

	// Remove channel from state - find the key by ID since we don't have the name
	channels := stateChannels(state)
	for key, channelData := range channels {
		if channelData.ID == c.ID {
			delete(channels, key)
			break
		}
	}

	categories := stateCategories(state)
	for key, categoryData := range categories {
		if categoryData.ID == c.ID {
			delete(categories, key)
			break
		}
	}

	if err := saveGuildState(c.GuildID, state, map[string]any{"channels": channels, "categories": categories}); err != nil {
		logger.Error(fmt.Sprintf("Failed to update state for channel delete %s: %v", c.ID, err))
	}
}

func HandleRoleCreate(s *discordgo.Session, r *discordgo.GuildRoleCreate) {
	logger := baseutility.CreateModuleLogger("SNAPSHOT_SYNC", "RoleCreate")
	if r == nil || r.GuildID == "" {
		return
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	state, ok := loadGuildState(r.GuildID)
	if !ok {
		return
	}

	roles := stateRoles(state)
	key := "role_" + SanitizeSnapshotKey(r.Role.Name)
	roles[key] = types.DiscordRole{
		ID:       r.Role.ID,
		Name:     r.Role.Name,
		Color:    r.Role.Color,
		Position: r.Role.Position,
	}

	if err := saveGuildState(r.GuildID, state, map[string]any{"roles": roles}); err != nil {
		logger.Error(fmt.Sprintf("Failed to update state for role create %s: %v", r.Role.Name, err))
	}
}

func HandleRoleUpdate(s *discordgo.Session, r *discordgo.GuildRoleUpdate) {
	logger := baseutility.CreateModuleLogger("SNAPSHOT_SYNC", "RoleUpdate")
	if r == nil || r.GuildID == "" {
		return
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	state, ok := loadGuildState(r.GuildID)
	if !ok {
		return
	}

	roles := stateRoles(state)
	newKey := "role_" + SanitizeSnapshotKey(r.Role.Name)

	// First, remove old entry if name changed
	for key, roleData := range roles {
		if roleData.ID == r.Role.ID && key != newKey {
			delete(roles, key)
			break
		}
	}

	// Now add/update with new data
	roles[newKey] = types.DiscordRole{
		ID:       r.Role.ID,
		Name:     r.Role.Name,
		Color:    r.Role.Color,
		Position: r.Role.Position,
	}

	if err := saveGuildState(r.GuildID, state, map[string]any{"roles": roles}); err != nil {
		logger.Error(fmt.Sprintf("Failed to update state for role update %s: %v", r.Role.Name, err))
	}
}

func HandleRoleDelete(s *discordgo.Session, r *discordgo.GuildRoleDelete) {
	logger := baseutility.CreateModuleLogger("SNAPSHOT_SYNC", "RoleDelete")
	if r == nil || r.GuildID == "" {
		return
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	state, ok := loadGuildState(r.GuildID)
	if !ok {
		return
	}

	roles := stateRoles(state)
	for key, roleData := range roles {
		if roleData.ID == r.RoleID {
			delete(roles, key)
			break
		}
	}

	if err := saveGuildState(r.GuildID, state, map[string]any{"roles": roles}); err != nil {
		logger.Error(fmt.Sprintf("Failed to update state for role delete %s: %v", r.RoleID, err))
	}
}

const appearanceSyncInterval = 30 * time.Minute

func HandleAppearanceSync(s *discordgo.Session, r *discordgo.Ready) {
	logger := baseutility.CreateModuleLogger("APPEARANCE_SYNC", "AppearanceSync")

	go runAppearanceSyncCycle(s, logger)

	go func() {
		ticker := time.NewTicker(appearanceSyncInterval)
		defer ticker.Stop()

		for range ticker.C {
			runAppearanceSyncCycle(s, logger)
		}
	}()
}

func runAppearanceSyncCycle(s *discordgo.Session, logger baseutility.AppLogger) {
	svc := database.Get()
	if svc == nil {
		logger.Error("Database service not initialized")
		return
	}

	guilds, err := svc.Guild.ListAll("", false)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to fetch guilds: %v", err))
		return
	}

	for _, guild := range guilds {
		if err := updateGuildAppearance(s, guild.SnowflakeID, logger); err != nil {
			logger.Error(fmt.Sprintf("Failed to update appearance for guild %s: %v", guild.SnowflakeID, err))
		}
	}
}

// updateGuildAppearance re-applies the guild's configured bot appearance
// (nickname, avatar, banner, bio) every cycle.
//
// ponytail: the old schema tracked an appearance_needs_update flag to skip
// unchanged guilds; the current schema has no such column, so this just
// re-applies unconditionally. These are idempotent Discord calls on a 30-minute
// interval, so the extra calls are cheap; add the flag back (on
// models.GuildConfigsModel) if this ever runs against enough guilds for it to matter.
func updateGuildAppearance(s *discordgo.Session, snowflakeID string, logger baseutility.AppLogger) error {
	if snowflakeID == "" {
		return fmt.Errorf("guild has no snowflake")
	}

	config, err := database.Must().GuildConfig.Get(snowflakeID)
	if sdk.IsNotFound(err) {
		return nil // no config row yet: nothing to sync
	}
	if err != nil {
		return fmt.Errorf("failed to get guild config: %w", err)
	}

	var appearance types.AppearanceConfig
	if !config.Appearance.IsEmpty() {
		_ = config.Appearance.Unmarshal(&appearance)
	}
	if appearance == (types.AppearanceConfig{}) {
		return nil // nothing configured
	}

	botToken := os.Getenv("DISCORD_BOT_TOKEN")
	if botToken == "" {
		return fmt.Errorf("DISCORD_BOT_TOKEN environment variable is not set")
	}
	restAPI := NewDiscordRestAPI(botToken)

	if appearance.Nickname != "" {
		if err := s.GuildMemberNickname(snowflakeID, "@me", appearance.Nickname); err != nil {
			logger.Error(fmt.Sprintf("Failed to update nickname for guild %s: %v", snowflakeID, err))
		}
	}

	if appearance.Avatar != "" {
		if err := restAPI.UpdateGuildMemberAvatar(snowflakeID, appearance.Avatar); err != nil {
			logger.Error(fmt.Sprintf("Failed to update avatar for guild %s: %v", snowflakeID, err))
		}
	} else {
		if err := restAPI.UpdateGuildMemberAvatar(snowflakeID, ""); err != nil {
			logger.Error(fmt.Sprintf("Failed to remove avatar for guild %s: %v", snowflakeID, err))
		}
	}

	if appearance.Banner != "" {
		if err := restAPI.UpdateGuildMemberBanner(snowflakeID, appearance.Banner); err != nil {
			logger.Error(fmt.Sprintf("Failed to update banner for guild %s: %v", snowflakeID, err))
		}
	} else {
		if err := restAPI.UpdateGuildMemberBanner(snowflakeID, ""); err != nil {
			logger.Error(fmt.Sprintf("Failed to remove banner for guild %s: %v", snowflakeID, err))
		}
	}

	if appearance.Bio != "" {
		if err := restAPI.UpdateGuildMemberBio(snowflakeID, appearance.Bio); err != nil {
			logger.Error(fmt.Sprintf("Failed to update bio for guild %s: %v", snowflakeID, err))
		}
	} else {
		if err := restAPI.UpdateGuildMemberBio(snowflakeID, ""); err != nil {
			logger.Error(fmt.Sprintf("Failed to remove bio for guild %s: %v", snowflakeID, err))
		}
	}

	return nil
}
