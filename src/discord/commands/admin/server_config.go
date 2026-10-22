package admin

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

var adminPermissions int64 = discordgo.PermissionAdministrator

func init() {
	helpers.Register(ServerConfigCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerConfigCommand manages guild configuration: setup/sync, per-command
// requirement assignments (stored on that command's own entry in the guild
// config's "commands" column — see types.CommandConfig), and bot settings.
var ServerConfigCommand = &types.Command{
	Name:        "config",
	Description: "Configures guild settings, command requirements, and bot behavior.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data:        buildServerConfigData(),
	Execute:     executeServerConfigCommand,
}

func buildServerConfigData() *discordgo.ApplicationCommand {
	textChannelTypes := []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews}
	categoryTypes := []discordgo.ChannelType{discordgo.ChannelTypeGuildCategory}

	return &discordgo.ApplicationCommand{
		DefaultMemberPermissions: &adminPermissions,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "setup",
				Description: "Initialize this guild with Ranked Bedwars (also seeds every command's requirement keys)",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "sync",
				Description: "Force sync this guild's data and snapshot",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "role",
				Description: "Assign a role to one of a command's requirement keys",
				Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
				Options:     requirementSubcommandOptions(discordgo.ApplicationCommandOptionRole, "role", nil),
			},
			{
				Name:        "channel",
				Description: "Assign a channel to one of a command's requirement keys",
				Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
				Options:     requirementSubcommandOptions(discordgo.ApplicationCommandOptionChannel, "channel", textChannelTypes),
			},
			{
				Name:        "category",
				Description: "Assign a category to one of a command's requirement keys",
				Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
				Options:     requirementSubcommandOptions(discordgo.ApplicationCommandOptionChannel, "category", categoryTypes),
			},
			{
				Name:        "bot",
				Description: "Bot and command settings",
				Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "view", Description: "View bot settings", Type: discordgo.ApplicationCommandOptionSubCommand},
					{
						Name:        "prefix",
						Description: "Set the command prefix",
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Options: []*discordgo.ApplicationCommandOption{
							{Name: "prefix", Description: "Prefix (max 5 characters)", Type: discordgo.ApplicationCommandOptionString, Required: true, MaxLength: 5},
						},
					},
					{
						Name:        "toggle",
						Description: "Toggle prefix or slash commands",
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Options: []*discordgo.ApplicationCommandOption{
							{
								Name:        "target",
								Description: "Which command mode to toggle",
								Type:        discordgo.ApplicationCommandOptionString,
								Required:    true,
								Choices: []*discordgo.ApplicationCommandOptionChoice{
									{Name: "Prefix", Value: "prefix"},
									{Name: "Slash", Value: "slash"},
								},
							},
							{Name: "enabled", Description: "Whether the mode should be enabled", Type: discordgo.ApplicationCommandOptionBoolean, Required: true},
						},
					},
				},
			},
		},
	}
}

// requirementSubcommandOptions builds the list/set/get/clear subcommands
// shared by the role/channel/category groups. valueName/valueType/channelTypes
// describe the "set" subcommand's value option (a role, or a channel narrowed
// to text channels or categories).
func requirementSubcommandOptions(valueType discordgo.ApplicationCommandOptionType, valueName string, channelTypes []discordgo.ChannelType) []*discordgo.ApplicationCommandOption {
	commandOption := &discordgo.ApplicationCommandOption{Name: "command", Description: "Command name (e.g. server_mute)", Type: discordgo.ApplicationCommandOptionString, Required: true}
	keyOption := &discordgo.ApplicationCommandOption{Name: "key", Description: "Requirement key declared by that command", Type: discordgo.ApplicationCommandOptionString, Required: true}
	valueOption := &discordgo.ApplicationCommandOption{Name: valueName, Description: "Value to assign", Type: valueType, Required: true, ChannelTypes: channelTypes}

	return []*discordgo.ApplicationCommandOption{
		{Name: "list", Description: "List a command's requirement keys", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{commandOption}},
		{Name: "set", Description: "Assign a value to one of a command's requirement keys", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{commandOption, keyOption, valueOption}},
		{Name: "get", Description: "Get a command's requirement key value", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{commandOption, keyOption}},
		{Name: "clear", Description: "Clear a command's requirement key value", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandOption{commandOption, keyOption}},
	}
}

