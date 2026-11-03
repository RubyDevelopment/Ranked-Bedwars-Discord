package general

import (
	"fmt"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
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
	Requirements: types.RegistrationRequirements,
	Description:  "Links your Minecraft account to your Discord account (or changes it if you are registered).",
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
		return helpers.EditInteractionEmbed(s, i, PlayerRegisterInvalidUsernameEmbed(i))
	}

	userID := helpers.InteractionUserID(i)

	// Already registered: it is a rename
	existing, err := database.Must().Player.Get(i.GuildID, userID)
	if err == nil && existing != nil {
		// Registered already: the same words name the account to switch to, so this is /rename (same checks, same answers).
		return executePlayerRenameCommand(s, i)
	} else if err != nil && !sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerRegisterEmbed(i))
	}

	// A rank must exist so the new player can be given one (and its role)
	if ok, rankErr := helpers.HasConfiguredRank(i.GuildID); rankErr != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerRegisterEmbed(i))
	} else if !ok {
		return helpers.EditInteractionEmbed(s, i, PlayerRegisterNoRanksEmbed(i))
	}

	// Registration creates the player's stats in the active season, so one has to exist
	if _, err := database.Must().Season.Active(i.GuildID); sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, PlayerRegisterNoSeasonEmbed(i))
	} else if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerRegisterEmbed(i))
	}

	verification, _ := helpers.LoadRegistrationVerification(i.GuildID)

	// Party-code mode: the user proves they own the account first; the code modal finishes the registration
	if RequiresPartyCodeVerification(verification) {
		return startPartyCodeRegistration(s, i, userID, username, verification.InGame)
	}

	// Verify Minecraft account existence if required by guild setup
	if helpers.RequiresRegistrationAPIVerification(verification) {
		exists, _, verifyErr := helpers.VerifyWithFallback(username, verification)
		if verifyErr != nil {
			return helpers.EditInteractionEmbed(s, i, PlayerRegisterAPIOutageEmbed(i, helpers.RegistrationNetworkLabel(verification)))
		}
		if !exists {
			return helpers.EditInteractionEmbed(s, i, PlayerRegisterAccountNotFoundEmbed(i, username, helpers.RegistrationNetworkLabel(verification)))
		}
	}

	// Register player atomically
	return helpers.EditInteractionEmbed(s, i, registerNewPlayer(i, s, i.GuildID, userID, username))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// registerNewPlayer links the Minecraft name to the Discord user and returns the embed that says how it went.
func registerNewPlayer(i *discordgo.InteractionCreate, s *discordgo.Session, guildID, userID, username string) *discordgo.MessageEmbed {
	reg, err := database.Must().Player.Register(userID, guildID, userID, username, 0)
	if err != nil {
		if sdk.IsConflict(err) {
			return helpers.PlayerRegisterConflictEmbed(i, username)
		}
		return helpers.UnexpectedErrorPlayerRegisterEmbed(i)
	}
	// Apply starting rank/ELO for brand new registrations
	if reg.CreatedGuild {
		if err := helpers.ApplyStartingRank(guildID, userID, reg.PlayerID); err != nil {
			return helpers.UnexpectedErrorPlayerRegisterEmbed(i)
		}
	}
	if _, err := helpers.SyncRegisteredPlayerProfile(s, guildID, reg.PlayerID, userID); err != nil {
		return helpers.UnexpectedErrorPlayerRegisterEmbed(i)
	}
	return PlayerRegisterSuccessEmbed(i, username)
}

// ============================================================================
// Helper functions
// ============================================================================

func PlayerRegisterNoRanksEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Unavailable",
		Description: "No ranks have been configured for this guild yet, so you cannot be registered. Please ask an administrator to set up at least one rank.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerRegisterNoSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Unavailable",
		Description: "This guild has no active season yet, so you cannot be registered. Please ask an administrator to start one on the dashboard's Season Periods page.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerRegisterAPIOutageEmbed(i *discordgo.InteractionCreate, network string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: fmt.Sprintf("Could not verify that account on **%s** right now. Please try again in a few minutes.", network),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerRegisterAccountNotFoundEmbed(i *discordgo.InteractionCreate, username, network string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Account Not Found",
		Description: fmt.Sprintf("Could not find a Minecraft account named **%s** on **%s**. Double-check the spelling and try again.", username, network),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func PlayerRegisterInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerRegisterSuccessEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Registration Complete",
		Description: fmt.Sprintf(
			"Your Discord account has been successfully linked. Please wait a moment for your roles and display name to update.\n\n> **Username:** `%s`",
			username,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}

// RequiresPartyCodeVerification reports whether /register has to prove ownership with a party code.
func RequiresPartyCodeVerification(cfg *types.RegistrationVerification) bool {
	return cfg != nil && cfg.Mode == helpers.RegistrationModePartyCode
}
