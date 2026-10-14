package admin

import (
	"fmt"
	"sort"
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
	Name:        "server_config",
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
			"Only administrators can use `/server_config`.",
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
		return helpers.EditInteractionEmbed(s, i, serverConfigUnknownRouteEmbed(group, sub))
	}
}

func executeRequirementCommand(s *discordgo.Session, i *discordgo.InteractionCreate, resourceType types.RequirementType, sub string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	cmdName := strings.ToLower(strings.TrimSpace(helpers.SubOptionString(options, "command")))
	if findRegisteredCommand(cmdName) == nil {
		return helpers.EditInteractionEmbed(s, i, serverConfigUnknownCommandEmbed(cmdName))
	}

	switch sub {
	case "list":
		slots, found, err := helpers.CommandRequirementSlots(i.GuildID, cmdName, resourceType)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerConfigEmbed())
		}
		if !found {
			return helpers.EditInteractionEmbed(s, i, serverConfigCommandNotSetUpEmbed(cmdName))
		}
		return helpers.EditInteractionEmbed(s, i, serverConfigRequirementListEmbed(resourceType, cmdName, slots))

	case "set":
		key := helpers.SubOptionString(options, "key")
		if key == "" {
			return helpers.EditInteractionEmbed(s, i, serverConfigInvalidKeyEmbed())
		}
		slots, found, err := helpers.CommandRequirementSlots(i.GuildID, cmdName, resourceType)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerConfigEmbed())
		}
		if !found {
			return helpers.EditInteractionEmbed(s, i, serverConfigCommandNotSetUpEmbed(cmdName))
		}
		if _, ok := slots[key]; !ok {
			return helpers.EditInteractionEmbed(s, i, serverConfigUnknownRequirementKeyEmbed(cmdName, key, slots))
		}

		id, label := resolveRequirementValue(s, i, options, resourceType)
		if id == "" {
			return helpers.EditInteractionEmbed(s, i, serverConfigMissingValueEmbed(string(resourceType)))
		}

		if err := helpers.SetRequirementSlotID(helpers.InteractionUserID(i), i.GuildID, cmdName, resourceType, key, id); err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerConfigEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, serverConfigRequirementSetEmbed(resourceType, cmdName, key, label))

	case "get":
		key := helpers.SubOptionString(options, "key")
		slots, found, err := helpers.CommandRequirementSlots(i.GuildID, cmdName, resourceType)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerConfigEmbed())
		}
		if !found {
			return helpers.EditInteractionEmbed(s, i, serverConfigCommandNotSetUpEmbed(cmdName))
		}
		slot, ok := slots[key]
		if !ok {
			return helpers.EditInteractionEmbed(s, i, serverConfigUnknownRequirementKeyEmbed(cmdName, key, slots))
		}
		return helpers.EditInteractionEmbed(s, i, serverConfigRequirementGetEmbed(resourceType, cmdName, key, slot))

	case "clear":
		key := helpers.SubOptionString(options, "key")
		slots, found, err := helpers.CommandRequirementSlots(i.GuildID, cmdName, resourceType)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerConfigEmbed())
		}
		if !found {
			return helpers.EditInteractionEmbed(s, i, serverConfigCommandNotSetUpEmbed(cmdName))
		}
		if _, ok := slots[key]; !ok {
			return helpers.EditInteractionEmbed(s, i, serverConfigUnknownRequirementKeyEmbed(cmdName, key, slots))
		}
		if err := helpers.SetRequirementSlotID(helpers.InteractionUserID(i), i.GuildID, cmdName, resourceType, key, ""); err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerConfigEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, serverConfigRequirementClearedEmbed(resourceType, cmdName, key))

	default:
		return helpers.EditInteractionEmbed(s, i, serverConfigUnknownRouteEmbed(string(resourceType), sub))
	}
}

func executeConfigBotCommand(s *discordgo.Session, i *discordgo.InteractionCreate, sub string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	actor := helpers.InteractionUserID(i)
	switch sub {
	case "view":
		config, err := database.Must().GuildConfig.Get(i.GuildID)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerConfigEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, serverConfigBotViewEmbed(config.Prefix, config.IsPrefixEnabled, config.IsSlashEnabled))
	case "prefix":
		prefix := helpers.SubOptionString(options, "prefix")
		if n := utf8.RuneCountInString(prefix); n < 1 || n > 5 {
			return helpers.EditInteractionEmbed(s, i, serverConfigInvalidPrefixEmbed())
		}
		if _, err := database.Must().GuildConfig.SaveField(actor, i.GuildID, "prefix", prefix); err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerConfigEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, serverConfigPrefixUpdatedEmbed(prefix))
	case "toggle":
		target := helpers.SubOptionString(options, "target")
		enabled := helpers.SubOptionBool(options, "enabled")
		switch target {
		case "prefix":
			if _, err := database.Must().GuildConfig.SaveField(actor, i.GuildID, "is_prefix_enabled", enabled); err != nil {
				return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerConfigEmbed())
			}
		case "slash":
			if _, err := database.Must().GuildConfig.SaveField(actor, i.GuildID, "is_slash_enabled", enabled); err != nil {
				return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerConfigEmbed())
			}
		default:
			return helpers.EditInteractionEmbed(s, i, serverConfigUnknownRouteEmbed("bot", sub))
		}
		return helpers.EditInteractionEmbed(s, i, serverConfigBotToggleEmbed(target, enabled))
	default:
		return helpers.EditInteractionEmbed(s, i, serverConfigUnknownRouteEmbed("bot", sub))
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

