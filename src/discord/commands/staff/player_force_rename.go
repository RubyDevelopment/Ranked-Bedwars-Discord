package staff

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
	helpers.Register(PlayerForceRenameCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerForceRenameCommand force-changes a target player's linked Minecraft username.
var PlayerForceRenameCommand = &types.Command{
	Name:          "forcerename",
	Requirements:  types.StaffRegistrationRequirements(),
	Description:   "Force-renames a player's linked Minecraft username.",
	PrefixAliases: []string{"frename"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
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
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRenameInvalidTargetEmbed(i))
	}

	// 2. Validate Username Format
	newUsername := strings.TrimSpace(helpers.OptionString(i, "username"))
	if !helpers.IsValidMcUsername(newUsername) {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRenameInvalidUsernameEmbed(i))
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRenameRegistrationRequiredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceRenameEmbed(i))
	}
	if view.Config.Username == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRenameProfileNotFoundEmbed(i))
	}

	// 4. Validation Checks
	if strings.EqualFold(view.Config.Username, newUsername) {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRenameAlreadyLinkedEmbed(i, newUsername))
	}

	// 5. Update Username in Database
	oldUsername := view.Config.Username
	actor := helpers.InteractionUserID(i)
	if _, err := database.Must().Player.SetUsername(actor, i.GuildID, sdk.UintToString(view.Player.ID), newUsername); err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRenameConflictEmbed(i, newUsername))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceRenameEmbed(i))
	}
	if _, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, target.ID); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceRenameEmbed(i))
	}

	return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRenameSuccessEmbed(i, target, oldUsername, newUsername))
}
