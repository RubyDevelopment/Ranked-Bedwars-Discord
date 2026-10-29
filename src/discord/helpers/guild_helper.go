package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/types"
	appLog "ranked-bedwars/src/utility"
	baseutility "ranked-bedwars/src/utility"
)

var errGuildConfigUnchanged = errors.New("guild config unchanged")

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
	err := database.Must().GuildConfig.SaveField(actorID, guildID, "lfg", lfg)
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

	if err := syncGuildDiscordProfile(s, guild); err != nil {
		return nil, fmt.Errorf("sync Discord profile: %w", err)
	}

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

func discordGuildAssetURL(guildID, hash, asset string, size int) string {
	if hash == "" {
		return ""
	}
	extension := "png"
	if strings.HasPrefix(hash, "a_") {
		extension = "gif"
	}
	return fmt.Sprintf("https://cdn.discordapp.com/%s/%s/%s.%s?size=%d", asset, guildID, hash, extension, size)
}

// liveBotMember is what Discord currently shows for the bot itself in a guild.
type liveBotMember struct {
	Nick   *string `json:"nick"`
	Bio    *string `json:"bio"`
	Avatar *string `json:"avatar"`
	Banner *string `json:"banner"`
}

// fetchBotApplicationDescription reads the bot's application description, which Discord shows as the bot's
// "About me" in every server where it has no server-specific bio.
func fetchBotApplicationDescription() (string, error) {
	api, err := discordRestAPI()
	if err != nil {
		return "", err
	}
	var application struct {
		Description string `json:"description"`
	}
	if err := api.makeRequest("GET", api.baseURL+"/applications/@me", nil, &application); err != nil {
		return "", err
	}
	return strings.TrimSpace(application.Description), nil
}

// guildMemberAssetURL is the CDN address of a server-specific avatar or banner of a member.
func guildMemberAssetURL(guildID, userID, hash, asset string, size int) string {
	if hash == "" || userID == "" {
		return ""
	}
	extension := "png"
	if strings.HasPrefix(hash, "a_") {
		extension = "gif"
	}
	return fmt.Sprintf("https://cdn.discordapp.com/guilds/%s/users/%s/%s/%s.%s?size=%d", guildID, userID, asset, hash, extension, size)
}

// fetchLiveBotMember reads the bot's own member record in a guild (nickname, server avatar and banner).
func fetchLiveBotMember(guildID, botUserID string) (liveBotMember, error) {
	var member liveBotMember
	api, err := discordRestAPI()
	if err != nil {
		return member, err
	}
	// Bots address themselves by id: "@me" only works for PATCH, not for reading.
	endpoint := fmt.Sprintf("%s/guilds/%s/members/%s", api.baseURL, guildID, botUserID)
	err = api.makeRequest("GET", endpoint, nil, &member)
	return member, err
}

// botLiveProfile is what Discord currently shows for the bot in one guild.
type botLiveProfile struct {
	nickname, bio, avatarURL, bannerURL string
}

var (
	liveProfileMu      sync.Mutex
	liveProfileChecked = map[string]cachedLiveProfile{} // guild -> last lookup, so a sync does not ask Discord every time
	liveProfileLogged  = map[string]bool{}              // guild+problem -> already reported
)

type cachedLiveProfile struct {
	at      time.Time
	profile botLiveProfile
	ok      bool
}

const liveProfileRecheck = 6 * time.Hour

func logLiveProfileOnce(guildID, problem string, err error) {
	liveProfileMu.Lock()
	defer liveProfileMu.Unlock()
	key := guildID + "|" + problem
	if liveProfileLogged[key] {
		return
	}
	liveProfileLogged[key] = true
	log.Printf("guild %s: %s: %v (not repeated for this guild)", guildID, problem, err)
}