func requirementMention(resourceType types.RequirementType, id string) string {
	if resourceType == types.ReqRole {
		return fmt.Sprintf("<@&%s>", id)
	}
	return fmt.Sprintf("<#%s>", id)
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}

func toggleLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorServerConfigEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to update the configuration for this guild. Please contact an administrator for assistance or try again later.",
	})
}

func serverConfigUnknownCommandEmbed(name string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unknown Command",
		Description: fmt.Sprintf("No registered command named **%s**.", name),
	})
}

func serverConfigCommandNotSetUpEmbed(name string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Command Not Set Up",
		Description: fmt.Sprintf("**%s** has no configuration yet. Run `/server_config setup` first.", name),
	})
}

func serverConfigUnknownRequirementKeyEmbed(cmdName, key string, slots map[string]types.RequirementSlot) *discordgo.MessageEmbed {
	valid := make([]string, 0, len(slots))
	for k := range slots {
		valid = append(valid, "`"+k+"`")
	}
	sort.Strings(valid)
	desc := fmt.Sprintf("**%s** has no requirement key **%s**.", cmdName, key)
	if len(valid) > 0 {
		desc += " Valid keys: " + strings.Join(valid, ", ")
	} else {
		desc += " This command declares no requirements of this type."
	}
	return helpers.Preset("error", types.PresetOptions{Title: "Unknown Requirement Key", Description: desc})
}

func serverConfigRequirementListEmbed(resourceType types.RequirementType, cmdName string, slots map[string]types.RequirementSlot) *discordgo.MessageEmbed {
	title := fmt.Sprintf("%s Requirements — /%s", strings.ToUpper(string(resourceType[:1]))+string(resourceType[1:]), cmdName)
	if len(slots) == 0 {
		return helpers.Preset("info", types.PresetOptions{
			Title:       title,
			Description: fmt.Sprintf("**%s** declares no %s requirements.", cmdName, resourceType),
		})
	}

	keys := make([]string, 0, len(slots))
	for key := range slots {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	fields := make([]*discordgo.MessageEmbedField, 0, len(keys))
	for _, key := range keys {
		slot := slots[key]
		value := "`Not configured`"
		if slot.ID != "" {
			value = requirementMention(resourceType, slot.ID)
		}
		state := "optional"
		if slot.Required {
			state = "required"
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%s (%s)", key, state),
			Value:  value,
			Inline: true,
		})
	}
	return helpers.Preset("info", types.PresetOptions{
		Title:       title,
		Description: "Configured requirement keys for this command.",
		Fields:      fields,
	})
}

func serverConfigRequirementSetEmbed(resourceType types.RequirementType, cmdName, key, label string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Requirement Updated",
		Description: fmt.Sprintf("Successfully updated the command requirement.\n\n> **Command:** `/%s`\n> **Key:** `%s`\n> **Type:** `%s`\n> **Value:** %s", cmdName, key, resourceType, label),
	})
}

func serverConfigRequirementGetEmbed(resourceType types.RequirementType, cmdName, key string, slot types.RequirementSlot) *discordgo.MessageEmbed {
	value := "`Not configured`"
	if slot.ID != "" {
		value = requirementMention(resourceType, slot.ID)
	}
	state := "optional"
	if slot.Required {
		state = "required"
	}
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Command Requirement",
		Description: fmt.Sprintf("> **Command:** `/%s`\n> **Key:** `%s`\n> **Type:** `%s`\n> **State:** `%s`\n> **Value:** %s", cmdName, key, resourceType, state, value),
	})
}

func serverConfigRequirementClearedEmbed(resourceType types.RequirementType, cmdName, key string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Requirement Cleared",
		Description: fmt.Sprintf("Successfully cleared the command requirement.\n\n> **Command:** `/%s`\n> **Key:** `%s`\n> **Type:** `%s`", cmdName, key, resourceType),
	})
}

func serverConfigUnknownRouteEmbed(group, sub string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unknown Subcommand",
		Description: fmt.Sprintf("Unknown config route `%s %s`. Use `/server_config` without arguments to see available options.", group, sub),
	})
}

func serverConfigInvalidKeyEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Key",
		Description: "You must provide a non-empty key.",
	})
}

func serverConfigMissingValueEmbed(kind string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Missing Value",
		Description: fmt.Sprintf("You must provide a %s.", kind),
	})
}

func serverConfigBotViewEmbed(prefix string, prefixEnabled, slashEnabled bool) *discordgo.MessageEmbed {
	lines := []string{
		fmt.Sprintf("> **Prefix:** `%s` (`%s`)", prefix, enabledLabel(prefixEnabled)),
		fmt.Sprintf("> **Slash Commands:** `%s`", enabledLabel(slashEnabled)),
	}
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Bot Settings",
		Description: strings.Join(lines, "\n"),
	})
}

func serverConfigInvalidPrefixEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Prefix",
		Description: "Please provide a valid prefix of 1-5 characters.",
	})
}

func serverConfigPrefixUpdatedEmbed(prefix string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Prefix Updated",
		Description: fmt.Sprintf("Successfully updated the command prefix.\n\n> **Prefix:** `%s`", prefix),
	})
}

func serverConfigBotToggleEmbed(target string, enabled bool) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Bot Setting Updated",
		Description: fmt.Sprintf("Successfully updated the command mode.\n\n> **Mode:** `%s`\n> **State:** `%s`", target, toggleLabel(enabled)),
	})
}
