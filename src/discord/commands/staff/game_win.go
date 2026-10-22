package staff

import (
	"fmt"
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
	helpers.Register(GameWinCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameWinCommand grants win-equivalent ELO to a registered player without recording a game.
var GameWinCommand = &types.Command{
	Name:         "win",
	Requirements: types.StaffRequirements,
	Description:  "Grants win-equivalent ELO to a registered player without recording a game.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "user", Description: "Player to grant ELO to", Type: discordgo.ApplicationCommandOptionUser, Required: true},
			{Name: "amount", Description: "How many wins worth of ELO to grant", Type: discordgo.ApplicationCommandOptionInteger, Required: true, MinValue: ptrToFloat64(1), MaxValue: 25},
			{Name: "count_stats", Description: "Whether to count this towards the player's win/loss statistics", Type: discordgo.ApplicationCommandOptionBoolean, Required: false},
		},
	},
	Execute: executeGameWinCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameWinCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameWinInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.GameWinBotTargetEmbed(i))
	}

	// 2. Parse Options
	winCount := helpers.OptionInt(i, "amount")
	countStats, _ := helpers.OptionBool(i, "count_stats")
	actor := helpers.InteractionUserID(i)

	// 3. Fetch Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.GameWinRegistrationRequiredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameWinEmbed(i))
	}
	pid := sdk.UintToString(view.Player.ID)

	// 4. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameWinEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.GameWinNoActiveSeasonEmbed(i))
	}

	// 5. Get or Create Stats
	stats, err := database.Must().Stats.GetOrCreate(i.GuildID, pid, seasonID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameWinEmbed(i))
	}

	// 6. Fetch Rank Ladder
	ranks, err := helpers.GuildMetaRanks(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameWinEmbed(i))
	}
	sortedRanks := helpers.SortedRanks(ranks)

	// 7. Apply Wins with Dynamic ELO
	previousElo, previousRank := stats.Elo, stats.Rank
	currentElo := previousElo
	totalEloGain := 0
	winDetails := make([]string, 0, winCount)

	for winIdx := 0; winIdx < winCount; winIdx++ {
		// Get the rank before applying this win
		currentRank := helpers.RankForElo(sortedRanks, currentElo)
		kFactor := 25 // Default fallback
		if currentRank != nil && currentRank.KFactorWin > 0 {
			kFactor = currentRank.KFactorWin
		}

		// Calculate ELO after this win
		newElo := currentElo + kFactor

		// Check if this win crosses a rank threshold
		newRank := helpers.RankForElo(sortedRanks, newElo)
		rankChangeStr := ""
		if newRank != nil && currentRank != nil && newRank.RankName != currentRank.RankName {
			rankChangeStr = fmt.Sprintf(" → %s", newRank.RankName)
		}

		currentElo = newElo
		totalEloGain += kFactor

		winDetails = append(winDetails, fmt.Sprintf("Win %d: `+%d` ELO (Rank: `%s%s`)", winIdx+1, kFactor, helpers.GetRankName(currentRank), rankChangeStr))
	}

	// 8. Update ELO
	updated, err := helpers.AdjustElo(actor, i.GuildID, pid, seasonID, stats, totalEloGain)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameWinEmbed(i))
	}

	// 9. Sync Rank
	newRank, err := helpers.SyncRankForElo(actor, i.GuildID, pid, seasonID, previousRank, updated.Elo)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameWinEmbed(i))
	}

	// 10. Update Win Stats if requested
	if countStats {
		if _, err := database.Must().Stats.Increment(actor, i.GuildID, pid, seasonID, map[string]int{"wins": winCount}); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameWinEmbed(i))
		}
	}

	return helpers.EditInteractionEmbed(s, i, helpers.GameWinSuccessEmbed(i, target, view.Config.Username, winCount, totalEloGain, previousElo, updated.Elo, previousRank, newRank, countStats, winDetails))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// ptrToFloat64 returns a pointer to v for option bounds.
func ptrToFloat64(v float64) *float64 {
	return &v
}