// readBotLiveProfile looks up the bot's nickname, bio, server avatar and server banner. It returns ok=false when it
// cannot look; a failed lookup only means the dashboard keeps its saved values. The answer is reused for a few hours.
func readBotLiveProfile(s *discordgo.Session, guildID string) (profile botLiveProfile, ok bool) {
	if s.State == nil || s.State.User == nil {
		return profile, false
	}
	liveProfileMu.Lock()
	if last, seen := liveProfileChecked[guildID]; seen && time.Since(last.at) < liveProfileRecheck {
		liveProfileMu.Unlock()
		return last.profile, last.ok
	}
	liveProfileMu.Unlock()
	defer func() {
		liveProfileMu.Lock()
		liveProfileChecked[guildID] = cachedLiveProfile{at: time.Now(), profile: profile, ok: ok}
		liveProfileMu.Unlock()
	}()

	botUserID := s.State.User.ID
	member, err := fetchLiveBotMember(guildID, botUserID)
	if err != nil {
		logLiveProfileOnce(guildID, "reading the bot's server profile over REST failed", err)
		// Without the REST token the gateway still knows the nickname, avatar and banner (not the bio).
		gatewayMember, gatewayErr := s.GuildMember(guildID, botUserID)
		if gatewayErr != nil || gatewayMember == nil {
			if gatewayErr != nil {
				logLiveProfileOnce(guildID, "reading the bot's member record failed", gatewayErr)
			}
			return profile, false
		}
		member = liveBotMember{Nick: &gatewayMember.Nick, Avatar: &gatewayMember.Avatar, Banner: &gatewayMember.Banner}
	}
	profile.nickname = strings.TrimSpace(derefString(member.Nick))
	profile.bio = strings.TrimSpace(derefString(member.Bio))
	if profile.bio == "" {
		// No server-specific bio: Discord shows the bot's application description instead.
		if description, err := fetchBotApplicationDescription(); err == nil {
			profile.bio = description
		} else {
			logLiveProfileOnce(guildID, "reading the bot's application description failed", err)
		}
	}
	profile.avatarURL = guildMemberAssetURL(guildID, botUserID, derefString(member.Avatar), "avatars", 128)
	profile.bannerURL = guildMemberAssetURL(guildID, botUserID, derefString(member.Banner), "banners", 1024)
	return profile, true
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// syncGuildDiscordProfile stores the server's live branding independently from
// bot-specific overrides. Legacy asset fields are migrated without losing custom URLs.
func syncGuildDiscordProfile(s *discordgo.Session, guild *discordgo.Guild) error {
	if s == nil {
		return fmt.Errorf("Discord session is nil")
	}
	if guild == nil || guild.ID == "" {
		return fmt.Errorf("guild is nil or has no ID")
	}

	guildWithCounts, err := s.GuildWithCounts(guild.ID)
	if err != nil {
		return fmt.Errorf("fetch Discord member count: %w", err)
	}
	if guildWithCounts == nil {
		return fmt.Errorf("fetch Discord member count: Discord returned no guild data")
	}
	memberCount := guildWithCounts.ApproximateMemberCount
	if memberCount == 0 {
		memberCount = guild.MemberCount
	}

	nextProfile := types.DiscordGuildProfile{
		ServerIconURL:   discordGuildAssetURL(guild.ID, guild.Icon, "icons", 128),
		ServerBannerURL: discordGuildAssetURL(guild.ID, guild.Banner, "banners", 1024),
		MemberCount:     memberCount,
	}
	migratedLegacy := false
	err = database.Must().GuildConfig.UpdateField(sdk.SystemActor, guild.ID, "discord_profile", func(config *models.GuildConfigsModel) (any, error) {
		var profile types.DiscordGuildProfile
		if !config.DiscordProfile.IsEmpty() {
			if err := config.DiscordProfile.Unmarshal(&profile); err != nil {
				return nil, fmt.Errorf("decode previous Discord profile: %w", err)
			}
		}
		previous := profile
		legacyChanged := profile.NormalizeLegacy()
		profileMigration := profile.BotProfileVersion < 1
		if profileMigration {
			var legacyAppearance types.AppearanceConfig
			if !config.Appearance.IsEmpty() {
				if err := config.Appearance.Unmarshal(&legacyAppearance); err != nil {
					return nil, fmt.Errorf("decode legacy appearance: %w", err)
				}
			}
			profile.MigrateLegacyAppearance(legacyAppearance, previous)
			migratedLegacy = !config.Appearance.IsEmpty()
		}
		if nextProfile.MemberCount <= 0 {
			nextProfile.MemberCount = profile.MemberCount
		}
		// Nothing was ever configured for the bot's name or about text: adopt what Discord already shows, and record
		// it as applied so it is not sent back.
		seeded := false
		needsNickname := profile.Nickname == "" && profile.AppliedAppearance.Nickname == ""
		needsBio := profile.Bio == "" && profile.AppliedAppearance.Bio == ""
		needsAvatar := profile.AppliedAppearance.Avatar == "" && profile.BotIconURL == ""
		needsBanner := profile.AppliedAppearance.Banner == "" && profile.BotBannerURL == ""
		if needsNickname || needsBio || needsAvatar || needsBanner {
			if live, ok := readBotLiveProfile(s, guild.ID); ok {
				if needsNickname && live.nickname != "" {
					profile.Nickname = live.nickname
					profile.AppliedAppearance.Nickname = live.nickname
					seeded = true
				}
				if needsBio && live.bio != "" {
					profile.Bio = live.bio
					profile.AppliedAppearance.Bio = live.bio
					seeded = true
				}
				if needsAvatar && live.avatarURL != "" {
					profile.BotIconMode = "custom"
					profile.BotIconURL = live.avatarURL
					profile.AppliedAppearance.Avatar = live.avatarURL
					seeded = true
				}
				if needsBanner && live.bannerURL != "" {
					profile.BotBannerMode = "custom"
					profile.BotBannerURL = live.bannerURL
					profile.AppliedAppearance.Banner = live.bannerURL
					seeded = true
				}
			}
		}
		changed := legacyChanged || seeded ||
			profileMigration ||
			profile.ServerIconURL != nextProfile.ServerIconURL ||
			profile.ServerBannerURL != nextProfile.ServerBannerURL ||
			profile.MemberCount != nextProfile.MemberCount ||
			profile.SyncedAt == ""
		if !changed {
			return nil, errGuildConfigUnchanged
		}
		profile.ServerIconURL = nextProfile.ServerIconURL
		profile.ServerBannerURL = nextProfile.ServerBannerURL
		profile.MemberCount = nextProfile.MemberCount
		profile.SyncedAt = time.Now().UTC().Format(time.RFC3339)
		return profile, nil
	})
	if err != nil && !errors.Is(err, errGuildConfigUnchanged) {
		return fmt.Errorf("save Discord profile: %w", err)
	}
	if migratedLegacy {
		err = database.Must().GuildConfig.UpdateField(sdk.SystemActor, guild.ID, "appearance", func(config *models.GuildConfigsModel) (any, error) {
			if config.Appearance.IsEmpty() {
				return nil, errGuildConfigUnchanged
			}
			return map[string]any{}, nil
		})
		if err != nil && !errors.Is(err, errGuildConfigUnchanged) {
			return fmt.Errorf("clear migrated appearance config: %w", err)
		}
	}
	return nil
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
		err = database.Must().GuildConfig.SaveField(sdk.SystemActor, guildID, "invite_code", inv.Code)
		return err
	}
	return fmt.Errorf("no channel the bot can create an invite in")
}

