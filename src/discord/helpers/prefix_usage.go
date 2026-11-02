package helpers

import (
	"strings"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

// prefixUsage is how a prefix command is written: =score <winner> [game] [mvp]. Required options are in <>, optional ones in [].
func prefixUsage(prefix string, cmd *types.Command, route *types.PrefixCommandRoute) string {
	var b strings.Builder
	b.WriteString(prefix + cmd.Data.Name)
	declared := cmd.Data.Options
	if route != nil && route.SubcommandPath != "" {
		for _, part := range strings.Split(route.SubcommandPath, ".") {
			b.WriteString(" " + part)
			for _, opt := range declared {
				if opt != nil && opt.Name == part {
					declared = opt.Options
					break
				}
			}
		}
	}
	for _, opt := range declared {
		if opt == nil || opt.Type == discordgo.ApplicationCommandOptionSubCommand || opt.Type == discordgo.ApplicationCommandOptionSubCommandGroup {
			continue
		}
		name := strings.ReplaceAll(opt.Name, "_", " ")
		if opt.Required {
			b.WriteString(" <" + name + ">")
		} else {
			b.WriteString(" [" + name + "]")
		}
	}
	return b.String()
}

// missingPrefixOptions lists the required options a prefix command was not given. Attachments are left to the command, which
// can also read a picture from the message being replied to.
func missingPrefixOptions(in *discordgo.InteractionCreate, cmd *types.Command, route *types.PrefixCommandRoute) []string {
	data := in.ApplicationCommandData()
	declared, target, ok := prefixLeaf(&data, cmd, route)
	if !ok {
		return nil
	}
	given := map[string]bool{}
	for _, o := range *target {
		given[o.Name] = true
	}
	var missing []string
	for _, opt := range declared {
		if opt == nil || !opt.Required || given[opt.Name] || opt.Type == discordgo.ApplicationCommandOptionAttachment {
			continue
		}
		if opt.Type == discordgo.ApplicationCommandOptionSubCommand || opt.Type == discordgo.ApplicationCommandOptionSubCommandGroup {
			continue
		}
		missing = append(missing, "`"+opt.Name+"`")
	}
	return missing
}

// PrefixUsageEmbed says which required options a prefix command is missing and how the command is written.
func PrefixUsageEmbed(prefix string, cmd *types.Command, route *types.PrefixCommandRoute, missing []string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       "Missing " + strings.Join(missing, ", "),
		Description: "Add " + strings.Join(missing, " and ") + " to the command.\n\n**Usage:** `" + prefixUsage(prefix, cmd, route) + "`\n<> is needed, [] is optional. The words can come in any order.",
		Thumbnail:   ThumbFlag,
	})
}

// prefixLeafOptions are the options a prefix command takes where it was called (inside its subcommand), without attachments.
func prefixLeafOptions(cmd *types.Command, route *types.PrefixCommandRoute) []*discordgo.ApplicationCommandOption {
	declared := cmd.Data.Options
	if route != nil && route.SubcommandPath != "" {
		for _, part := range strings.Split(route.SubcommandPath, ".") {
			var next []*discordgo.ApplicationCommandOption
			for _, opt := range declared {
				if opt != nil && opt.Name == part {
					next = opt.Options
				}
			}
			declared = next
		}
	}
	var out []*discordgo.ApplicationCommandOption
	for _, opt := range declared {
		if opt == nil {
			continue
		}
		switch opt.Type {
		case discordgo.ApplicationCommandOptionSubCommand, discordgo.ApplicationCommandOptionSubCommandGroup, discordgo.ApplicationCommandOptionAttachment:
			continue
		}
		out = append(out, opt)
	}
	return out
}

// unmatchedPrefixWords are the words of a prefix command that no option took ("=clear 5 banana"). A command with text options
// takes every word, so it has none.
func unmatchedPrefixWords(guildID string, cmd *types.Command, route *types.PrefixCommandRoute) []string {
	if route == nil {
		return nil
	}
	leaf := prefixLeafOptions(cmd, route)
	if len(leaf) == 0 || hasFreeText(leaf) {
		return nil
	}
	_, left := assignPrefixWords(guildID, leaf, route.RemainingArgs)
	return left
}

// unresolvedPrefixPlayers are the names typed for a player that no registered player has.
func unresolvedPrefixPlayers(in *discordgo.InteractionCreate, cmd *types.Command, route *types.PrefixCommandRoute) []string {
	data := in.ApplicationCommandData()
	_, target, ok := prefixLeaf(&data, cmd, route)
	if !ok {
		return nil
	}
	var names []string
	for _, o := range *target {
		if o.Type != discordgo.ApplicationCommandOptionUser {
			continue
		}
		if raw, ok := o.Value.(string); ok && !prefixSnowflakePattern.MatchString(raw) {
			names = append(names, "`"+raw+"`")
		}
	}
	return names
}

// PrefixProblemEmbed explains what was wrong with a prefix command's words and how it is written.
func PrefixProblemEmbed(prefix string, cmd *types.Command, route *types.PrefixCommandRoute, title, detail string) *discordgo.MessageEmbed {
	return Preset("error", types.PresetOptions{
		Title:       title,
		Description: detail + "\n\n**Usage:** `" + prefixUsage(prefix, cmd, route) + "`\n<> is needed, [] is optional. The words can come in any order.",
		Thumbnail:   ThumbFlag,
	})
}
