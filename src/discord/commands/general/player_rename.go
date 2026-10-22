package general

import (
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
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
	Name:         "rename",
	Requirements: types.GeneralRequirements,
	Description:  "Updates your linked Minecraft username.",
	Ephemeral:    true,
	Cooldown:     5 * time.Second,
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
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerRenameRegistrationRequiredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerRenameEmbed(i))
	}
	if view.Config.Username == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerRenameProfileNotFoundEmbed(i))
	}

	// Validation checks
	if strings.EqualFold(view.Config.Username, newUsername) {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerRenameAlreadyLinkedEmbed(i, newUsername))
	}
	if !helpers.IsValidMcUsername(newUsername) {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerRenameInvalidUsernameEmbed(i))
	}

	// Verify Minecraft account existence if required by guild setup
	verification, _ := helpers.LoadRegistrationVerification(i.GuildID)
	if helpers.RequiresRegistrationAPIVerification(verification) {
		exists, verifyErr := helpers.VerifyMinecraftUsernameExistsWithConfig(newUsername, verification)
		if verifyErr != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerRenameAPIOutageEmbed(i, helpers.RegistrationNetworkLabel(verification)))
		}
		if !exists {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerRenameAccountNotFoundEmbed(i, newUsername, helpers.RegistrationNetworkLabel(verification)))
		}
	}

	// Update username in database
	oldUsername := view.Config.Username
	if _, err := database.Must().Player.SetUsername(userID, i.GuildID, sdk.UintToString(view.Player.ID), newUsername); err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerRenameConflictEmbed(i, newUsername))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerRenameEmbed(i))
	}
	if _, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, userID); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerRenameEmbed(i))
	}

	return helpers.EditInteractionEmbed(s, i, helpers.PlayerRenameSuccessEmbed(i, oldUsername, newUsername))
}
