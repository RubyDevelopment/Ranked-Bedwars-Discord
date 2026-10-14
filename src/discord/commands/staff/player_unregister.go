package staff

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
	helpers.Register(PlayerUnregisterCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerUnregisterCommand unlinks a player's Minecraft account and clears their guild roles.
var PlayerUnregisterCommand = &types.Command{
	Name:         "unregister",
	Description:  "Unlinks a player's Minecraft account and clears their guild roles.",
	Requirements: []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}},
	Ephemeral:    true,
	Cooldown:     5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to unregister",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Execute: executePlayerUnregisterCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerUnregisterCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, playerUnregisterInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, playerUnregisterBotTargetEmbed())
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerUnregisterNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerUnregisterEmbed())
	}
	if view.Config.Username == "" {
		return helpers.EditInteractionEmbed(s, i, playerUnregisterNotRegisteredEmbed())
	}

	// 3. Remove Registration from Database
	// Remove also clears the guild's stats, strikes, punishments, and decays for this player.
	username := view.Config.Username
	actor := helpers.InteractionUserID(i)
	if err := database.Must().Player.Remove(actor, i.GuildID, sdk.UintToString(view.Player.ID)); err != nil && !sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerUnregisterEmbed())
	}

	// 4. Reset Roles & Nickname
	if member, _ := helpers.FetchGuildMember(s, i.GuildID, target.ID); member != nil {
		_ = helpers.ResetMemberRegistrationProfile(s, i.GuildID, member)
	}

	return helpers.EditInteractionEmbed(s, i, playerUnregisterSuccessEmbed(target, username))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerUnregisterEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while unregistering the player. Please try again later or contact support.",
	})
}

func playerUnregisterInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to unregister. Select a registered player from the command options.",
	})
}

func playerUnregisterBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be unregistered. Target a registered player instead.",
	})
}

func playerUnregisterNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can be unregistered.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func playerUnregisterSuccessEmbed(user *discordgo.User, username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Player Unregistered",
		Description: fmt.Sprintf(
			"Successfully removed the player's registration. Their seasonal stats and profile data for this guild have been cleared.\n\n> **Player:** <@%s>\n> **Username:** `%s`",
			user.ID, username,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}