// ============================================================================
// Command Configuration Seeding
// ============================================================================

// reconcileRequirements syncs cfg's Required* maps to match requirements

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
		return
	}

	result, err := EnsureGuildSetup(s, g.Guild.ID)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to run automatic guild setup for %s (%s): %v", g.Guild.Name, g.Guild.ID, err))
		return
	}
	if err := updateGuildAppearance(s, g.Guild.ID, logger); err != nil {
		logger.Error(fmt.Sprintf("Failed to apply default appearance for %s (%s): %v", g.Guild.Name, g.Guild.ID, err))
	}
	if result.GuildCreated {
		notifyUnassignedGuild(s, g.Guild.ID, logger)
	}

}

// HandleGuildUpdate refreshes branding after Discord changes a guild's icon or banner.
func HandleGuildUpdate(s *discordgo.Session, g *discordgo.GuildUpdate) {
	if g == nil || g.Guild == nil || g.Guild.ID == "" || database.Get() == nil {
		return
	}
	logger := baseutility.CreateModuleLogger("GUILD_SETUP", "GuildUpdate")
	if err := syncGuildDiscordProfile(s, g.Guild); err != nil {
		logger.Error(fmt.Sprintf("Failed to save branding for guild %s: %v", g.Guild.ID, err))
		return
	}
	if err := updateGuildAppearance(s, g.Guild.ID, logger); err != nil {
		logger.Error(fmt.Sprintf("Failed to apply branding for guild %s: %v", g.Guild.ID, err))
	}
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
		if err := updateGuildAppearance(s, guild.ID, logger); err != nil {
			logger.Error(fmt.Sprintf("Failed to apply default appearance for %s (%s): %v", guild.Name, guild.ID, err))
		}

		if result.GuildCreated {
			setupCount++
			notifyUnassignedGuild(s, guild.ID, logger)
		} else {
			syncCount++
		}
	}

	if errorCount > 0 {
		logger.Error(fmt.Sprintf("Guild setup check complete. New setups: %d, Syncs: %d, Errors: %d", setupCount, syncCount, errorCount))
	}

	// Mark initial setup as complete so guild_create events can now process new guilds
	setInitialSetupComplete()
}

