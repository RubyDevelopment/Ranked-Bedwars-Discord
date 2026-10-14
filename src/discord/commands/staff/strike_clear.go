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
	helpers.Register(StrikeClearCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// StrikeClearCommand voids every active strike on a player's record.
var StrikeClearCommand = &types.Command{
	Name:        "strike_clear",
	Description: "Clears all active strikes from a player's record.",
	Ephemeral:   true,
	Cooldown:    5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to clear all strikes for",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Execute: executeStrikeClearCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeStrikeClearCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, strikeClearInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, strikeClearBotTargetEmbed())
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, strikeClearNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorStrikeClearEmbed())
	}
	actor := helpers.InteractionUserID(i)

	// 3. Collect the Active Strikes
	entries, err := database.Must().Strikes.AllByPlayer(i.GuildID, sdk.UintToString(view.Player.ID), 0, false)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorStrikeClearEmbed())
	}
	var active []uint
	for _, entry := range entries {
		if !entry.IsVoided {
			active = append(active, entry.ID)
		}
	}
	if len(active) == 0 {
		return helpers.EditInteractionEmbed(s, i, strikeClearNoStrikesEmbed(view.Config.Username))
	}

	// 4. Void Each Strike in Database
	cleared := 0
	for _, id := range active {
		if err := database.Must().Strikes.Delete(actor, i.GuildID, id); err != nil && !sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, strikeClearPartialEmbed(cleared, len(active)))
		}
		cleared++
	}

	embed := strikeClearSuccessEmbed(target, view.Config.Username, cleared)
	helpers.LogCommandResult(s, i.GuildID, "strike_clear", actor, embed)
	return helpers.EditInteractionEmbed(s, i, embed)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorStrikeClearEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while clearing the strikes. Please try again later or contact support.",
	})
}

func strikeClearInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to clear strikes for. Select a registered player from the command options.",
	})
}

func strikeClearBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive strikes. Target a registered player instead.",
	})
}

func strikeClearNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can receive strikes.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func strikeClearNoStrikesEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Strikes Found",
		Description: "This player has no active strikes to clear. Their strike history can be reviewed with `/strike_list`.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func strikeClearPartialEmbed(cleared, total int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Strikes Partially Cleared",
		Description: fmt.Sprintf("Only **%d** of **%d** strikes were cleared before an error occurred. Run the command again to clear the rest.", cleared, total),
	})
}

func strikeClearSuccessEmbed(user *discordgo.User, username string, cleared int) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Strikes Cleared",
		Description: fmt.Sprintf(
			"Successfully voided every active strike. This action has been logged for staff review.\n\n> **Player:** <@%s> (**%s**)\n> **Strikes Cleared:** `%d`",
			user.ID, username, cleared,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}
