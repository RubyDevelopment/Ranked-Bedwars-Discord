package types

// AppearanceConfig stores bot-specific branding overrides in the guild config's "appearance" section.
// Empty bot asset URLs use the corresponding server asset from discord_profile.
type AppearanceConfig struct {
	Nickname      string `json:"nickname"`
	BotIconMode   string `json:"bot_icon_mode,omitempty"`
	BotIconURL    string `json:"bot_icon_url,omitempty"`
	BotBannerMode string `json:"bot_banner_mode,omitempty"`
	BotBannerURL  string `json:"bot_banner_url,omitempty"`
	Bio           string `json:"bio"`
	Avatar        string `json:"avatar,omitempty"`
	Banner        string `json:"banner,omitempty"`
}

// DiscordGuildProfile stores the server's synchronized branding separately from
// the bot-specific overrides in AppearanceConfig.
type DiscordGuildProfile struct {
	ServerIconURL     string              `json:"server_icon_url"`
	ServerBannerURL   string              `json:"server_banner_url"`
	MemberCount       int                 `json:"member_count"`
	SyncedAt          string              `json:"synced_at"`
	AppliedAppearance SyncedBotAppearance `json:"applied_appearance,omitempty"`
	BlockedAvatarURL  string              `json:"blocked_avatar_url,omitempty"`
	BlockedBannerURL  string              `json:"blocked_banner_url,omitempty"`
	IconURL           string              `json:"icon_url,omitempty"`
	BannerURL         string              `json:"banner_url,omitempty"`
}

// SyncedBotAppearance records the bot appearance values successfully applied
// to Discord so unchanged values do not trigger another REST request.
type SyncedBotAppearance struct {
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	Banner   string `json:"banner"`
	Bio      string `json:"bio"`
}

// ShouldApplyAppearance reports whether desired differs from applied and is not
// blocked after a permanent asset-not-found response.
func ShouldApplyAppearance(desired, applied string, blocked bool) bool {
	return desired != applied && !blocked
}

// NormalizeLegacy migrates old asset keys without treating a saved server default
// as a bot-specific override.
func (profile *DiscordGuildProfile) NormalizeLegacy() bool {
	changed := false
	if profile.ServerIconURL == "" && profile.IconURL != "" {
		profile.ServerIconURL = profile.IconURL
	}
	if profile.ServerBannerURL == "" && profile.BannerURL != "" {
		profile.ServerBannerURL = profile.BannerURL
	}
	if profile.IconURL != "" {
		profile.IconURL = ""
		changed = true
	}
	if profile.BannerURL != "" {
		profile.BannerURL = ""
		changed = true
	}
	return changed
}

// NormalizeLegacy migrates old avatar/banner keys and drops values that were
// copied from either the previous or current server profile.
func (appearance *AppearanceConfig) NormalizeLegacy(current, previous DiscordGuildProfile) bool {
	changed := false
	if appearance.BotIconURL == "" && appearance.Avatar != "" {
		appearance.BotIconURL = appearance.Avatar
		changed = true
	}
	if appearance.BotBannerURL == "" && appearance.Banner != "" {
		appearance.BotBannerURL = appearance.Banner
		changed = true
	}
	if appearance.Avatar != "" {
		appearance.Avatar = ""
		changed = true
	}
	if appearance.Banner != "" {
		appearance.Banner = ""
		changed = true
	}
	if appearance.BotIconMode == "" {
		appearance.BotIconMode = "custom"
		if appearance.BotIconURL == "" ||
			appearance.BotIconURL == current.ServerIconURL ||
			appearance.BotIconURL == previous.ServerIconURL {
			appearance.BotIconMode = "server"
		}
		changed = true
	}
	if appearance.BotBannerMode == "" {
		appearance.BotBannerMode = "custom"
		if appearance.BotBannerURL == "" ||
			appearance.BotBannerURL == current.ServerBannerURL ||
			appearance.BotBannerURL == previous.ServerBannerURL {
			appearance.BotBannerMode = "server"
		}
		changed = true
	}
	return changed
}

// RegistrationVerification is the guild config's "registration_verification" section.
type RegistrationVerification struct {
	Mode             string       `json:"mode"` // database_only, network_api, custom_api, party_code (legacy: manual, disabled)
	NetworkPresetID  string       `json:"network_preset_id"`
	CustomProfileURL string       `json:"custom_profile_url,omitempty"`
	ExternalAPI      *ExternalAPI `json:"external_api,omitempty"`
}