func notifyUnassignedGuild(s *discordgo.Session, guildID string, logger baseutility.AppLogger) {
	guild, err := s.Guild(guildID)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load guild %s to send setup notice: %v", guildID, err))
		return
	}
	if guild.SystemChannelID == "" {
		logger.Info(fmt.Sprintf("Guild %s has no system channel; skipped the plan setup notice", guildID))
		return
	}
	_, err = s.ChannelMessageSendComplex(guild.SystemChannelID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{unassignedGuildEmbed()},
		Components: unassignedGuildComponents(),
	})
	if err != nil {
		logger.Error(fmt.Sprintf("Could not send plan setup notice to guild %s: %v", guildID, err))
	}
}

// ============================================================================
// Embed Builders
// ============================================================================

// unassignedGuildComponents are the dashboard links under that notice (none when no dashboard URL is configured).
func unassignedGuildComponents() []discordgo.MessageComponent {
	base := dashboardBase()
	if base == "" {
		return nil
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Request Access", Style: discordgo.LinkButton, URL: base + "/setup"},
			discordgo.Button{Label: "View Pricing", Style: discordgo.LinkButton, URL: base + "/pricing"},
		}},
	}
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

// HandleAppearanceSync brings every server up to date once when the bot starts (it may have been offline while the
// dashboard changed something). Later changes arrive one at a time through ServeAppearance, as they are saved.
func HandleAppearanceSync(s *discordgo.Session, r *discordgo.Ready) {
	logger := baseutility.CreateModuleLogger("APPEARANCE_SYNC", "AppearanceSync")

	appearanceSession.Store(s)
	go runAppearanceSyncCycle(s, logger)
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
		liveGuild, err := s.Guild(guild.SnowflakeID)
		if err != nil {
			if isDiscordRESTCode(err, 10004) {
				continue
			}
			logger.Error(fmt.Sprintf("Failed to refresh server branding for guild %s: %v", guild.SnowflakeID, err))
			continue
		}
		if err := syncGuildDiscordProfile(s, liveGuild); err != nil {
			logger.Error(fmt.Sprintf("Failed to save server branding for guild %s: %v", guild.SnowflakeID, err))
		}
		if err := updateGuildAppearance(s, guild.SnowflakeID, logger); err != nil {
			logger.Error(fmt.Sprintf("Failed to update appearance for guild %s: %v", guild.SnowflakeID, err))
		}
	}
}

var appearanceApplyMu sync.Mutex

