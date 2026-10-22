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
	helpers.Register(PlayerForceNicknameCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerForceNicknameCommand force-sets or removes a target player's in-game nickname.
var PlayerForceNicknameCommand = &types.Command{
	Name:          "forcenickname",
	Requirements:  types.StaffRegistrationRequirements(),
	Description:   "Force-sets or removes a player's in-game Minecraft nickname.",
	PrefixAliases: []string{"fnickname"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to nick",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "nickname",
				Description: "Minecraft nickname to link (leave empty to remove)",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executePlayerForceNicknameCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerForceNicknameCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceNicknameInvalidTargetEmbed(i))
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceNicknameRegistrationRequiredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceNicknameEmbed(i))
	}
	if view.Config.Username == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceNicknameProfileNotFoundEmbed(i))
	}

	config := view.Config
	pid := sdk.UintToString(view.Player.ID)
	actor := helpers.InteractionUserID(i)

	// 3. Handle Nickname Removal (empty string provided)
	newNickname := strings.TrimSpace(helpers.OptionString(i, "nickname"))
	if newNickname == "" {
		if config.Nickname == nil || strings.TrimSpace(*config.Nickname) == "" {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceNicknameNoneActiveEmbed(i))
		}
		if _, err := database.Must().Player.SetNickname(actor, i.GuildID, pid, ""); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceNicknameEmbed(i))
		}
		if _, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, target.ID); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceNicknameEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceNicknameRemovedEmbed(i, target, config.Username))
	}

	// 4. Validate Nickname Format
	if !helpers.IsValidMcUsername(newNickname) {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceNicknameInvalidUsernameEmbed(i))
	}

	// 5. Validation Checks
	if config.Nickname != nil && strings.EqualFold(*config.Nickname, newNickname) {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceNicknameAlreadySetEmbed(i, *config.Nickname))
	}

	search := strings.ToLower(newNickname)
	if isNicknameInUse(i.GuildID, search, view.Player.ID) {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceNicknameConflictEmbed(i, newNickname))
	}

	// 6. Update Nickname in Database
	if _, err := database.Must().Player.SetNickname(actor, i.GuildID, pid, newNickname); err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceNicknameConflictEmbed(i, newNickname))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceNicknameEmbed(i))
	}
	if _, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, target.ID); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceNicknameEmbed(i))
	}

	return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceNicknameUpdatedEmbed(i, target, config.Username, newNickname))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// isNicknameInUse checks if a requested nickname is already tied to another player's username or nickname.
func isNicknameInUse(guildID, search string, requestingPlayerID uint) bool {
	if conflict, err := database.Must().Player.GetByUsername(guildID, search); err == nil && conflict != nil && conflict.PlayerID != requestingPlayerID {
		return true
	}
	if conflict, err := database.Must().Player.GetByNickname(guildID, search); err == nil && conflict != nil && conflict.PlayerID != requestingPlayerID {
		return true
	}
	return false
}
