package staff

import (
	"fmt"
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
		return helpers.EditInteractionEmbed(s, i, playerForceNicknameInvalidTargetEmbed())
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerForceNicknameRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceNicknameEmbed())
	}
	if view.Config.Username == "" {
		return helpers.EditInteractionEmbed(s, i, playerForceNicknameProfileNotFoundEmbed())
	}

	config := view.Config
	pid := sdk.UintToString(view.Player.ID)
	actor := helpers.InteractionUserID(i)

	// 3. Handle Nickname Removal (empty string provided)
	newNickname := strings.TrimSpace(helpers.OptionString(i, "nickname"))
	if newNickname == "" {
		if config.Nickname == nil || strings.TrimSpace(*config.Nickname) == "" {
			return helpers.EditInteractionEmbed(s, i, playerForceNicknameNoneActiveEmbed())
		}
		if _, err := database.Must().Player.SetNickname(actor, i.GuildID, pid, ""); err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceNicknameEmbed())
		}
		if _, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, target.ID); err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceNicknameEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, playerForceNicknameRemovedEmbed(target, config.Username))
	}

	// 4. Validate Nickname Format
	if !helpers.IsValidMcUsername(newNickname) {
		return helpers.EditInteractionEmbed(s, i, playerForceNicknameInvalidUsernameEmbed())
	}

	// 5. Validation Checks
	if config.Nickname != nil && strings.EqualFold(*config.Nickname, newNickname) {
		return helpers.EditInteractionEmbed(s, i, playerForceNicknameAlreadySetEmbed(*config.Nickname))
	}

	search := strings.ToLower(newNickname)
	if isNicknameInUse(i.GuildID, search, view.Player.ID) {
		return helpers.EditInteractionEmbed(s, i, playerForceNicknameConflictEmbed(newNickname))
	}

	// 6. Update Nickname in Database
	if _, err := database.Must().Player.SetNickname(actor, i.GuildID, pid, newNickname); err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, playerForceNicknameConflictEmbed(newNickname))
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceNicknameEmbed())
	}
	if _, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, target.ID); err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceNicknameEmbed())
	}

	return helpers.EditInteractionEmbed(s, i, playerForceNicknameUpdatedEmbed(target, config.Username, newNickname))
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

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerForceNicknameEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while force-updating the player's nickname. Please try again later or contact support.",
	})
}

func playerForceNicknameInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to update. Select a registered player from the command options.",
	})
}

func playerForceNicknameRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/player_force_register` first.",
	})
}

func playerForceNicknameProfileNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "That player's guild profile could not be found. Run `/player_force_sync` or contact staff if the issue persists.",
	})
}

func playerForceNicknameNoneActiveEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Nickname Set",
		Description: "That player does not have an active nickname to remove. Provide a nickname value to assign one.",
	})
}

func playerForceNicknameInvalidUsernameEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Nickname",
		Description: "That nickname format is not valid. Nicknames must be 3-16 characters and contain only letters, numbers, and underscores.",
	})
}

func playerForceNicknameAlreadySetEmbed(nickname string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Nickname Already Set",
		Description: fmt.Sprintf("That player's nickname is already set to **%s**. No further action is needed.", nickname),
		Thumbnail:   helpers.MinecraftBustAvatarURL(nickname),
	})
}

func playerForceNicknameConflictEmbed(nickname string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Nickname Taken",
		Description: fmt.Sprintf("The nickname **%s** is already in use in this guild. Try a different nickname.", nickname),
		Thumbnail:   helpers.MinecraftBustAvatarURL(nickname),
	})
}

func playerForceNicknameRemovedEmbed(user *discordgo.User, username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Nickname Cleared",
		Description: fmt.Sprintf("Successfully cleared the player's in-game nickname.\n\n> **User:** <@%s>\n> **Username:** `%s`", user.ID, username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerForceNicknameUpdatedEmbed(user *discordgo.User, username, nickname string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Nickname Force-Updated",
		Description: fmt.Sprintf(
			"Successfully updated the player's in-game nickname. This change may take a moment to propagate across all systems.\n\n> **User:** <@%s>\n> **Username:** `%s`\n> **Nickname:** `%s`",
			user.ID, username, nickname,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(nickname),
	})
}
