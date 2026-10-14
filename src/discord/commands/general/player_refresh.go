package general

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
	helpers.Register(PlayerRefreshCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerRefreshCommand synchronizes the caller's registered role, rank role, and nickname.
var PlayerRefreshCommand = &types.Command{
	Name:        "refresh",
	Description: "Synchronizes your registered role, rank role, and nickname with the guild.",
	Ephemeral:   true,
	Cooldown:    5 * time.Second,
	Data:        &discordgo.ApplicationCommand{},
	Execute:     executePlayerRefreshCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerRefreshCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := helpers.InteractionUserID(i)

	// 1. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerRefreshRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerRefreshEmbed())
	}

	// 2. Validate Guild Membership
	member, err := helpers.FetchGuildMember(s, i.GuildID, userID)
	if err != nil || member == nil {
		return helpers.EditInteractionEmbed(s, i, playerRefreshMemberNotFoundEmbed())
	}

	// 3. Synchronize Roles & Nickname
	actions, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, userID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerRefreshEmbed())
	}
	if len(actions) == 0 {
		return helpers.EditInteractionEmbed(s, i, playerRefreshUpToDateEmbed(view.Config.Username))
	}
	return helpers.EditInteractionEmbed(s, i, playerRefreshSuccessEmbed(view.Config.Username, actions))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerRefreshEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while synchronizing your profile. Please contact an administrator for assistance or try again later.",
	})
}

func playerRefreshRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must register before you can refresh your profile. Run `/player_register` to link your Minecraft account first.",
	})
}

func playerRefreshMemberNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: "Your guild membership could not be found. Verify you are still a member of this guild and try again.",
	})
}

func playerRefreshUpToDateEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Profile Synchronized",
		Description: fmt.Sprintf("Your profile for **%s** is already up to date. No further changes are needed.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerRefreshSuccessEmbed(username string, actions []string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Account Synchronized",
		Description: fmt.Sprintf(
			"Successfully refreshed your roles and nickname.\n\n> **Username:** `%s`\n\n%s",
			username, strings.Join(actions, "\n"),
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}
