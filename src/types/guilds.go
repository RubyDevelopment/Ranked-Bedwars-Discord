package types

// AppearanceConfig is the guild config's "appearance" section.
type AppearanceConfig struct {
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	Bio      string `json:"bio"`
	Banner   string `json:"banner"`
}

// RegistrationVerification is the guild config's "registration_verification" section.
type RegistrationVerification struct {
	Mode             string `json:"mode"` // database_only, network_api, custom_api, party_code (legacy: manual, disabled)
	NetworkPresetID  string `json:"network_preset_id"`
	CustomProfileURL string `json:"custom_profile_url,omitempty"`
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
