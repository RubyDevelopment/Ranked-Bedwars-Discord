package types

import (
	"time"

	"github.com/bwmarrin/discordgo"
)

// CooldownScope defines whether a cooldown is tracked per user, channel, or globally.
type CooldownScope string

const (
	CooldownUser    CooldownScope = "user"
	CooldownChannel CooldownScope = "channel"
	CooldownGuild   CooldownScope = "guild"
	CooldownGlobal  CooldownScope = "global"
)

// Command defines the execution structure, checks, metadata, and customization hooks for a command.
type Command struct {
	Name          string
	Description   string
	Category      string // Set automatically by the registry from package folder if empty
	Disabled      bool
	Ephemeral     bool
	Cooldown      time.Duration
	CooldownScope CooldownScope
	SlashAliases  []string
	PrefixAliases []string

	// Declarative subcommands (automatically translated into Data.Options by the registry)
	Subcommands []SlashSubcommand

	// Discord Native Application Command Data (Explicit configuration for slash options, context menus, permissions)
	Data *discordgo.ApplicationCommand

	// Discord Native Permission & Access Control Overrides
	DefaultMemberPermissions *int64
	DMPermission             *bool

	// Checks & Custom Guards
	SkipGuildCheck bool
	CustomCheck    func(s *discordgo.Session, i *discordgo.InteractionCreate) (passed bool, failMessage string)
	Requirements   []Requirement

	// Execution Handlers
	Execute      func(s *discordgo.Session, i *discordgo.InteractionCreate) error
	Autocomplete func(s *discordgo.Session, i *discordgo.InteractionCreate)
}

// SlashOption describes options for subcommands cleanly.
type SlashOption struct {
	Name         string
	Description  string
	Type         discordgo.ApplicationCommandOptionType
	Required     bool
	Choices      []*discordgo.ApplicationCommandOptionChoice
	Autocomplete bool
}

// SlashSubcommand is a declarative subcommand definition.
type SlashSubcommand struct {
	Name        string
	Description string
	Options     []SlashOption
	Run         func(s *discordgo.Session, i *discordgo.InteractionCreate) error
}

// Sub creates a subcommand with optional options.
func Sub(
	name, description string,
	run func(s *discordgo.Session, i *discordgo.InteractionCreate) error,
	options ...SlashOption,
) SlashSubcommand {
	return SlashSubcommand{
		Name:        name,
		Description: description,
		Options:     options,
		Run:         run,
	}
}

// SelectedSubcommand extracts the invoked subcommand name and its options from an interaction.
func SelectedSubcommand(i *discordgo.InteractionCreate) (name string, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if i == nil {
		return "", nil
	}
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		return "", nil
	}
	return opts[0].Name, opts[0].Options
}

// Event defines the gateway listener structure for Discord or internal events.
type Event struct {
	Name         string
	Description  string
	Type         string
	Once         bool
	Disabled     bool
	System       bool
	Requirements []Requirement
	Execute      any
}

// RequirementType defines the scope for required resources or permissions.
type RequirementType string

const (
	ReqRole        RequirementType = "role"
	ReqChannel     RequirementType = "channel"
	ReqCategory    RequirementType = "category"
	ReqThread      RequirementType = "thread"
	ReqPermission  RequirementType = "permission"
	ReqFeature     RequirementType = "feature"
	ReqCustomGuard RequirementType = "custom"
)

// Requirement defines permission or resource checks for commands and events.
type Requirement struct {
	Type         RequirementType `json:"type"`
	Key          string          `json:"key"`
	Optional     bool            `json:"optional"`
	ErrorMessage string          `json:"error_message"`
	// Grants marks a role the command hands out or manages (muted, registered) rather than one members must hold: it is
	// configuration only and is never checked against the member running the command.
	Grants   bool                                                            `json:"-"`
	Validate func(s *discordgo.Session, i *discordgo.InteractionCreate) bool `json:"-"`
}

// StaffCommandLogsKey is the optional channel requirement every staff command shares: each successful use is logged there.
const StaffCommandLogsKey = "staff_command_logs"

// ModerationAlertsKey is the optional channel requirement for punishment alerts (expiry, enforcement).
const ModerationAlertsKey = "moderation_alerts"

// ModerationRequirements is declared by every moderation command. Requirements are identified by type and key, so one
// assignment of each (/server_config channel|role set) applies to all of them.
var ModerationRequirements = []Requirement{
	{Type: ReqChannel, Key: StaffCommandLogsKey, Optional: true},
	{Type: ReqChannel, Key: ModerationAlertsKey, Optional: true},
	{Type: ReqRole, Key: "staff_role"},
}

