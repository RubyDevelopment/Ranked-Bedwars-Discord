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
	helpers.Register(PlayerForceTransferCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerForceTransferCommand force-transfers a Minecraft account link between Discord users.
var PlayerForceTransferCommand = &types.Command{
	Name:          "forcetransfer",
	Description:   "Force-transfers a Minecraft account link between Discord users.",
	Requirements:  types.StaffRegistrationRequirements(),
	PrefixAliases: []string{"ftransfer"},
	Ephemeral:     true,
	Cooldown:      5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "old_user",
				Description: "Current linked Discord user",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "new_user",
				Description: "Discord user to receive the link",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Execute: executePlayerForceTransferCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerForceTransferCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target Users
	oldUser := helpers.OptionUser(s, i, "old_user")
	newUser := helpers.OptionUser(s, i, "new_user")

	if oldUser == nil || newUser == nil {
		return helpers.EditInteractionEmbed(s, i, playerForceTransferInvalidTargetEmbed())
	}
	if oldUser.ID == newUser.ID {
		return helpers.EditInteractionEmbed(s, i, playerForceTransferSameUserEmbed())
	}

	// 2. Fetch Source Player Profile
	oldView, err := database.Must().Player.Get(i.GuildID, oldUser.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerForceTransferSourceNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceTransferEmbed())
	}
	if oldView.Config.Username == "" {
		return helpers.EditInteractionEmbed(s, i, playerForceTransferSourceProfileNotFoundEmbed())
	}

	// 3. Check Target Registration Status
	if newView, err := database.Must().Player.Get(i.GuildID, newUser.ID); err == nil && newView.Player.ID != oldView.Player.ID {
		return helpers.EditInteractionEmbed(s, i, playerForceTransferDestinationAlreadyRegisteredEmbed(newUser, newView.Config.Username))
	} else if err != nil && !sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceTransferEmbed())
	}

	// 4. Update Snowflake Link in Database
	username := oldView.Config.Username
	actor := helpers.InteractionUserID(i)
	pid := sdk.UintToString(oldView.Player.ID)

	if _, err := database.Must().Player.SetSnowflake(actor, pid, newUser.ID); err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, playerForceTransferConflictEmbed(newUser))
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceTransferEmbed())
	}

	// 5. Reset Source Member Profile
	if oldMember, _ := helpers.FetchGuildMember(s, i.GuildID, oldUser.ID); oldMember != nil {
		_ = helpers.ResetMemberRegistrationProfile(s, i.GuildID, oldMember)
	}

	// 6. Synchronize Destination Member Profile
	var actions []string
	if newMember, _ := helpers.FetchGuildMember(s, i.GuildID, newUser.ID); newMember != nil {
		syncActions, syncErr := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, oldView.Player.ID, newUser.ID)
		if syncErr != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerForceTransferEmbed())
		}
		actions = syncActions
	}

	return helpers.EditInteractionEmbed(s, i, playerForceTransferSuccessEmbed(oldUser, newUser, username, actions))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerForceTransferEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while transferring the account link. Please try again later or contact support.",
	})
}

func playerForceTransferInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify both the current linked user and the destination user.",
	})
}

func playerForceTransferSameUserEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "The source and destination users must be different Discord accounts.",
	})
}

func playerForceTransferSourceNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "The source user is not registered in this guild. Only registered accounts can be transferred.",
	})
}

func playerForceTransferSourceProfileNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Profile Not Found",
		Description: "The source user's guild profile could not be found. Run `/player_force_sync` or contact staff if the issue persists.",
	})
}

func playerForceTransferDestinationAlreadyRegisteredEmbed(user *discordgo.User, username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("<@%s> is already linked to **%s**. Unregister them first or choose a different target user.", user.ID, username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerForceTransferConflictEmbed(user *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already Registered",
		Description: fmt.Sprintf("<@%s> already has a linked player record, so the transfer cannot overwrite it.", user.ID),
	})
}

func playerForceTransferSuccessEmbed(oldUser, newUser *discordgo.User, username string, actions []string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully transferred the linked profile. This change may take a moment to propagate across all systems.\n\n> **From:** <@%s>\n> **To:** <@%s>\n> **Username:** `%s`",
		oldUser.ID, newUser.ID, username,
	)
	if len(actions) > 0 {
		description += fmt.Sprintf("\n\n**Actions Taken:**\n%s", strings.Join(actions, "\n"))
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Account Transferred",
		Description: description,
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
