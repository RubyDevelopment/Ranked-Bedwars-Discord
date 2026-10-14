package staff

import (
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(PlayerForceSyncCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerForceSyncCommand force-synchronizes a target player's registered role, rank role, and nickname.
var PlayerForceSyncCommand = &types.Command{
	Name:          "forcesync",
	Description:   "Force-synchronizes a player's registered role, rank role, and nickname with the guild.",
	PrefixAliases: []string{"fsync"},
	Ephemeral:     true,
	Cooldown:      5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Player to sync",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Execute: executePlayerForceSyncCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerForceSyncCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, playerForceSyncInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, playerForceSyncBotTargetEmbed())
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerForceSyncRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceSyncEmbed())
	}

	// 3. Validate Guild Membership
	member, err := helpers.FetchGuildMember(s, i.GuildID, target.ID)
	if err != nil || member == nil {
		return helpers.EditInteractionEmbed(s, i, playerForceSyncMemberNotFoundEmbed())
	}

	// 4. Synchronize Roles & Nickname
	actions, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, target.ID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceSyncEmbed())
	}

	// 5. Return Result
	if len(actions) == 0 {
		return helpers.EditInteractionEmbed(s, i, playerForceSyncUpToDateEmbed(view.Config.Username))
	}
	return helpers.EditInteractionEmbed(s, i, playerForceSyncSuccessEmbed(target, view.Config.Username, actions))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerForceSyncEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while force-synchronizing the player's profile. Please try again later or contact support.",
	})
}

func playerForceSyncInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to sync. Select a registered player from the command options.",
	})
}

func playerForceSyncBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be force-synced. Target a registered player instead.",
	})
}

func playerForceSyncRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/player_force_register` first.",
	})
}

func playerForceSyncMemberNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: "Could not find that player's guild membership to sync. Verify they are still a member of this guild.",
	})
}

func playerForceSyncUpToDateEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Profile Synchronized",
		Description: fmt.Sprintf("The profile for **%s** is already up to date. No further changes are needed.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerForceSyncSuccessEmbed(user *discordgo.User, username string, actions []string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Profile Force-Synchronized",
		Description: fmt.Sprintf(
			"Successfully refreshed the player's roles and nickname. Run `/player_force_sync` again anytime their roles or nickname need refreshing.\n\n> **User:** <@%s>\n> **Username:** `%s`\n\n%s",
			user.ID, username, strings.Join(actions, "\n"),
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}
