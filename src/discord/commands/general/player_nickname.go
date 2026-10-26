package general

import (
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	cmdcfg "ranked-bedwars/src/discord/config"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(PlayerNicknameCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerNicknameCommand Updates or syncs your display nickname across the platform.
var PlayerNicknameCommand = &types.Command{
	Name:          "nickname",
	Requirements:  types.GeneralRequirements,
	Description:   "Updates or syncs your display nickname across the platform.",
	PrefixAliases: []string{"nick"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "nickname",
				Description: "Your new in-game Minecraft nickname (leave blank or clear to remove)",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executePlayerNicknameCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerNicknameCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	newNickname := helpers.OptionString(i, "nickname")
	userID := helpers.InteractionUserID(i)

	// Fetch existing player record
	view, err := database.Must().Player.Get(i.GuildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.NicknameRegistrationRequiredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorNicknameEmbed(i))
	}

	config := view.Config
	pid := sdk.UintToString(view.Player.ID)

	// Handle nickname removal (empty string provided)
	if newNickname == "" {
		if config.Nickname == nil || strings.TrimSpace(*config.Nickname) == "" {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.NicknameNoneActiveEmbed(i))
		}
		if _, err := database.Must().Player.SetNickname(userID, i.GuildID, pid, ""); err != nil {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorNicknameEmbed(i))
		}
		if _, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, userID); err != nil {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorNicknameEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, cmdcfg.NicknameRemovedEmbed(i))
	}

	// Validate nickname format
	if !helpers.IsValidMcUsername(newNickname) {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.NicknameInvalidUsernameEmbed(i))
	}

	// Check if nickname is already set to the exact value
	if config.Nickname != nil && strings.EqualFold(*config.Nickname, newNickname) {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.NicknameAlreadySetEmbed(i, *config.Nickname))
	}

	// Validate potential conflicts (Premium name, Guild username, Guild nickname)
	if conflictEmbed := validateNicknameConflicts(i, i.GuildID, view.Player.ID, newNickname); conflictEmbed != nil {
		return helpers.EditInteractionEmbed(s, i, conflictEmbed)
	}

	// Persist the new nickname in the database
	if _, err := database.Must().Player.SetNickname(userID, i.GuildID, pid, newNickname); err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.NicknameConflictEmbed(i, newNickname))
		}
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorNicknameEmbed(i))
	}
	if _, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, userID); err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorNicknameEmbed(i))
	}

	return helpers.EditInteractionEmbed(s, i, cmdcfg.NicknameUpdatedEmbed(i, newNickname))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

func validateNicknameConflicts(i *discordgo.InteractionCreate, guildID string, playerID uint, nickname string) *discordgo.MessageEmbed {
	search := strings.ToLower(nickname)
	verification, _ := helpers.LoadRegistrationVerification(guildID)

	// Verify if the requested nickname is a premium Minecraft account
	if helpers.RequiresRegistrationAPIVerification(verification) {
		exists, verifyErr := helpers.VerifyMinecraftUsernameExistsWithConfig(nickname, verification)
		if verifyErr != nil {
			return cmdcfg.NicknameAPIOutageEmbed(i, helpers.RegistrationNetworkLabel(verification))
		}
		if exists {
			return cmdcfg.NicknamePremiumNameEmbed(i, nickname)
		}
	}

	// Check if another player in the guild uses this name as a primary username
	usernameConflict, err := database.Must().Player.GetByUsername(guildID, search)
	if err == nil && usernameConflict != nil && usernameConflict.PlayerID != playerID {
		return cmdcfg.NicknameConflictEmbed(i, nickname)
	}

	// Check if another player in the guild uses this name as a nickname
	nicknameConflict, err := database.Must().Player.GetByNickname(guildID, search)
	if err == nil && nicknameConflict != nil && nicknameConflict.PlayerID != playerID {
		return cmdcfg.NicknameConflictEmbed(i, nickname)
	}

	return nil
}
