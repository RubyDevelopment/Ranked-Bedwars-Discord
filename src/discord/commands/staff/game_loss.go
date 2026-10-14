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
	helpers.Register(GameLossCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameLossCommand removes loss-equivalent ELO from a registered player without recording a game.
var GameLossCommand = &types.Command{
	Name:        "loss",
	Description: "Removes loss-equivalent ELO from a registered player without recording a game.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
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
		return helpers.EditInteractionEmbed(s, i, gameLossInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, gameLossBotTargetEmbed())
	}

	// 2. Parse Options
	lossCount := helpers.OptionInt(i, "amount")
	countStats, _ := helpers.OptionBool(i, "count_stats")
	actor := helpers.InteractionUserID(i)

	// 3. Fetch Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, gameLossRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameLossEmbed())
	}
	pid := sdk.UintToString(view.Player.ID)

	// 4. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameLossEmbed())
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, gameLossNoActiveSeasonEmbed())
	}

	// 5. Get or Create Stats
	stats, err := database.Must().Stats.GetOrCreate(i.GuildID, pid, seasonID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameLossEmbed())
	}

	// 6. Fetch Rank Ladder
	ranks, err := helpers.GuildMetaRanks(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameLossEmbed())
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
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameLossEmbed())
	}

	// 9. Sync Rank
	newRank, err := helpers.SyncRankForElo(actor, i.GuildID, pid, seasonID, previousRank, updated.Elo)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameLossEmbed())
	}

	// 10. Update Loss Stats if requested
	if countStats {
		if _, err := database.Must().Stats.Increment(actor, i.GuildID, pid, seasonID, map[string]int{"losses": lossCount}); err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameLossEmbed())
		}
	}

	return helpers.EditInteractionEmbed(s, i, gameLossSuccessEmbed(target, view.Config.Username, lossCount, totalEloLoss, previousElo, updated.Elo, previousRank, newRank, countStats, lossDetails))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorGameLossEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while removing ELO from the player. Please try again later or contact support.",
	})
}

func gameLossInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to remove ELO from. Select a registered player from the command options.",
	})
}

func gameLossBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive ELO removals. Target a registered player instead.",
	})
}

func gameLossRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/player_force_register` before removing ELO.",
	})
}

func gameLossNoActiveSeasonEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. ELO can only be removed during an active season. Please contact an administrator to configure a season.",
	})
}

func gameLossSuccessEmbed(user *discordgo.User, username string, lossCount, totalEloLoss, previousElo, newElo int, oldRank, newRank string, countStats bool, lossDetails []string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully removed ELO from <@%s> (**%s**). This action has been logged for staff review.\n\n> **Losses Applied:** `%d`\n> **Total ELO Loss:** `-%d`\n> **Previous ELO:** `%d`\n> **New ELO:** `%d`\n> **Previous Rank:** `%s`\n> **New Rank:** `%s`\n> **Counted Towards Stats:** `%t`",
		user.ID, username, lossCount, totalEloLoss, previousElo, newElo, oldRank, newRank, countStats,
	)

	// Add loss breakdown if there are rank changes
	if len(lossDetails) > 0 {
		description += "\n\n__**ELO Breakdown:**__\n"
		for _, detail := range lossDetails {
			description += fmt.Sprintf("> %s\n", detail)
		}
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       "ELO Removed",
		Description: description,
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
