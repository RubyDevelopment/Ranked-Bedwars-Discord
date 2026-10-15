package types

// AppearanceConfig is the legacy appearance-section shape, retained only for
// migrating existing guild configs into DiscordGuildProfile.
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

// DiscordGuildProfile stores synchronized server branding and configured bot
// identity together, in the guild config's "discord_profile" section.
type DiscordGuildProfile struct {
	ServerIconURL     string              `json:"server_icon_url"`
	ServerBannerURL   string              `json:"server_banner_url"`
	MemberCount       int                 `json:"member_count"`
	SyncedAt          string              `json:"synced_at"`
	BotProfileVersion int                 `json:"bot_profile_version,omitempty"`
	Nickname          string              `json:"nickname"`
	BotIconMode       string              `json:"bot_icon_mode"`
	BotIconURL        string              `json:"bot_icon_url"`
	BotBannerMode     string              `json:"bot_banner_mode"`
	BotBannerURL      string              `json:"bot_banner_url"`
	Bio               string              `json:"bio"`
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

// MigrateLegacyAppearance moves bot settings out of the legacy appearance
// section and infers old asset modes without discarding their saved URLs.
func (profile *DiscordGuildProfile) MigrateLegacyAppearance(appearance AppearanceConfig, previous DiscordGuildProfile) {
	if profile.BotIconURL == "" {
		if appearance.BotIconURL != "" {
			profile.BotIconURL = appearance.BotIconURL
		} else if appearance.Avatar != "" {
			profile.BotIconURL = appearance.Avatar
		}
	}
	if profile.BotBannerURL == "" {
		if appearance.BotBannerURL != "" {
			profile.BotBannerURL = appearance.BotBannerURL
		} else if appearance.Banner != "" {
			profile.BotBannerURL = appearance.Banner
		}
	}
	if profile.Nickname == "" {
		profile.Nickname = appearance.Nickname
	}
	if profile.Bio == "" {
		profile.Bio = appearance.Bio
	}
	if profile.BotIconMode == "" {
		if appearance.BotIconMode == "server" || appearance.BotIconMode == "custom" {
			profile.BotIconMode = appearance.BotIconMode
		} else {
			profile.BotIconMode = "custom"
			if profile.BotIconURL == "" ||
				profile.BotIconURL == profile.ServerIconURL ||
				profile.BotIconURL == previous.ServerIconURL {
				profile.BotIconMode = "server"
			}
		}
	}
	if profile.BotBannerMode == "" {
		if appearance.BotBannerMode == "server" || appearance.BotBannerMode == "custom" {
			profile.BotBannerMode = appearance.BotBannerMode
		} else {
			profile.BotBannerMode = "custom"
			if profile.BotBannerURL == "" ||
				profile.BotBannerURL == profile.ServerBannerURL ||
				profile.BotBannerURL == previous.ServerBannerURL {
				profile.BotBannerMode = "server"
			}
		}
	}
	profile.BotProfileVersion = 1
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
