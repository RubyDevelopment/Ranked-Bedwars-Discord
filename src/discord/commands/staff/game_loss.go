package staff

import (
	"fmt"
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
	helpers.Register(GameLossCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameLossCommand removes loss-equivalent ELO from a registered player without recording a game.
var GameLossCommand = &types.Command{
	Name:         "loss",
	Requirements: types.StaffRequirements,
	Description:  "Removes loss-equivalent ELO from a registered player without recording a game.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "user", Description: "Player to remove ELO from", Type: discordgo.ApplicationCommandOptionUser, Required: true},
			{Name: "amount", Description: "How many losses worth of ELO to remove", Type: discordgo.ApplicationCommandOptionInteger, Required: true, MinValue: ptrToFloat64(1), MaxValue: 25},
			{Name: "count_stats", Description: "Whether to count this towards the player's win/loss statistics", Type: discordgo.ApplicationCommandOptionBoolean, Required: false},
		},
	},
	Execute: executeGameLossCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameLossCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameLossInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.GameLossBotTargetEmbed(i))
	}

	// 2. Parse Options
	lossCount := helpers.OptionInt(i, "amount")
	countStats, _ := helpers.OptionBool(i, "count_stats")
	actor := helpers.InteractionUserID(i)

	// 3. Fetch Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.GameLossRegistrationRequiredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameLossEmbed(i))
	}
	pid := sdk.UintToString(view.Player.ID)

	// 4. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameLossEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.GameLossNoActiveSeasonEmbed(i))
	}

	// 5. Get or Create Stats
	stats, err := database.Must().Stats.GetOrCreate(i.GuildID, pid, seasonID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameLossEmbed(i))
	}

	// 6. Fetch Rank Ladder
	ranks, err := helpers.GuildMetaRanks(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameLossEmbed(i))
	}
	sortedRanks := helpers.SortedRanks(ranks)

	// 7. Apply Losses with Dynamic ELO
	previousElo, previousRank := stats.Elo, stats.Rank
	currentElo := previousElo
	totalEloLoss := 0
	lossDetails := make([]string, 0, lossCount)

	for lossIdx := 0; lossIdx < lossCount; lossIdx++ {
		// Get the rank before applying this loss
		currentRank := helpers.RankForElo(sortedRanks, currentElo)
		kFactor := 25 // Default fallback
		if currentRank != nil && currentRank.KFactorLoss != 0 {
			kFactor = helpers.AbsInt(currentRank.KFactorLoss)
		}

		// Calculate ELO after this loss (clamped at 0)
		newElo := currentElo - kFactor
		if newElo < 0 {
			newElo = 0
		}
		actualLoss := currentElo - newElo

		// Check if this loss crosses a rank threshold
		newRank := helpers.RankForElo(sortedRanks, newElo)
		rankChangeStr := ""
		if newRank != nil && currentRank != nil && newRank.RankName != currentRank.RankName {
			rankChangeStr = fmt.Sprintf(" → %s", newRank.RankName)
		}

		currentElo = newElo
		totalEloLoss += actualLoss

		lossDetails = append(lossDetails, fmt.Sprintf("Loss %d: `-%d` ELO (Rank: `%s%s`)", lossIdx+1, actualLoss, helpers.GetRankName(currentRank), rankChangeStr))
	}

	// 8. Update ELO
	updated, err := helpers.AdjustElo(actor, i.GuildID, pid, seasonID, stats, -totalEloLoss)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameLossEmbed(i))
	}

	// 9. Sync Rank
	newRank, err := helpers.SyncRankForElo(actor, i.GuildID, pid, seasonID, previousRank, updated.Elo)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameLossEmbed(i))
	}

	// 10. Update Loss Stats if requested
	if countStats {
		if _, err := database.Must().Stats.Increment(actor, i.GuildID, pid, seasonID, map[string]int{"losses": lossCount}); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameLossEmbed(i))
		}
	}

	return helpers.EditInteractionEmbed(s, i, helpers.GameLossSuccessEmbed(i, target, view.Config.Username, lossCount, totalEloLoss, previousElo, updated.Elo, previousRank, newRank, countStats, lossDetails))
}