// ExternalAPI configuration for custom_api verification mode.
type ExternalAPI struct {
	EndpointURL    string `json:"endpoint_url"`     // Base API endpoint URL
	APIKey         string `json:"api_key"`          // API key or token
	AuthType       string `json:"auth_type"`        // bearer or custom_header
	HeaderName     string `json:"header_name"`      // Custom header name if auth_type is custom_header
	UUIDFieldPath  string `json:"uuid_field_path"`  // JSON field path to extract UUID (e.g., "player.uuid")
	QueryParam     string `json:"query_param"`      // Parameter name for username/UUID in request
	QueryParamType string `json:"query_param_type"` // uuid or username
}

// DiscordChannel is one entry of the guild state's "channels" map.
type DiscordChannel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     int    `json:"type"` // 0=text, 2=voice, 4=category, 5=announcement, 11=thread, 15=forum
	ParentID string `json:"parent_id,omitempty"`
	Position int    `json:"position"`
}

// DiscordCategory is one entry of the guild state's "categories" map.
type DiscordCategory struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     int    `json:"type"`
	ParentID string `json:"parent_id,omitempty"`
	Position int    `json:"position"`
}

// DiscordRole is one entry of the guild state's "roles" map.
type DiscordRole struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Color    int    `json:"color"`
	Position int    `json:"position"`
}

// DiscordThread is one entry of the guild state's "threads" map.
type DiscordThread struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     int    `json:"type"`
	ParentID string `json:"parent_id,omitempty"`
	Position *int   `json:"position,omitempty"`
	Archived bool   `json:"archived"`
}

// GuildState represents the synchronized state of a guild's roles and channels.
type GuildState struct {
	Roles      map[string]DiscordRole     `json:"roles"`
	Channels   map[string]DiscordChannel  `json:"channels"`
	Categories map[string]DiscordCategory `json:"categories,omitempty"`
	Threads    map[string]DiscordThread   `json:"threads,omitempty"`
}

// GuildSettings represents guild-specific settings for command handling.
type GuildSettings struct {
	Prefix        string
	PrefixEnabled bool
	SlashEnabled  bool
	Commands      GuildCommandsMap
}

// GuildCommandPermission represents command permission settings.
type GuildCommandPermission struct {
	Disabled        *bool
	SlashDisabled   *bool
	PrefixDisabled  *bool
	Aliases         []string
	Cooldown        int
	AllowedChannels []string
	DeniedChannels  []string
	AllowedRoles    []string
	DeniedRoles     []string
	Subcommands     map[string]GuildSubcommandPermission

	// Custom marks a command made on the dashboard's Command Builder; StaffOnly is set when it declares the staff role
	// requirement, which gates it like a built-in staff command.
	Custom    bool
	StaffOnly bool

	// Required* mirror the guild config's own required-resource maps for this
	// command (see CommandConfig), so LoadCommandRequirements can tell which
	// keys are missing their snowflake ID from the single GuildSettings load
	// instead of a config fetch of its own.
	RequiredChannels   map[string]RequirementSlot
	RequiredRoles      map[string]RequirementSlot
	RequiredCategories map[string]RequirementSlot
}

// GuildSubcommandPermission represents subcommand permission settings.
type GuildSubcommandPermission struct {
	Disabled        *bool
	SlashDisabled   *bool
	PrefixDisabled  *bool
	Aliases         []string
	AllowedChannels []string
	AllowedRoles    []string
}

// GuildCommandsMap maps command names to their permissions.
type GuildCommandsMap map[string]GuildCommandPermission

// SetupResult reports which pieces were created during guild setup.
type SetupResult struct {
	GuildCreated      bool
	ConfigCreated     bool
	SnapshotCreated   bool
	SnapshotSynced    bool
	MetaCreated       bool
	GameConfigCreated bool
}

// LFGConfig holds the guild-side LFG posting settings, persisted in the
// guild config's own `lfg` section.
type LFGConfig struct {
	LFGChannelID string `json:"lfg_channel_id"`
	LFGRoleID    string `json:"lfg_role_id"`
	QueuesPaused bool   `json:"queues_paused"`
}
