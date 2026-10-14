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
	helpers.Register(PlayerForceRenameCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerForceRenameCommand force-changes a target player's linked Minecraft username.
var PlayerForceRenameCommand = &types.Command{
	Name:        "player_force_rename",
	Description: "Force-renames a player's linked Minecraft username.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to rename",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "username",
				Description: "Minecraft username to link",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
		},
	},
	Execute: executePlayerForceRenameCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerForceRenameCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, playerForceRenameInvalidTargetEmbed())
	}

	// 2. Validate Username Format
	newUsername := strings.TrimSpace(helpers.OptionString(i, "username"))
	if !helpers.IsValidMcUsername(newUsername) {
		return helpers.EditInteractionEmbed(s, i, playerForceRenameInvalidUsernameEmbed())
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerForceRenameRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceRenameEmbed())
	}
	if view.Config.Username == "" {
		return helpers.EditInteractionEmbed(s, i, playerForceRenameProfileNotFoundEmbed())
	}

	// 4. Validation Checks
	if strings.EqualFold(view.Config.Username, newUsername) {
		return helpers.EditInteractionEmbed(s, i, playerForceRenameAlreadyLinkedEmbed(newUsername))
	}

	// 5. Update Username in Database
	oldUsername := view.Config.Username
	actor := helpers.InteractionUserID(i)
	if _, err := database.Must().Player.SetUsername(actor, i.GuildID, sdk.UintToString(view.Player.ID), newUsername); err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, playerForceRenameConflictEmbed(newUsername))
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceRenameEmbed())
	}

	return helpers.EditInteractionEmbed(s, i, playerForceRenameSuccessEmbed(target, oldUsername, newUsername))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerForceRenameEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while force-renaming the player. Please try again later or contact support.",
	})
}

func playerForceRenameInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to rename. Select a registered player from the command options.",
	})
}

func playerForceRenameInvalidUsernameEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
	})
}

func playerForceRenameRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can be force-renamed.",
	})
}

func playerForceRenameProfileNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "That player's guild profile could not be found. Run `/player_force_sync` or contact staff if the issue persists.",
	})
}

func playerForceRenameAlreadyLinkedEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already Linked",
		Description: fmt.Sprintf("That account is already linked to **%s**. No further action is needed.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerForceRenameConflictEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild. Try a different username.", username),
	})
}

func playerForceRenameSuccessEmbed(user *discordgo.User, oldUsername, newUsername string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Username Force-Updated",
		Description: fmt.Sprintf(
			"Successfully updated the player's linked username. This change may take a moment to propagate across all systems.\n\n> **User:** <@%s>\n> **Previous Username:** `%s`\n> **New Username:** `%s`",
			user.ID, oldUsername, newUsername,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(newUsername),
	})
}