// ============================================================================
// Command Execution
// ============================================================================

func executeServerConfigCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !helpers.MemberIsAdministrator(s, i) {
		return helpers.EditInteractionEmbed(s, i, helpers.AccessDeniedEmbed(
			"Permission Denied",
			"Only administrators can use `/config`.",
		))
	}

	group, sub, options := selectedConfigRoute(i)
	switch group {
	case "setup":
		return executeGuildSetup(s, i)
	case "sync":
		return executeGuildSync(s, i)
	case "role":
		return executeRequirementCommand(s, i, types.ReqRole, sub, options)
	case "channel":
		return executeRequirementCommand(s, i, types.ReqChannel, sub, options)
	case "category":
		return executeRequirementCommand(s, i, types.ReqCategory, sub, options)
	case "bot":
		return executeConfigBotCommand(s, i, sub, options)
	default:
		return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigUnknownRouteEmbed(i, group, sub))
	}
}

func executeRequirementCommand(s *discordgo.Session, i *discordgo.InteractionCreate, resourceType types.RequirementType, sub string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	cmdName := strings.ToLower(strings.TrimSpace(helpers.SubOptionString(options, "command")))
	if findRegisteredCommand(cmdName) == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigUnknownCommandEmbed(i, cmdName))
	}

	switch sub {
	case "list":
		slots, found, err := helpers.CommandRequirementSlots(i.GuildID, cmdName, resourceType)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorServerConfigEmbed(i))
		}
		if !found {
			return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigCommandNotSetUpEmbed(i, cmdName))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigRequirementListEmbed(i, resourceType, cmdName, slots))

	case "set":
		key := helpers.SubOptionString(options, "key")
		if key == "" {
			return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigInvalidKeyEmbed(i))
		}
		slots, found, err := helpers.CommandRequirementSlots(i.GuildID, cmdName, resourceType)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorServerConfigEmbed(i))
		}
		if !found {
			return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigCommandNotSetUpEmbed(i, cmdName))
		}
		if _, ok := slots[key]; !ok {
			return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigUnknownRequirementKeyEmbed(i, cmdName, key, slots))
		}

		id, label := resolveRequirementValue(s, i, options, resourceType)
		if id == "" {
			return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigMissingValueEmbed(i, string(resourceType)))
		}

		if err := helpers.SetRequirementSlotID(helpers.InteractionUserID(i), i.GuildID, cmdName, resourceType, key, id); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorServerConfigEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigRequirementSetEmbed(i, resourceType, cmdName, key, label))

	case "get":
		key := helpers.SubOptionString(options, "key")
		slots, found, err := helpers.CommandRequirementSlots(i.GuildID, cmdName, resourceType)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorServerConfigEmbed(i))
		}
		if !found {
			return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigCommandNotSetUpEmbed(i, cmdName))
		}
		slot, ok := slots[key]
		if !ok {
			return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigUnknownRequirementKeyEmbed(i, cmdName, key, slots))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigRequirementGetEmbed(i, resourceType, cmdName, key, slot))

	case "clear":
		key := helpers.SubOptionString(options, "key")
		slots, found, err := helpers.CommandRequirementSlots(i.GuildID, cmdName, resourceType)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorServerConfigEmbed(i))
		}
		if !found {
			return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigCommandNotSetUpEmbed(i, cmdName))
		}
		if _, ok := slots[key]; !ok {
			return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigUnknownRequirementKeyEmbed(i, cmdName, key, slots))
		}
		if err := helpers.SetRequirementSlotID(helpers.InteractionUserID(i), i.GuildID, cmdName, resourceType, key, ""); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorServerConfigEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigRequirementClearedEmbed(i, resourceType, cmdName, key))

	default:
		return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigUnknownRouteEmbed(i, string(resourceType), sub))
	}
}

