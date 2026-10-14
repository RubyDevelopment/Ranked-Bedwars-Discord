package staff

import (
	"fmt"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(PlayerUnwipeCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerUnwipeCommand undoes a stat wipe and restores the player's stats.
var PlayerUnwipeCommand = &types.Command{
	Name:         "unwipe",
	Description:  "Undoes a wipe and restores the player's stats.",
	Requirements: []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}},
	Ephemeral:    true,
	Cooldown:     5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "user", Description: "Player to unwipe", Type: discordgo.ApplicationCommandOptionUser, Required: true},
			{Name: "wipe_id", Description: "Wipe to undo (defaults to the player's latest wipe this season that is still in effect)", Type: discordgo.ApplicationCommandOptionInteger, Required: false, MinValue: ptrToFloat64(1)},
		},
	},
	Execute: executePlayerUnwipeCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerUnwipeCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, playerUnwipeInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, playerUnwipeBotTargetEmbed())
	}
	wipeID := helpers.OptionInt(i, "wipe_id")
	actor := helpers.InteractionUserID(i)

	// 2. Fetch Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerUnwipeNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerUnwipeEmbed())
	}
	username := view.Config.Username

	// 3. Find the Wipe to Undo (this season's wipes, newest first)
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerUnwipeEmbed())
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, playerWipeNoSeasonEmbed())
	}
	pid := sdk.UintToString(view.Player.ID)
	wipes, err := database.Must().Wipes.AllByPlayer(i.GuildID, pid, seasonID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerUnwipeEmbed())
	}
	var wipe *models.PlayerWipe
	for idx := range wipes {
		w := &wipes[idx]
		if (wipeID == 0 && w.RestoredAt == nil) || int(w.ID) == wipeID {
			wipe = w
			break
		}
	}
	switch {
	case wipe == nil:
		return helpers.EditInteractionEmbed(s, i, playerUnwipeNothingEmbed(username, wipeID))
	case wipe.RestoredAt != nil:
		return helpers.EditInteractionEmbed(s, i, playerUnwipeAlreadyEmbed(username, wipe.ID))
	}

	// 4. Restore
	result, err := database.Must().Wipes.Restore(actor, i.GuildID, pid, seasonID, wipe.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerUnwipeNothingEmbed(username, wipeID))
		}
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, playerUnwipeAlreadyEmbed(username, wipe.ID))
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerUnwipeEmbed())
	}

	// 5. Sync Rank
	newRank, _ := helpers.Elo.SyncRank(actor, i.GuildID, target.ID)
	if newRank == "" {
		newRank = result.Stats.Rank
	}

	embed := playerUnwipeSuccessEmbed(username, result, newRank)
	helpers.LogCommandResult(s, i.GuildID, "player_unwipe", actor, embed)
	return helpers.EditInteractionEmbed(s, i, embed)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerUnwipeEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while restoring the stats. Please contact an administrator for assistance or try again later.",
	})
}

func playerUnwipeInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to unwipe. Select a registered player from the command options.",
	})
}

func playerUnwipeBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have stats to restore. Target a registered player instead.",
	})
}

func playerUnwipeNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can have stats restored.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func playerUnwipeNothingEmbed(username string, wipeID int) *discordgo.MessageEmbed {
	description := fmt.Sprintf("**%s** has no wipe to undo.", username)
	if wipeID > 0 {
		description = fmt.Sprintf("**%s** has no wipe with ID **%d**.", username, wipeID)
	}
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Wipe Found",
		Description: description,
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerUnwipeAlreadyEmbed(username string, wipeID uint) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already Restored",
		Description: fmt.Sprintf("Wipe **%d** of **%s** was already undone.", wipeID, username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerUnwipeSuccessEmbed(username string, r *sdk.WipeResult, rank string) *discordgo.MessageEmbed {
	scope := "All stats this season"
	if r.Wipe.FromGameNumber > 0 {
		scope = fmt.Sprintf("Game **#%d** onwards (**%d** games)", r.Wipe.FromGameNumber, r.Wipe.GamesAffected)
	}
	return helpers.Preset("success", types.PresetOptions{
		Title: "Stats Restored",
		Description: fmt.Sprintf(
			"Successfully undid the wipe. This action has been logged for staff review.\n\n> **Player:** `%s`\n> **Restored:** %s\n> **ELO:** `%d`\n> **Rank:** `%s`\n> **Wipe ID:** `%d`",
			username, scope, r.Stats.Elo, rank, r.Wipe.ID,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}
