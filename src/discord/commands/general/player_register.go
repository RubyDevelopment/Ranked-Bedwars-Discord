package general

import (
	"fmt"
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
	helpers.Register(PlayerRegisterCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerRegisterCommand links the caller's Minecraft account to their Discord account.
var PlayerRegisterCommand = &types.Command{
	Name:         "register",
	Description:  "Links your Minecraft account to your Discord account.",
	Requirements: types.RegistrationRequirements,
	Ephemeral:    true,
	Cooldown:     5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "username",
				Description: "Your Minecraft username",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
		},
	},
	Execute: executePlayerRegisterCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerRegisterCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	username := helpers.OptionString(i, "username")
	if !helpers.IsValidMcUsername(username) {
		return helpers.EditInteractionEmbed(s, i, playerRegisterInvalidUsernameEmbed())
	}

	userID := helpers.InteractionUserID(i)

	// Check if player is already registered
	existing, err := database.Must().Player.Get(i.GuildID, userID)
	if err == nil && existing != nil {
		return helpers.EditInteractionEmbed(s, i, playerRegisterAlreadyRegisteredEmbed(existing.Config.Username))
	} else if err != nil && !sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerRegisterEmbed())
	}

	// A rank must exist so the new player can be given one (and its role)
	if ok, rankErr := helpers.HasConfiguredRank(i.GuildID); rankErr != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerRegisterEmbed())
	} else if !ok {
		return helpers.EditInteractionEmbed(s, i, playerRegisterNoRanksEmbed())
	}

	verification, _ := helpers.LoadRegistrationVerification(i.GuildID)

	// Party-code mode: the user proves they own the account first; the code modal finishes the registration
	if helpers.RequiresPartyCodeVerification(verification) {
		return startPartyCodeRegistration(s, i, userID, username)
	}

	// Verify Minecraft account existence if required by guild setup
	if helpers.RequiresRegistrationAPIVerification(verification) {
		exists, verifyErr := helpers.VerifyMinecraftUsernameExistsWithConfig(username, verification)
		if verifyErr != nil {
			return helpers.EditInteractionEmbed(s, i, playerRegisterAPIOutageEmbed(helpers.RegistrationNetworkLabel(verification)))
		}
		if !exists {
			return helpers.EditInteractionEmbed(s, i, playerRegisterAccountNotFoundEmbed(username, helpers.RegistrationNetworkLabel(verification)))
		}
	}

	// Register player atomically
	return helpers.EditInteractionEmbed(s, i, registerNewPlayer(s, i.GuildID, userID, username))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// registerNewPlayer links the Minecraft name to the Discord user and returns the embed that says how it went.
func registerNewPlayer(s *discordgo.Session, guildID, userID, username string) *discordgo.MessageEmbed {
	reg, err := database.Must().Player.Register(userID, guildID, userID, username, 0)
	if err != nil {
		if sdk.IsConflict(err) {
			return playerRegisterConflictEmbed(username)
		}
		return unexpectedErrorPlayerRegisterEmbed()
	}
	// Apply starting rank/ELO for brand new registrations
	if reg.CreatedGuild {
		if err := helpers.ApplyStartingRank(guildID, userID, reg.PlayerID); err != nil {
			return unexpectedErrorPlayerRegisterEmbed()
		}
	}
	if _, err := helpers.SyncRegisteredPlayerProfile(s, guildID, reg.PlayerID, userID); err != nil {
		return unexpectedErrorPlayerRegisterEmbed()
	}
	return playerRegisterSuccessEmbed(username)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerRegisterEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to process your registration. Please contact an administrator for assistance or try again later.",
	})
}

func playerRegisterNoRanksEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Unavailable",
		Description: "No ranks have been configured for this guild yet, so you cannot be registered. Please ask an administrator to set up at least one rank.",
	})
}

func playerRegisterConflictEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild. Try a different username or contact staff if this is your account.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerRegisterAlreadyRegisteredEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("You are already registered as **%s**. Use `/player_rename` to change your username.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerRegisterAPIOutageEmbed(network string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: fmt.Sprintf("Could not verify that account on **%s** right now. Please try again in a few minutes.", network),
	})
}

func playerRegisterAccountNotFoundEmbed(username, network string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Account Not Found",
		Description: fmt.Sprintf("Could not find a Minecraft account named **%s** on **%s**. Double-check the spelling and try again.", username, network),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerRegisterInvalidUsernameEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
	})
}

func playerRegisterSuccessEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Registration Complete",
		Description: fmt.Sprintf(
			"Your Discord account has been successfully linked. Please wait a moment for your roles and display name to update.\n\n> **Username:** `%s`",
			username,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}
