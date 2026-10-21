package admin

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
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
		return helpers.EditInteractionEmbed(s, i, serverSetupInvalidSubcommandEmbed())
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
		return helpers.EditInteractionEmbed(s, i, serverSetupFailedEmbed(err))
	}

	return helpers.EditInteractionEmbed(s, i, serverSetupSuccessEmbed(result))
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
		return helpers.EditInteractionEmbed(s, i, serverSyncFailedEmbed(err))
	}

	return helpers.EditInteractionEmbed(s, i, serverSyncSuccessEmbed(result))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

func setupResultLines(result *types.SetupResult) []string {
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

// ============================================================================
// Embed Builders
// ============================================================================

func serverSetupInvalidSubcommandEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "Use `/server_setup init` or `/server_setup sync`.",
	})
}

func serverSetupFailedEmbed(err error) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Guild Setup Failed",
		Description: fmt.Sprintf("Could not initialize this guild with Ranked Bedwars: %v. Verify the bot has the required permissions and try again.", err),
	})
}

func serverSyncFailedEmbed(err error) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Guild Sync Failed",
		Description: fmt.Sprintf("Could not sync this guild's data: %v. Verify the bot has the required permissions and try again.", err),
	})
}

func serverSetupSuccessEmbed(result *types.SetupResult) *discordgo.MessageEmbed {
	lines := []string{
		"This guild is now registered with Ranked Bedwars.",
		"",
		"**What was created:**",
	}
	lines = append(lines, setupResultLines(result)...)
	lines = append(lines, "", "Use `/server_config` or the web dashboard to assign roles, channels, and bots.")

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Guild Setup Complete",
		Description: strings.Join(lines, "\n"),
	})
}

func serverSyncSuccessEmbed(result *types.SetupResult) *discordgo.MessageEmbed {
	lines := []string{
		"This guild's data has been synced successfully.",
		"",
		"**What was synced:**",
	}
	lines = append(lines, setupResultLines(result)...)
	lines = append(lines, "", "Use `/server_config` or the web dashboard to assign roles, channels, and bots.")

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Guild Sync Complete",
		Description: strings.Join(lines, "\n"),
	})
}