// updateGuildAppearance applies only values that differ from the last values
// successfully sent to Discord.
func updateGuildAppearance(s *discordgo.Session, snowflakeID string, logger baseutility.AppLogger) error {
	appearanceApplyMu.Lock()
	defer appearanceApplyMu.Unlock()

	if snowflakeID == "" {
		return fmt.Errorf("guild has no snowflake")
	}

	// Fresh, not cached: this runs right after the dashboard saved, and a cached copy would still be the old profile.
	config, err := database.Must().GuildConfig.GetFresh(snowflakeID)
	if sdk.IsNotFound(err) {
		return nil // no config row yet: nothing to sync
	}
	if err != nil {
		return fmt.Errorf("failed to get guild config: %w", err)
	}

	var profile types.DiscordGuildProfile
	if !config.DiscordProfile.IsEmpty() {
		if err := config.DiscordProfile.Unmarshal(&profile); err != nil {
			return fmt.Errorf("failed to decode server profile: %w", err)
		}
	}
	profile.NormalizeLegacy()

	desired := types.SyncedBotAppearance{
		Nickname: profile.Nickname,
		Bio:      profile.Bio,
	}
	// Unless someone configures it, the bot keeps its own icon and banner ("default" sends nothing, and clears an
	// override that was set before): the server's are only used when the guild picks "server".
	switch profile.BotIconMode {
	case "custom":
		desired.Avatar = profile.BotIconURL
	case "server":
		desired.Avatar = profile.ServerIconURL
	}
	switch profile.BotBannerMode {
	case "custom":
		desired.Banner = profile.BotBannerURL
	case "server":
		desired.Banner = profile.ServerBannerURL
	}

	applied := profile.AppliedAppearance
	nextApplied := applied
	nextBlockedAvatar := profile.BlockedAvatarURL
	nextBlockedBanner := profile.BlockedBannerURL
	stateChanged := false

	if types.ShouldApplyAppearance(desired.Nickname, applied.Nickname, false) {
		if err := s.GuildMemberNickname(snowflakeID, "@me", desired.Nickname); err != nil {
			if !isDiscordRESTCode(err, 10007) {
				logger.Error(fmt.Sprintf("Failed to update nickname for guild %s: %v", snowflakeID, err))
			}
		} else {
			nextApplied.Nickname = desired.Nickname
			stateChanged = true
		}
	}

	if types.ShouldApplyAppearance(desired.Avatar, applied.Avatar, desired.Avatar != "" && desired.Avatar == profile.BlockedAvatarURL) {
		restAPI, err := discordRestAPI()
		if err != nil {
			return err
		}
		if err := restAPI.UpdateGuildMemberAvatar(snowflakeID, desired.Avatar); err != nil {
			if isMissingDiscordImage(err) {
				logger.Error(fmt.Sprintf("Skipping unavailable avatar URL for guild %s until it changes: %v", snowflakeID, err))
				nextBlockedAvatar = desired.Avatar
				stateChanged = true
			} else if !isDiscordRESTCode(err, 10007) {
				logger.Error(fmt.Sprintf("Failed to update avatar for guild %s: %v", snowflakeID, err))
			}
		} else {
			nextApplied.Avatar = desired.Avatar
			nextBlockedAvatar = ""
			stateChanged = true
		}
	}

	if types.ShouldApplyAppearance(desired.Banner, applied.Banner, desired.Banner != "" && desired.Banner == profile.BlockedBannerURL) {
		restAPI, err := discordRestAPI()
		if err != nil {
			return err
		}
		if err := restAPI.UpdateGuildMemberBanner(snowflakeID, desired.Banner); err != nil {
			if isMissingDiscordImage(err) {
				logger.Error(fmt.Sprintf("Skipping unavailable banner URL for guild %s until it changes: %v", snowflakeID, err))
				nextBlockedBanner = desired.Banner
				stateChanged = true
			} else if !isDiscordRESTCode(err, 10007) {
				logger.Error(fmt.Sprintf("Failed to update banner for guild %s: %v", snowflakeID, err))
			}
		} else {
			nextApplied.Banner = desired.Banner
			nextBlockedBanner = ""
			stateChanged = true
		}
	}

	if types.ShouldApplyAppearance(desired.Bio, applied.Bio, false) {
		restAPI, err := discordRestAPI()
		if err != nil {
			return err
		}
		if err := restAPI.UpdateGuildMemberBio(snowflakeID, desired.Bio); err != nil {
			if !isDiscordRESTCode(err, 10007) {
				logger.Error(fmt.Sprintf("Failed to update bio for guild %s: %v", snowflakeID, err))
			}
		} else {
			nextApplied.Bio = desired.Bio
			stateChanged = true
		}
	}

	if !stateChanged {
		return nil
	}

	err = database.Must().GuildConfig.UpdateField(sdk.SystemActor, snowflakeID, "discord_profile", func(config *models.GuildConfigsModel) (any, error) {
		var current types.DiscordGuildProfile
		if !config.DiscordProfile.IsEmpty() {
			if err := config.DiscordProfile.Unmarshal(&current); err != nil {
				return nil, fmt.Errorf("decode Discord profile appearance state: %w", err)
			}
		}
		if current.AppliedAppearance == nextApplied &&
			current.BlockedAvatarURL == nextBlockedAvatar &&
			current.BlockedBannerURL == nextBlockedBanner {
			return nil, errGuildConfigUnchanged
		}
		current.AppliedAppearance = nextApplied
		current.BlockedAvatarURL = nextBlockedAvatar
		current.BlockedBannerURL = nextBlockedBanner
		return current, nil
	})
	if errors.Is(err, errGuildConfigUnchanged) {
		return nil
	}
	return err
}

