package admin

import (
	"time"

	"github.com/bwmarrin/discordgo"

	cmdcfg "ranked-bedwars/src/discord/config"
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
		return helpers.EditInteractionEmbed(s, i, cmdcfg.ServerSetupInvalidSubcommandEmbed(i))
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
		return helpers.EditInteractionEmbed(s, i, cmdcfg.ServerSetupFailedEmbed(i, err))
	}

	return helpers.EditInteractionEmbed(s, i, cmdcfg.ServerSetupSuccessEmbed(i, result))
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
		return helpers.EditInteractionEmbed(s, i, cmdcfg.ServerSyncFailedEmbed(i, err))
	}

	return helpers.EditInteractionEmbed(s, i, cmdcfg.ServerSyncSuccessEmbed(i, result))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================
