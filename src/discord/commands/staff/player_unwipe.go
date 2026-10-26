package staff

import (
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
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
	helpers.Register(PlayerUnwipeCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerUnwipeCommand undoes a stat wipe and restores the player's stats.
var PlayerUnwipeCommand = &types.Command{
	Name:         "unwipe",
	Requirements: types.StaffRequirements,
	Description:  "Undoes a wipe and restores the player's stats.",
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
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerUnwipeInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerUnwipeBotTargetEmbed(i))
	}
	wipeID := helpers.OptionInt(i, "wipe_id")
	actor := helpers.InteractionUserID(i)

	// 2. Fetch Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerUnwipeNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerUnwipeEmbed(i))
	}
	username := view.Config.Username

	// 3. Find the Wipe to Undo (this season's wipes, newest first)
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerUnwipeEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerWipeNoSeasonEmbed(i))
	}
	pid := sdk.UintToString(view.Player.ID)
	wipes, err := database.Must().Wipes.AllByPlayer(i.GuildID, pid, seasonID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerUnwipeEmbed(i))
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
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerUnwipeNothingEmbed(i, username, wipeID))
	case wipe.RestoredAt != nil:
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerUnwipeAlreadyEmbed(i, username, wipe.ID))
	}

	// 4. Restore
	result, err := database.Must().Wipes.Restore(actor, i.GuildID, pid, seasonID, wipe.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerUnwipeNothingEmbed(i, username, wipeID))
		}
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerUnwipeAlreadyEmbed(i, username, wipe.ID))
		}
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerUnwipeEmbed(i))
	}

	// 5. Sync Rank
	newRank, _ := helpers.Elo.SyncRank(actor, i.GuildID, target.ID)
	if newRank == "" {
		newRank = result.Stats.Rank
	}

	embed := cmdcfg.PlayerUnwipeSuccessEmbed(i, username, result, newRank)
	helpers.LogCommandResult(s, i.GuildID, "player_unwipe", actor, embed)
	return helpers.EditInteractionEmbed(s, i, embed)
}