func discordRestAPI() (*DiscordRestAPI, error) {
	botToken := os.Getenv("DISCORD_BOT_TOKEN")
	if botToken == "" {
		return nil, fmt.Errorf("DISCORD_BOT_TOKEN environment variable is not set")
	}
	return NewDiscordRestAPI(botToken), nil
}

func isDiscordRESTCode(err error, code int) bool {
	var restErr *discordgo.RESTError
	return errors.As(err, &restErr) && restErr.Message != nil && restErr.Message.Code == code
}

// A guild's plan comes from the Database (guilds.tier and tier_expires_at), which the developer sets by hand
// (approving a plan request in the dashboard) and the Database clears when the period and its grace days run out. The bot only
// reads it: while the plan is active the bot works, once it lapses commands, buttons and new games are refused with
// how to renew. Ongoing upkeep (rank decay, syncs, an already running game) is deliberately not stopped.

// ErrPlanInactive is returned when something that needs a plan (starting a game) is refused for lack of one.
var ErrPlanInactive = errors.New("this server's plan is not active")

// planExemptCommands stay usable without a plan, so an administrator can still set the server up and see its status.
var planExemptCommands = map[string]bool{"setup": true, "config": true, "bots": true}

// planEnforced is false in development mode and when PLAN_ENFORCEMENT=off (a switch for rolling plans out to servers
// that were registered before they existed).
func planEnforced() bool {
	return !devMode() && strings.ToLower(strings.TrimSpace(os.Getenv("PLAN_ENFORCEMENT"))) != "off"
}

// planGrandfathered exempts servers that were registered before PLAN_GRANDFATHER_BEFORE (an RFC 3339 date, unset = nobody)
// and never had a plan, so existing communities can be moved onto paid plans gradually instead of all at once.
func planGrandfathered(g *models.GuildsModel) bool {
	raw := strings.TrimSpace(os.Getenv("PLAN_GRANDFATHER_BEFORE"))
	if raw == "" || g.Tier != nil || g.TierExpiresAt != nil {
		return false
	}
	cutoff, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		cutoff, err = time.Parse("2006-01-02", raw)
	}
	return err == nil && g.CreatedAt.Before(cutoff)
}

// planCounts reports whether the guild may use the bot right now.
func planCounts(g *models.GuildsModel, now time.Time) bool {
	return g.IsEnabled && (g.PlanActive(now) || planGrandfathered(g))
}

// dashboardBase is the dashboard's address from DASHBOARD_URL, without a trailing slash ("" when unset).
func dashboardBase() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("DASHBOARD_URL")), "/")
}

// planPageURL is where an administrator asks for or renews a plan: the guild's plan page in the dashboard.
func planPageURL(guildID string) string {
	base := dashboardBase()
	if base == "" {
		return ""
	}
	if guildID == "" {
		return base + "/setup"
	}
	return base + "/dashboard/" + guildID + "/plan"
}

// lastKnown remembers each guild's most recent plan answer. It makes a click cheap (no Database call within
// planCacheTTL) and keeps an expired server locked, instead of failing open, while the Database is unreachable.
type lastKnown struct {
	active   bool
	disabled bool
	expires  *time.Time
	at       time.Time
}

const planCacheTTL = 30 * time.Second

var (
	planMu    sync.Mutex
	planCache = map[string]lastKnown{}
)

func rememberPlan(guildID string, g *models.GuildsModel, now time.Time) {
	planMu.Lock()
	defer planMu.Unlock()
	planCache[guildID] = lastKnown{active: planCounts(g, now), disabled: !g.IsEnabled, expires: g.TierExpiresAt, at: now}
}

func recallPlan(guildID string) (lastKnown, bool) {
	planMu.Lock()
	defer planMu.Unlock()
	k, ok := planCache[guildID]
	return k, ok
}

// planDenial is nil when the guild may use the bot right now, else the embed explaining why not. When the guild could
// not be loaded (gc.Guild nil) it falls back to the last known answer, and lets the command through only if there is none.
func planDenial(gc GuildContext, guildID string, cmd *types.Command, now time.Time) *discordgo.MessageEmbed {
	if !planEnforced() || planExemptCommands[cmd.Name] {
		return nil
	}
	if gc.Guild != nil {
		rememberPlan(guildID, gc.Guild, now)
		if planCounts(gc.Guild, now) {
			return nil
		}
		return inactiveEmbed(guildID, !gc.Guild.IsEnabled, gc.Guild.TierExpiresAt)
	}
	if k, ok := recallPlan(guildID); ok && !k.active {
		return inactiveEmbed(guildID, k.disabled, k.expires)
	}
	return nil
}

