package staff

import (
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
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
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerWipeInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerWipeBotTargetEmbed(i))
	}
	fromGame := helpers.OptionInt(i, "from_game")
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	actor := helpers.InteractionUserID(i)

	// 2. Fetch Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerWipeNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerWipeEmbed(i))
	}
	pid := sdk.UintToString(view.Player.ID)
	username := view.Config.Username

	// 3. Validate Active Season & Existing Stats
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerWipeEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerWipeNoSeasonEmbed(i))
	}
	if _, err := database.Must().Stats.Get(i.GuildID, pid, seasonID); err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerWipeNoStatsEmbed(i, username))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerWipeEmbed(i))
	}

	// 4. Wipe (recorded by the database so it can be undone)
	ranks, err := helpers.GuildMetaRanks(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerWipeEmbed(i))
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
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerWipeNoGamesEmbed(i, username, fromGame))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerWipeEmbed(i))
	}

	// 5. Sync Rank (a partial wipe changes the ELO, so the rank and its role follow)
	newRank, _ := helpers.Elo.SyncRank(actor, i.GuildID, target.ID)
	if newRank == "" {
		newRank = result.Stats.Rank
	}

	embed := helpers.PlayerWipeSuccessEmbed(i, username, seasonKey, fromGame, result, newRank)
	helpers.LogCommandResult(s, i.GuildID, "player_wipe", actor, embed)
	return helpers.EditInteractionEmbed(s, i, embed)
}
