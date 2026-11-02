package staff

import (
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/ratings"
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
		return helpers.EditInteractionEmbed(s, i, PlayerWipeInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, PlayerWipeBotTargetEmbed(i))
	}
	fromGame := helpers.OptionInt(i, "from_game")
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	actor := helpers.InteractionUserID(i)

	// 2. Fetch Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, PlayerWipeNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPlayerWipeEmbed(i))
	}
	pid := sdk.UintToString(view.Player.ID)
	username := view.Config.Username

	// 3. Validate Active Season & Existing Stats
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPlayerWipeEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerWipeNoSeasonEmbed(i))
	}
	if _, err := database.Must().Stats.Get(i.GuildID, pid, seasonID); err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, PlayerWipeNoStatsEmbed(i, username))
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPlayerWipeEmbed(i))
	}

	// 4. Wipe (recorded by the database so it can be undone)
	ranks, err := helpers.GuildMetaRanks(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPlayerWipeEmbed(i))
	}
	rankName := "Unranked"
	if r := ratings.RankFor(ranks, ratings.SeasonStartElo); r != nil && r.RankName != "" {
		rankName = r.RankName
	}
	result, err := database.Must().Wipes.Wipe(actor, i.GuildID, pid, seasonID, sdk.WipePlayerStatsRequest{
		FromGameNumber: fromGame, StartElo: ratings.SeasonStartElo, Rank: rankName, Reason: reason,
	})
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, PlayerWipeNoGamesEmbed(i, username, fromGame))
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPlayerWipeEmbed(i))
	}

	// 5. Sync Rank (a partial wipe changes the ELO, so the rank and its role follow)
	newRank, _ := ratings.Elo.SyncRank(actor, i.GuildID, target.ID)
	// A wipe and an unwipe write the rank themselves, so SyncRank sees no change and leaves the rank role and nickname
	// alone: refresh them explicitly.
	ratings.RefreshRankRoles(i.GuildID, target.ID)
	if newRank == "" {
		newRank = result.Stats.Rank
	}

	embed := PlayerWipeSuccessEmbed(i, username, seasonKey, fromGame, result, newRank)
	helpers.LogCommandResult(s, i.GuildID, "player_wipe", actor, embed)
	return helpers.EditInteractionEmbed(s, i, embed)
}

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorPlayerWipeEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while wiping the stats. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerWipeInvalidTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to wipe. Select a registered player from the command options.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerWipeBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot have stats wiped. Target a registered player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PlayerWipeNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players have seasonal stats to wipe.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func PlayerWipeNoStatsEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Stats Found",
		Description: fmt.Sprintf("**%s** does not have stats for the active season. The player may need to play a game first.", username),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func PlayerWipeNoGamesEmbed(i *discordgo.InteractionCreate, username string, fromGame int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Games To Wipe",
		Description: fmt.Sprintf("**%s** has no scored games from game **#%d** onwards this season, so there is nothing to wipe.", username, fromGame),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func PlayerWipeSuccessEmbed(i *discordgo.InteractionCreate, username, season string, fromGame int, r *sdk.WipeResult, rank string) *discordgo.MessageEmbed {
	scope := "All of the player's stats this season"
	if fromGame > 0 {
		scope = fmt.Sprintf("Game **#%d** onwards (**%d** games)", fromGame, r.Wipe.GamesAffected)
	}
	return helpers.Preset("success", types.PresetOptions{
		Title: "Stats Wiped",
		Description: fmt.Sprintf(
			"Successfully wiped the player's seasonal stats. This action has been logged for staff review. Use `/unwipe` to undo it.\n\n> **Player:** `%s`\n> **Season:** `%s`\n> **Wiped:** %s\n> **ELO:** `%d` → `%d`\n> **Rank:** `%s`\n> **Wipe ID:** `%d`",
			username, season, scope, r.Wipe.EloBefore, r.Stats.Elo, rank, r.Wipe.ID,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}