// guildPlanOK answers "may this server use the bot now" from the cache or one Database lookup, failing open only when
// the server was never seen and cannot be loaded.
func guildPlanOK(guildID string) (ok bool, embed *discordgo.MessageEmbed) {
	if !planEnforced() || guildID == "" {
		return true, nil
	}
	now := time.Now()
	if k, found := recallPlan(guildID); found && now.Sub(k.at) < planCacheTTL {
		if k.active {
			return true, nil
		}
		return false, inactiveEmbed(guildID, k.disabled, k.expires)
	}
	guild, err := lookupGuildBySnowflake(guildID)
	if err != nil || guild == nil {
		if k, found := recallPlan(guildID); found && !k.active {
			return false, inactiveEmbed(guildID, k.disabled, k.expires)
		}
		return true, nil
	}
	rememberPlan(guildID, guild, now)
	if planCounts(guild, now) {
		return true, nil
	}
	return false, inactiveEmbed(guildID, !guild.IsEnabled, guild.TierExpiresAt)
}

// GuildPlanActive reports whether the server may use the bot now (for work that is not a command, e.g. starting a game).
func GuildPlanActive(guildID string) bool {
	ok, _ := guildPlanOK(guildID)
	return ok
}

// planBlocksComponent answers a button or menu click from a server without a plan and reports that it did.
func planBlocksComponent(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	ok, embed := guildPlanOK(i.GuildID)
	if ok {
		return false
	}
	_ = Respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Flags: discordgo.MessageFlagsEphemeral},
	})
	return true
}

// GuildHasFeature reports whether the guild's active plan includes `feature` ("automated-hosting", "custom-portal").
// Fails open when the guild cannot be loaded.
func GuildHasFeature(guildID, feature string) bool {
	if !planEnforced() {
		return true
	}
	guild, err := lookupGuildBySnowflake(guildID)
	if err != nil || guild == nil {
		return true
	}
	if planGrandfathered(guild) {
		return true
	}
	return guild.HasFeature(feature, time.Now())
}

// devMode is true when DEV_MODE is set: local testing without plan checks.
func devMode() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DEV_MODE")))
	return v == "1" || v == "true" || v == "yes"
}

// ============================================================================
// Reminders
// ============================================================================

const (
	planReminderEvery  = 6 * time.Hour
	planReminderWindow = 3 * 24 * time.Hour // warn this long before the plan ends
	planExpiredWindow  = 7 * 24 * time.Hour // tell the owner about an ended plan for this long after it
)

// remindedPlans remembers which notice each server's owner already got, per expiry date, so a restart is the only way
// to be told twice. ponytail: in memory; persist it in the Database if restarts become frequent.
var (
	remindMu       sync.Mutex
	remindedPlans  = map[string]string{}
	remindedEnding = "ending"
	remindedEnded  = "ended"
)

// HandlePlanReminders DMs each server's owner shortly before the plan ends and once after it has, until renewed.
func HandlePlanReminders(s *discordgo.Session, r *discordgo.Ready) {
	if !planEnforced() {
		return
	}
	logger := baseutility.CreateModuleLogger("PLAN_REMINDERS", "PlanReminders")
	go func() {
		for {
			runPlanReminders(s, logger, time.Now())
			time.Sleep(planReminderEvery)
		}
	}()
}