// ModerationRequirementsWithRole is ModerationRequirements plus the role the command hands out and takes back
// (chat_muted_role, vc_muted_role, queue_banned_role). Its pair command declares the same key, so they share one role.
func ModerationRequirementsWithRole(roleKey string) []Requirement {
	return append(append([]Requirement{}, ModerationRequirements...), Requirement{Type: ReqRole, Key: roleKey, Grants: true})
}

type Embed struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Blend       bool   `json:"blend"` // no side colour: the bar takes the embed's own background colour
	Author      struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		IconURL string `json:"icon_url"`
	} `json:"author"`
	Footer struct {
		Text    string `json:"text"`
		IconURL string `json:"icon_url"`
	} `json:"footer"`
	Image     string `json:"image"`
	Thumbnail string `json:"thumbnail"`
	Timestamp bool   `json:"timestamp"`
	Fields    []struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Inline bool   `json:"inline"`
	} `json:"fields"`
}

// HistoryPageKind identifies which history view a pagination button belongs to.
type HistoryPageKind string

type LinkButton struct {
	Label    string `json:"label"`
	URL      string `json:"url"`
	Emoji    string `json:"emoji"`    // a unicode emoji before the label
	Disabled bool   `json:"disabled"` // greyed out
}

// RoleHierarchyIssue describes why a role mutation is not allowed.
type RoleHierarchyIssue string

// V2Item is one Components V2 item: a container (with Children) or one of its leaves. Only the fields of its Type are used.
type V2Item struct {
	Type      string   `json:"type"`
	Content   string   `json:"content"`
	Texts     []string `json:"texts"`
	Accessory struct {
		Kind        string `json:"kind"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Label       string `json:"label"`
		Emoji       string `json:"emoji"`
		Disabled    bool   `json:"disabled"`
		Spoiler     bool   `json:"spoiler"`
	} `json:"accessory"`
	Items []struct {
		URL         string `json:"url"`
		Description string `json:"description"`
		Spoiler     bool   `json:"spoiler"`
	} `json:"items"`
	Divider  *bool        `json:"divider"`
	Spacing  int          `json:"spacing"`
	Buttons  []LinkButton `json:"buttons"`
	Accent   string       `json:"accent"`
	Spoiler  bool         `json:"spoiler"`
	Children []V2Item     `json:"children"`
}

// Role requirement keys shared by command groups. Requirements are identified by type and key, so one assignment of each
// (/server_config role set) applies to every command that declares it.
const (
	StaffRoleKey      = "staff_role"      // members allowed to run staff commands (they must hold it)
	RegisteredRoleKey = "registered_role" // members who have registered (they must hold it to use general commands)
)

// StaffRequirements is declared by every staff command: only members holding the staff role may run it, and each use is
// logged to the optional staff command logs channel.
var StaffRequirements = []Requirement{
	{Type: ReqRole, Key: StaffRoleKey},
	{Type: ReqChannel, Key: StaffCommandLogsKey, Optional: true},
}

// PunishmentRequirements is declared by a punishment command and by its reversal alike (ban and unban, chat mute and
// unmute, ...): the staff role, the punishment's own log channel and the roles it hands out and takes back. Requirements
// are identified by type and key, so one assignment of each applies to every command that declares it. Strikes pass the
// queue-banned role as well, as they queue-ban.
func PunishmentRequirements(logsKey string, roleKeys ...string) []Requirement {
	reqs := append(append([]Requirement{}, StaffRequirements...), Requirement{Type: ReqChannel, Key: logsKey, Optional: true})
	for _, key := range roleKeys {
		reqs = append(reqs, Requirement{Type: ReqRole, Key: key, Grants: true})
	}
	return reqs
}

// GeneralRequirements is declared by every general (player facing) command: anyone may use it, but only registered members.
var GeneralRequirements = []Requirement{
	{Type: ReqRole, Key: RegisteredRoleKey},
}

// HasRequirementKey reports whether the list declares a requirement of the given type and key.
func HasRequirementKey(reqs []Requirement, t RequirementType, key string) bool {
	for _, r := range reqs {
		if r.Type == t && r.Key == key {
			return true
		}
	}
	return false
}

// RegistrationRequirements is declared by the commands that register, rename or sync a player: they manage the
// registered role, so it is configured for them but a member does not need to hold it to run them.
var RegistrationRequirements = []Requirement{
	{Type: ReqRole, Key: RegisteredRoleKey, Grants: true},
}

// RequiresRole reports whether the list makes members hold the role of this key (a declared role that is not Grants).
func RequiresRole(reqs []Requirement, key string) bool {
	for _, r := range reqs {
		if r.Type == ReqRole && r.Key == key && !r.Grants {
			return true
		}
	}
	return false
}

// StaffRegistrationRequirements is StaffRequirements plus the registered role the command manages.
func StaffRegistrationRequirements() []Requirement {
	return append(append([]Requirement{}, StaffRequirements...), RegistrationRequirements...)
}
