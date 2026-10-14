package general

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
	helpers.Register(PlayerRenameCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerRenameCommand updates the caller's linked Minecraft username.
var PlayerRenameCommand = &types.Command{
	Name:        "rename",
	Description: "Updates your linked Minecraft username.",
	Ephemeral:   true,
	Cooldown:    5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "username",
				Description: "Your new premium Minecraft username",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
		},
	},
	Execute: executePlayerRenameCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerRenameCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	newUsername := helpers.OptionString(i, "username")
	userID := helpers.InteractionUserID(i)

	// Fetch existing player record
	view, err := database.Must().Player.Get(i.GuildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerRenameRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerRenameEmbed())
	}
	if view.Config.Username == "" {
		return helpers.EditInteractionEmbed(s, i, playerRenameProfileNotFoundEmbed())
	}

	// Validation checks
	if strings.EqualFold(view.Config.Username, newUsername) {
		return helpers.EditInteractionEmbed(s, i, playerRenameAlreadyLinkedEmbed(newUsername))
	}
	if !helpers.IsValidMcUsername(newUsername) {
		return helpers.EditInteractionEmbed(s, i, playerRenameInvalidUsernameEmbed())
	}

	// Verify Minecraft account existence if required by guild setup
	verification, _ := helpers.LoadRegistrationVerification(i.GuildID)
	if helpers.RequiresRegistrationAPIVerification(verification) {
		exists, verifyErr := helpers.VerifyMinecraftUsernameExists(newUsername)
		if verifyErr != nil {
			return helpers.EditInteractionEmbed(s, i, playerRenameAPIOutageEmbed(helpers.RegistrationNetworkLabel(verification)))
		}
		if !exists {
			return helpers.EditInteractionEmbed(s, i, playerRenameAccountNotFoundEmbed(newUsername, helpers.RegistrationNetworkLabel(verification)))
		}
	}

	// Update username in database
	oldUsername := view.Config.Username
	if _, err := database.Must().Player.SetUsername(userID, i.GuildID, sdk.UintToString(view.Player.ID), newUsername); err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, playerRenameConflictEmbed(newUsername))
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerRenameEmbed())
	}

	return helpers.EditInteractionEmbed(s, i, playerRenameSuccessEmbed(oldUsername, newUsername))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerRenameEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to update your username. Please contact an administrator for assistance or try again later.",
	})
}

func playerRenameRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before you can rename your linked account. Run `/player_register` to link your Minecraft account first.",
	})
}

func playerRenameProfileNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "Your guild player profile could not be found. Try running `/player_refresh` or contact staff if the issue persists.",
	})
}

func playerRenameAlreadyLinkedEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already Linked",
		Description: fmt.Sprintf("Your account is already linked to **%s**. No further changes are needed.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerRenameInvalidUsernameEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
	})
}

func playerRenameAPIOutageEmbed(network string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: fmt.Sprintf("Could not verify that account on **%s** right now. Please try again in a few minutes.", network),
	})
}

func playerRenameAccountNotFoundEmbed(username, network string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Account Not Found",
		Description: fmt.Sprintf("Could not find a Minecraft account named **%s** on **%s**. Double-check the spelling and try again.", username, network),
	})
}

func playerRenameConflictEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild. Try a different username or contact staff if this is your account.", username),
	})
}

func playerRenameSuccessEmbed(oldUsername, newUsername string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Username Updated",
		Description: fmt.Sprintf(
			"Your linked Minecraft username has been updated. Please wait a moment for your display name to update.\n\n> **Previous Username:** `%s`\n> **New Username:** `%s`",
			oldUsername, newUsername,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(newUsername),
	})
}
