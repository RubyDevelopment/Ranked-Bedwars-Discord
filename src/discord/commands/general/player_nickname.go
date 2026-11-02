package general

import (
	"fmt"
	"strings"
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
			return helpers.EditInteractionEmbed(s, i, NicknameRegistrationRequiredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorNicknameEmbed(i))
	}

	config := view.Config
	pid := sdk.UintToString(view.Player.ID)

	// Handle nickname removal (empty string provided)
	if newNickname == "" {
		if config.Nickname == nil || strings.TrimSpace(*config.Nickname) == "" {
			return helpers.EditInteractionEmbed(s, i, NicknameNoneActiveEmbed(i))
		}
		if _, err := database.Must().Player.SetNickname(userID, i.GuildID, pid, ""); err != nil {
			return helpers.EditInteractionEmbed(s, i, UnexpectedErrorNicknameEmbed(i))
		}
		if _, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, userID); err != nil {
			return helpers.EditInteractionEmbed(s, i, UnexpectedErrorNicknameEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, NicknameRemovedEmbed(i))
	}

	// Validate nickname format
	if !helpers.IsValidMcUsername(newNickname) {
		return helpers.EditInteractionEmbed(s, i, NicknameInvalidUsernameEmbed(i))
	}

	// Check if nickname is already set to the exact value
	if config.Nickname != nil && strings.EqualFold(*config.Nickname, newNickname) {
		return helpers.EditInteractionEmbed(s, i, NicknameAlreadySetEmbed(i, *config.Nickname))
	}

	// Validate potential conflicts (Premium name, Guild username, Guild nickname)
	if conflictEmbed := validateNicknameConflicts(i, i.GuildID, view.Player.ID, newNickname); conflictEmbed != nil {
		return helpers.EditInteractionEmbed(s, i, conflictEmbed)
	}

	// Persist the new nickname in the database
	if _, err := database.Must().Player.SetNickname(userID, i.GuildID, pid, newNickname); err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, NicknameConflictEmbed(i, newNickname))
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorNicknameEmbed(i))
	}
	if _, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, userID); err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorNicknameEmbed(i))
	}

	return helpers.EditInteractionEmbed(s, i, NicknameUpdatedEmbed(i, newNickname))
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
			return NicknameAPIOutageEmbed(i, helpers.RegistrationNetworkLabel(verification))
		}
		if exists {
			return NicknamePremiumNameEmbed(i, nickname)
		}
	}

	// Check if another player in the guild uses this name as a primary username
	usernameConflict, err := database.Must().Player.GetByUsername(guildID, search)
	if err == nil && usernameConflict != nil && usernameConflict.PlayerID != playerID {
		return NicknameConflictEmbed(i, nickname)
	}

	// Check if another player in the guild uses this name as a nickname
	nicknameConflict, err := database.Must().Player.GetByNickname(guildID, search)
	if err == nil && nicknameConflict != nil && nicknameConflict.PlayerID != playerID {
		return NicknameConflictEmbed(i, nickname)
	}

	return nil
}

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorNicknameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to update your nickname. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func NicknameRegistrationRequiredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before setting a nickname. Run `/register` to link your Minecraft account first.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func NicknameNoneActiveEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Nickname Set",
		Description: "It looks like you do not have an active nickname to remove. Provide a nickname with `/nickname` to assign one first.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func NicknameInvalidUsernameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Nickname",
		Description: "That nickname format is not valid. Nicknames must be 3-16 characters and contain only letters, numbers, and underscores.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func NicknameAlreadySetEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Nickname Already Set",
		Description: fmt.Sprintf("Your nickname is already set to **%s**. No further action is needed.", nickname),
		Thumbnail:   helpers.MinecraftBustAvatarURL(nickname),
	})
}

func NicknamePremiumNameEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Premium Username",
		Description: fmt.Sprintf("**%s** is a premium Minecraft username and cannot be used as a nickname. Choose a different nickname that is not a registered premium name.", nickname),
		Thumbnail:   helpers.MinecraftBustAvatarURL(nickname),
	})
}

func NicknameConflictEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Nickname Taken",
		Description: fmt.Sprintf("The nickname **%s** is already in use in this guild. Try a different nickname.", nickname),
		Thumbnail:   helpers.MinecraftBustAvatarURL(nickname),
	})
}

func NicknameAPIOutageEmbed(i *discordgo.InteractionCreate, label string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Verification Unavailable",
		Description: fmt.Sprintf("The %s verification service is currently unavailable. Please try again later.", label),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func NicknameRemovedEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Nickname Removed",
		Description: "Your in-game nickname has been cleared. Use `/sync` to refresh your displayed name.",
		Thumbnail:   helpers.ThumbLoad,
	})
}

func NicknameUpdatedEmbed(i *discordgo.InteractionCreate, nickname string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Nickname Updated",
		Description: fmt.Sprintf(
			"Your in-game nickname has been updated to **%s**. Please wait a moment for your displayed name to update.",
			nickname,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(nickname),
	})
}
