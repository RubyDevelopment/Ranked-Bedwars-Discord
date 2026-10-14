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
	helpers.Register(PlayerForceRegisterCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerForceRegisterCommand force-registers a target user with a Minecraft username without verification.
var PlayerForceRegisterCommand = &types.Command{
	Name:          "forceregister",
	Description:   "Force-registers a Minecraft account to a Discord account without verification.",
	PrefixAliases: []string{"fregister"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to register",
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
	Execute: executePlayerForceRegisterCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerForceRegisterCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, playerForceRegisterInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, playerForceRegisterBotTargetEmbed())
	}

	// 2. Validate Username Format
	username := strings.TrimSpace(helpers.OptionString(i, "username"))
	if !helpers.IsValidMcUsername(username) {
		return helpers.EditInteractionEmbed(s, i, playerForceRegisterInvalidUsernameEmbed())
	}

	// 3. Check for Existing Registration
	if existing, err := database.Must().Player.Get(i.GuildID, target.ID); err == nil {
		return helpers.EditInteractionEmbed(s, i, playerForceRegisterAlreadyRegisteredEmbed(existing.Config.Username))
	} else if !sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceRegisterEmbed())
	}

	// 4. Require at least one configured rank so the player can be given one (and its role)
	if ok, rankErr := helpers.HasConfiguredRank(i.GuildID); rankErr != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceRegisterEmbed())
	} else if !ok {
		return helpers.EditInteractionEmbed(s, i, playerForceRegisterNoRanksEmbed())
	}

	// 5. Register (creates the global player, config, and starting stats atomically)
	actor := helpers.InteractionUserID(i)
	reg, err := database.Must().Player.Register(actor, i.GuildID, target.ID, username, 0)
	if err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, playerForceRegisterConflictEmbed(username))
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceRegisterEmbed())
	}

	// 6. Apply the guild's configured starting rank/ELO for new registrations
	if reg.CreatedGuild {
		if err := helpers.ApplyStartingRank(i.GuildID, actor, reg.PlayerID); err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceRegisterEmbed())
		}
	}

	return helpers.EditInteractionEmbed(s, i, playerForceRegisterSuccessEmbed(target, username))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerForceRegisterEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing the force registration. Please try again later or contact support.",
	})
}

func playerForceRegisterInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a user to register. Select a Discord member from the command options.",
	})
}

func playerForceRegisterBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be registered. Target a real Discord member instead.",
	})
}

func playerForceRegisterInvalidUsernameEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Username",
		Description: "That Minecraft username format is not valid. Usernames must be 3-16 characters and contain only letters, numbers, and underscores.",
	})
}

func playerForceRegisterAlreadyRegisteredEmbed(existingUsername string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("That user is already registered as **%s**. No further action is needed.", existingUsername),
		Thumbnail:   helpers.MinecraftBustAvatarURL(existingUsername),
	})
}

func playerForceRegisterNoRanksEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Ranks Configured",
		Description: "This guild has no ranks configured, so the player cannot be given a starting rank or rank role. Configure at least one rank first.",
	})
}

func playerForceRegisterConflictEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Username Taken",
		Description: fmt.Sprintf("The username **%s** is already linked to another player in this guild.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerForceRegisterSuccessEmbed(user *discordgo.User, username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Force Registration Complete",
		Description: fmt.Sprintf(
			"Successfully linked the player's account. This change may take a moment to propagate across all systems.\n\n> **User:** <@%s>\n> **Username:** `%s`",
			user.ID, username,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}
