package admin

import (
	"fmt"
	"strings"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ServerSetupCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerSetupCommand manages Ranked Bedwars guild registration for this guild.
var ServerSetupCommand = &types.Command{
	Name:           "setup",
	Description:    "Initializes or syncs this guild's Ranked Bedwars data and snapshot.",
	SkipGuildCheck: true,
	Ephemeral:      true,
	Cooldown:       5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: &adminPermissions,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "init",
				Description: "Initialize this guild with Ranked Bedwars",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "sync",
				Description: "Force sync this guild's data and snapshot",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	},
	Execute: executeServerSetupCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeServerSetupCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	subcommand, _ := types.SelectedSubcommand(i)

	switch subcommand {
	case "init":
		return executeGuildSetup(s, i)
	case "sync":
		return executeGuildSync(s, i)
	default:
		return helpers.EditInteractionEmbed(s, i, ServerSetupInvalidSubcommandEmbed(i))
	}
}

func executeGuildSetup(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !helpers.MemberIsAdministrator(s, i) {
		return helpers.EditInteractionEmbed(s, i, helpers.AccessDeniedEmbed(
			"Permission Denied",
			"Only administrators can set up this guild with Ranked Bedwars.",
		))
	}

	result, err := helpers.EnsureGuildSetup(s, i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, ServerSetupFailedEmbed(i, err))
	}

	return helpers.EditInteractionEmbed(s, i, ServerSetupSuccessEmbed(i, result))
}

func executeGuildSync(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !helpers.MemberIsAdministrator(s, i) {
		return helpers.EditInteractionEmbed(s, i, helpers.AccessDeniedEmbed(
			"Permission Denied",
			"Only administrators can sync this guild's data.",
		))
	}

	result, err := helpers.EnsureGuildSetup(s, i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, ServerSyncFailedEmbed(i, err))
	}

	return helpers.EditInteractionEmbed(s, i, ServerSyncSuccessEmbed(i, result))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// ============================================================================
// Helper functions
// ============================================================================

func ServerSetupInvalidSubcommandEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "Use `/setup init` or `/setup sync`.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ServerSetupFailedEmbed(i *discordgo.InteractionCreate, err error) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Guild Setup Failed",
		Description: fmt.Sprintf("Could not initialize this guild with Ranked Bedwars: %v. Verify the bot has the required permissions and try again.", err),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ServerSyncFailedEmbed(i *discordgo.InteractionCreate, err error) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Guild Sync Failed",
		Description: fmt.Sprintf("Could not sync this guild's data: %v. Verify the bot has the required permissions and try again.", err),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ServerSetupSuccessEmbed(i *discordgo.InteractionCreate, result *types.SetupResult) *discordgo.MessageEmbed {
	lines := []string{
		"This guild is now registered with Ranked Bedwars.",
		"",
		"**What was created:**",
	}
	lines = append(lines, SetupResultLines(result)...)
	lines = append(lines, "", "Use `/config` or the web dashboard to assign roles, channels, and bots.")

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Guild Setup Complete",
		Description: strings.Join(lines, "\n"),
		Thumbnail:   helpers.ThumbGuild,
	})
}

func ServerSyncSuccessEmbed(i *discordgo.InteractionCreate, result *types.SetupResult) *discordgo.MessageEmbed {
	lines := []string{
		"This guild's data has been synced successfully.",
		"",
		"**What was synced:**",
	}
	lines = append(lines, SetupResultLines(result)...)
	lines = append(lines, "", "Use `/config` or the web dashboard to assign roles, channels, and bots.")

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Guild Sync Complete",
		Description: strings.Join(lines, "\n"),
		Thumbnail:   helpers.ThumbGuild,
	})
}

func SetupResultLines(result *types.SetupResult) []string {
	lines := []string{}
	if result.GuildCreated {
		lines = append(lines, "> Guild database record")
	}
	if result.ConfigCreated {
		lines = append(lines, "> Guild configuration scaffold")
	}
	if result.SnapshotCreated {
		lines = append(lines, "> Guild snapshot scaffold")
	}
	if result.SnapshotSynced {
		lines = append(lines, "> Discord role and channel snapshot sync")
	}
	if result.MetaCreated {
		lines = append(lines, "> Game meta scaffold")
	}
	if result.GameConfigCreated {
		lines = append(lines, "> Game configuration scaffold")
	}
	if len(lines) == 0 {
		lines = append(lines, "> Nothing new was required; existing records were refreshed")
	}
	return lines
}
