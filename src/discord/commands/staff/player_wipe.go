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
	helpers.Register(PlayerWipeCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerWipeCommand wipes a player's stats for the active season, entirely or from a game onwards.
var PlayerWipeCommand = &types.Command{
	Name:         "wipe",
	Requirements: types.StaffRequirements,
	Description:  "Wipes a player's stats for the active season, entirely or from a game onwards.",
	Ephemeral:    true,
	Cooldown:     5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "user", Description: "Player to wipe", Type: discordgo.ApplicationCommandOptionUser, Required: true},
			{Name: "from_game", Description: "Only wipe this game number and every game after it (leave empty to wipe everything)", Type: discordgo.ApplicationCommandOptionInteger, Required: false, MinValue: ptrToFloat64(1)},
			{Name: "reason", Description: "Why the stats are being wiped", Type: discordgo.ApplicationCommandOptionString, Required: false, MaxLength: 200},
		},
	},
	Execute: executePlayerWipeCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerWipeCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, playerWipeInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, playerWipeBotTargetEmbed())
	}
	fromGame := helpers.OptionInt(i, "from_game")
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	actor := helpers.InteractionUserID(i)

	// 2. Fetch Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerWipeNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerWipeEmbed())
	}
	pid := sdk.UintToString(view.Player.ID)
	username := view.Config.Username

	// 3. Validate Active Season & Existing Stats
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerWipeEmbed())
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, playerWipeNoSeasonEmbed())
	}
	if _, err := database.Must().Stats.Get(i.GuildID, pid, seasonID); err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerWipeNoStatsEmbed(username))
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerWipeEmbed())
	}

	// 4. Wipe (recorded by the database so it can be undone)
	ranks, err := helpers.GuildMetaRanks(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerWipeEmbed())
	}
	rankName := "Unranked"
	if r := helpers.RankFor(ranks, helpers.SeasonStartElo); r != nil && r.RankName != "" {
		rankName = r.RankName
	}
	result, err := database.Must().Wipes.Wipe(actor, i.GuildID, pid, seasonID, sdk.WipePlayerStatsRequest{
		FromGameNumber: fromGame, StartElo: helpers.SeasonStartElo, Rank: rankName, Reason: reason,
	})
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerWipeNoGamesEmbed(username, fromGame))
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerWipeEmbed())
	}

	// 5. Sync Rank (a partial wipe changes the ELO, so the rank and its role follow)
	newRank, _ := helpers.Elo.SyncRank(actor, i.GuildID, target.ID)
	if newRank == "" {
		newRank = result.Stats.Rank
	}

	embed := playerWipeSuccessEmbed(username, seasonKey, fromGame, result, newRank)
	helpers.LogCommandResult(s, i.GuildID, "player_wipe", actor, embed)
	return helpers.EditInteractionEmbed(s, i, embed)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerWipeEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while wiping the stats. Please contact an administrator for assistance or try again later.",
	})
}

func playerWipeInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to wipe. Select a registered player from the command options.",
	})
}

func playerWipeBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot have stats wiped. Target a registered player instead.",
	})
}

func playerWipeNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players have seasonal stats to wipe.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func playerWipeNoSeasonEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. An administrator should configure a season first.",
	})
}

func playerWipeNoStatsEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Stats Found",
		Description: fmt.Sprintf("**%s** does not have stats for the active season. The player may need to play a game first.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerWipeNoGamesEmbed(username string, fromGame int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Games To Wipe",
		Description: fmt.Sprintf("**%s** has no scored games from game **#%d** onwards this season, so there is nothing to wipe.", username, fromGame),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerWipeSuccessEmbed(username, season string, fromGame int, r *sdk.WipeResult, rank string) *discordgo.MessageEmbed {
	scope := "All of the player's stats this season"
	if fromGame > 0 {
		scope = fmt.Sprintf("Game **#%d** onwards (**%d** games)", fromGame, r.Wipe.GamesAffected)
	}
	return helpers.Preset("success", types.PresetOptions{
		Title: "Stats Wiped",
		Description: fmt.Sprintf(
			"Successfully wiped the player's seasonal stats. This action has been logged for staff review. Use `/player_unwipe` to undo it.\n\n> **Player:** `%s`\n> **Season:** `%s`\n> **Wiped:** %s\n> **ELO:** `%d` → `%d`\n> **Rank:** `%s`\n> **Wipe ID:** `%d`",
			username, season, scope, r.Wipe.EloBefore, r.Stats.Elo, rank, r.Wipe.ID,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}