func executeConfigBotCommand(s *discordgo.Session, i *discordgo.InteractionCreate, sub string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	actor := helpers.InteractionUserID(i)
	switch sub {
	case "view":
		config, err := database.Must().GuildConfig.Get(i.GuildID)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorServerConfigEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigBotViewEmbed(i, config.Prefix, config.IsPrefixEnabled, config.IsSlashEnabled))
	case "prefix":
		prefix := helpers.SubOptionString(options, "prefix")
		if n := utf8.RuneCountInString(prefix); n < 1 || n > 5 {
			return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigInvalidPrefixEmbed(i))
		}
		if err := database.Must().GuildConfig.SaveField(actor, i.GuildID, "prefix", prefix); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorServerConfigEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigPrefixUpdatedEmbed(i, prefix))
	case "toggle":
		target := helpers.SubOptionString(options, "target")
		enabled := helpers.SubOptionBool(options, "enabled")
		switch target {
		case "prefix":
			if err := database.Must().GuildConfig.SaveField(actor, i.GuildID, "is_prefix_enabled", enabled); err != nil {
				return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorServerConfigEmbed(i))
			}
		case "slash":
			if err := database.Must().GuildConfig.SaveField(actor, i.GuildID, "is_slash_enabled", enabled); err != nil {
				return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorServerConfigEmbed(i))
			}
		default:
			return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigUnknownRouteEmbed(i, "bot", sub))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigBotToggleEmbed(i, target, enabled))
	default:
		return helpers.EditInteractionEmbed(s, i, helpers.ServerConfigUnknownRouteEmbed(i, "bot", sub))
	}
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// resolveRequirementValue reads the "set" subcommand's value option (role,
// channel or category, per resourceType) and returns its snowflake ID plus a
// display mention for the confirmation embed. Empty id means the option
// wasn't actually provided/resolved.
func resolveRequirementValue(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption, resourceType types.RequirementType) (id, label string) {
	switch resourceType {
	case types.ReqRole:
		role := subOptionRole(s, i, options, "role")
		if role == nil {
			return "", ""
		}
		return role.ID, role.Mention()
	case types.ReqChannel:
		channel := subOptionChannel(s, options, "channel")
		if channel == nil {
			return "", ""
		}
		return channel.ID, fmt.Sprintf("<#%s>", channel.ID)
	case types.ReqCategory:
		category := subOptionChannel(s, options, "category")
		if category == nil {
			return "", ""
		}
		return category.ID, fmt.Sprintf("<#%s>", category.ID)
	default:
		return "", ""
	}
}

func findRegisteredCommand(name string) *types.Command {
	for _, cmd := range helpers.CommandRegistry {
		if cmd != nil && cmd.Name == name {
			return cmd
		}
	}
	return nil
}

func subOptionRole(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption, name string) *discordgo.Role {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionRole {
			return opt.RoleValue(s, i.GuildID)
		}
	}
	return nil
}

func subOptionChannel(s *discordgo.Session, options []*discordgo.ApplicationCommandInteractionDataOption, name string) *discordgo.Channel {
	for _, opt := range options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionChannel {
			return opt.ChannelValue(s)
		}
	}
	return nil
}

func selectedConfigRoute(i *discordgo.InteractionCreate) (group, sub string, options []*discordgo.ApplicationCommandInteractionDataOption) {
	top := i.ApplicationCommandData().Options
	if len(top) == 0 {
		return "", "", nil
	}
	if top[0].Type == discordgo.ApplicationCommandOptionSubCommandGroup {
		group = top[0].Name
		if len(top[0].Options) == 0 {
			return group, "", nil
		}
		sub = top[0].Options[0].Name
		options = top[0].Options[0].Options
		return group, sub, options
	}
	if top[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		// A top-level subcommand (setup, sync) is routed by its own name.
		return top[0].Name, "", top[0].Options
	}
	return "", "", nil
}