func runPlanReminders(s *discordgo.Session, logger baseutility.AppLogger, now time.Time) {
	svc := database.Get()
	if svc == nil {
		return
	}
	guilds, err := svc.Guild.ListAll("", false)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to fetch guilds for plan reminders: %v", err))
		return
	}
	for i := range guilds {
		g := &guilds[i]
		if g.TierExpiresAt == nil {
			continue
		}
		var kind, text string
		switch {
		case g.PlanActive(now) && g.Tier != nil && g.TierExpiresAt.Sub(now) <= planReminderWindow && g.TierExpiresAt.After(now):
			kind = remindedEnding
			if g.PlanCancelRequestedAt != nil {
				text = fmt.Sprintf("**%s**'s Ranked Bedwars plan ends on <t:%d:D> because you asked to cancel it. To keep the bot running, say so or request a renewal here", g.Name, g.TierExpiresAt.Unix())
			} else {
				text = fmt.Sprintf("**%s**'s Ranked Bedwars plan ends on <t:%d:D>. Plans are renewed by hand, so request a renewal here before then", g.Name, g.TierExpiresAt.Unix())
			}
		case !g.PlanActive(now) && now.Sub(*g.TierExpiresAt) <= planExpiredWindow:
			kind = remindedEnded
			text = fmt.Sprintf("**%s**'s Ranked Bedwars plan ended on <t:%d:D>, so the bot is paused there. Your configuration is kept; request a renewal here to switch it back on", g.Name, g.TierExpiresAt.Unix())
		default:
			continue
		}
		key := kind + g.TierExpiresAt.Format(time.RFC3339)
		remindMu.Lock()
		done := remindedPlans[g.SnowflakeID] == key
		remindedPlans[g.SnowflakeID] = key
		remindMu.Unlock()
		if done {
			continue
		}
		live, err := s.Guild(g.SnowflakeID)
		if err != nil || live.OwnerID == "" {
			continue // the bot is no longer in that server
		}
		dm, err := s.UserChannelCreate(live.OwnerID)
		if err != nil {
			continue
		}
		if url := planPageURL(g.SnowflakeID); url != "" {
			text += ": " + url
		} else {
			text += " in the dashboard"
		}
		if _, err := s.ChannelMessageSendEmbed(dm.ID, &discordgo.MessageEmbed{Title: "Ranked Bedwars plan", Description: text, Color: ColorError}); err != nil {
			logger.Error(fmt.Sprintf("Could not DM the owner of %s about the plan: %v", g.SnowflakeID, err))
		}
	}
}

// guildWatch runs a periodic sweep only for guilds that have something pending. A guild is added by Track (the
// moment something is created, or at startup when the database already holds something) and dropped by the first
// sweep that reports nothing left. The ticker goroutine exists only while at least one guild is tracked.
type guildWatch struct {
	name    string
	every   time.Duration
	sweep   func(guildID string) (pending bool)
	mu      sync.Mutex
	guilds  map[string]uint64 // guild -> Track count, so a Track during a sweep isn't lost to that sweep's "done"
	running bool
}

func newGuildWatch(name string, every time.Duration, sweep func(guildID string) bool) *guildWatch {
	return &guildWatch{name: name, every: every, sweep: sweep, guilds: map[string]uint64{}}
}

// Track starts sweeping guildID if it isn't already.
func (w *guildWatch) Track(guildID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.guilds == nil {
		w.guilds = map[string]uint64{}
	}
	w.guilds[guildID]++
	if !w.running {
		w.running = true
		go w.loop()
	}
}

// Seed tracks every guild that has something pending right now; call it once the bot knows its guilds.
func (w *guildWatch) Seed(guildIDs []string, pending func(guildID string) bool) {
	for _, g := range guildIDs {
		if safePending(w.name, g, pending) {
			w.Track(g)
		}
	}
}

func (w *guildWatch) loop() {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for range t.C {
		w.mu.Lock()
		seen := make(map[string]uint64, len(w.guilds))
		for g, n := range w.guilds {
			seen[g] = n
		}
		w.mu.Unlock()

		for g, n := range seen {
			if safeSweep(w.name, g, w.sweep) {
				continue
			}
			w.mu.Lock()
			if w.guilds[g] == n { // nothing was tracked since this sweep began
				delete(w.guilds, g)
			}
			w.mu.Unlock()
		}

		w.mu.Lock()
		if len(w.guilds) == 0 {
			w.running = false
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()
	}
}

// safeSweep keeps a panic or failure in one guild from stopping the rest; a guild that panicked stays tracked.
func safeSweep(name, guildID string, sweep func(string) bool) (pending bool) {
	pending = true
	defer func() {
		if r := recover(); r != nil {
			appLog.EngineLogger.Error(fmt.Sprintf("[%s] sweep panic in %s: %v", name, guildID, r))
			pending = true
		}
	}()
	return sweep(guildID)
}

func safePending(name, guildID string, pending func(string) bool) bool {
	return safeSweep(name, guildID, pending)
}
