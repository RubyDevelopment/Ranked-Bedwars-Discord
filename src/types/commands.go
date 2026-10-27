package types

import (
	"encoding/json"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/bwmarrin/discordgo"
)

// AccessDecision represents the result of an access control check.
type AccessDecision struct {
	Allowed bool
	Title   string
	Message string
}

// RequirementStatus represents the status of a requirement check.
type RequirementStatus struct {
	Type         RequirementType
	Key          string
	Satisfied    bool
	ErrorMessage string
}

// PrefixCommandRoute represents a resolved prefix command route.
type PrefixCommandRoute struct {
	CommandName    string
	SubcommandPath string
	RemainingArgs  []string
}

// CommandConfig is one entry of the guild config's "commands" map, keyed by
// command name. It's seeded for every registered command on guild setup (see
// EnsureGuildSetup) and edited per-guild from there via /config.
type CommandConfig struct {
	Name            string   `json:"name"`
	Category        string   `json:"category"`
	Description     string   `json:"description"`
	Aliases         []string `json:"aliases"`
	IsEnabled       bool     `json:"is_enabled"`
	IsSlashEnabled  bool     `json:"is_slash_enabled"`
	IsPrefixEnabled bool     `json:"is_prefix_enabled"`

	// Allowed/Denied gate who can invoke the command (checked by checkAccess);
	// Denied wins over Allowed, and an empty Allowed set means "everyone".
	AllowedRoles    map[string]bool `json:"allowed_roles"`
	DeniedRoles     map[string]bool `json:"denied_roles"`
	AllowedChannels map[string]bool `json:"allowed_channels"`
	DeniedChannels  map[string]bool `json:"denied_channels"`

	// Required* hold the actual snowflake ID assigned to each requirement key,
	// independent of who's allowed to invoke the command (see
	// LoadCommandRequirements). A slot with Required=true and an empty ID means
	// the command refuses to run until an admin assigns one.
	RequiredRoles      map[string]RequirementSlot `json:"required_roles"`
	RequiredChannels   map[string]RequirementSlot `json:"required_channels"`
	RequiredCategories map[string]RequirementSlot `json:"required_categories"`

	Options                  []CommandOption `json:"options,omitempty"`
	Cooldown                 int             `json:"cooldown,omitempty"`
	DefaultMemberPermissions *int64          `json:"default_member_permissions,string,omitempty"`

	// Builder is the panel's custom-command definition (options, steps and
	// reply). It is carried verbatim so saving the commands map never drops it.
	Builder json.RawMessage `json:"builder,omitempty"`

	// Responses are the built-in answers of the command, as the bot wrote them: what the dashboard's Responses tab lists to
	// be restyled. The bot keeps them in step with its own code each time it syncs the commands (SeedCommandConfigs).
	Responses []CommandResponse `json:"responses,omitempty"`

	// Layouts are the pieces of text the command builds its lists and rows from (a bot's line, a queue's heading), as the
	// bot wrote them: the dashboard lets a guild rewrite each one. The guild's own versions live in panels.responses.layouts.
	Layouts []CommandLayout `json:"layouts,omitempty"`
}

// CommandLayout is one rewritable piece of text of a command: its key (command.name), a label, the default text with {var}
// placeholders, and the variables it can use. Repeats means it is written once for every row of a list.
type CommandLayout struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Default string   `json:"default"`
	Vars    []string `json:"vars,omitempty"`
	Repeats bool     `json:"repeats,omitempty"`
}

// CommandResponse is one built-in answer of a command (see scripts/gen_responses): its key (command.title-slug), title,
// look (success, error, caution, info, loading or default) and description, in which each part the command fills in
// (a name, a number) is shown as "…".
type CommandResponse struct {
	Key         string `json:"key"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Look        string `json:"look"`
	Description string `json:"description,omitempty"`
	HasFields   bool   `json:"has_fields,omitempty"` // the embed also has fields
}

// RequirementSlot is one resolved (or unresolved) requirement key on a
// CommandConfig: ID is the snowflake assigned to it (empty if not yet
// configured), and Required says whether the command needs it set to run at
// all versus it just being an optional, available slot.
type RequirementSlot struct {
	ID       string `json:"id"`
	Required bool   `json:"required"`
}

// CommandOption is a parameter of a CommandConfig.
type CommandOption struct {
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Description string          `json:"description"`
	Required    bool            `json:"required"`
	Choices     []CommandChoice `json:"choices,omitempty"`
}

// CommandChoice is a predefined choice for a CommandOption.
type CommandChoice struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// ComponentHandler handles a component interaction and reports whether the
// customID was one of its own (true stops further routing).
type ComponentHandler func(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool

// GuildContext is one command invocation's snapshot of the database: the
// guild row, whether it counts as registered, and its derived settings. Load
// it once per invocation with LoadGuildContext and thread it through, rather
// than having each pipeline stage fetch the guild and its config again itself.
type GuildContext struct {
	Guild *models.GuildsModel // nil unless the guild was actually found

	// Registered is false only when the guild definitively isn't registered
	// (a 404). Any other lookup failure (the API briefly unavailable) leaves
	// it true, so a transient error can never look like an unregistered guild
	// to a command's caller.
	Registered bool

	Settings GuildSettings
}

// PipelineOptions configures command execution pipeline behavior.
type PipelineOptions struct {
	SkipGuildRegistration bool
}

// UnmarshalJSON reads a command's settings the way the dashboard shows them: a switch missing from the stored entry is on.
// Without this a partly saved entry decodes to every switch off, and the bot refuses a command the dashboard shows enabled.
func (c *CommandConfig) UnmarshalJSON(data []byte) error {
	type plain CommandConfig
	p := plain{IsEnabled: true, IsSlashEnabled: true, IsPrefixEnabled: true}
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*c = CommandConfig(p)
	return nil
}
