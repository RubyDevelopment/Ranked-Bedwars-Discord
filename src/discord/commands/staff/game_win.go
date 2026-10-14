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
	helpers.Register(GameWinCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameWinCommand grants win-equivalent ELO to a registered player without recording a game.
var GameWinCommand = &types.Command{
	Name:        "win",
	Description: "Grants win-equivalent ELO to a registered player without recording a game.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
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
		return helpers.EditInteractionEmbed(s, i, gameWinInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, gameWinBotTargetEmbed())
	}

	// 2. Parse Options
	winCount := helpers.OptionInt(i, "amount")
	countStats, _ := helpers.OptionBool(i, "count_stats")
	actor := helpers.InteractionUserID(i)

	// 3. Fetch Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, gameWinRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameWinEmbed())
	}
	pid := sdk.UintToString(view.Player.ID)

	// 4. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameWinEmbed())
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, gameWinNoActiveSeasonEmbed())
	}

	// 5. Get or Create Stats
	stats, err := database.Must().Stats.GetOrCreate(i.GuildID, pid, seasonID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameWinEmbed())
	}

	// 6. Fetch Rank Ladder
	ranks, err := helpers.GuildMetaRanks(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameWinEmbed())
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
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameWinEmbed())
	}

	// 9. Sync Rank
	newRank, err := helpers.SyncRankForElo(actor, i.GuildID, pid, seasonID, previousRank, updated.Elo)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameWinEmbed())
	}

	// 10. Update Win Stats if requested
	if countStats {
		if _, err := database.Must().Stats.Increment(actor, i.GuildID, pid, seasonID, map[string]int{"wins": winCount}); err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorGameWinEmbed())
		}
	}

	return helpers.EditInteractionEmbed(s, i, gameWinSuccessEmbed(target, view.Config.Username, winCount, totalEloGain, previousElo, updated.Elo, previousRank, newRank, countStats, winDetails))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// ptrToFloat64 returns a pointer to v for option bounds.
func ptrToFloat64(v float64) *float64 {
	return &v
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorGameWinEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while granting ELO to the player. Please try again later or contact support.",
	})
}

func gameWinInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to grant ELO to. Select a registered player from the command options.",
	})
}

func gameWinBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive ELO grants. Target a registered player instead.",
	})
}

func gameWinRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register them with `/player_force_register` before granting ELO.",
	})
}

func gameWinNoActiveSeasonEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. ELO can only be granted during an active season. Please contact an administrator to configure a season.",
	})
}

func gameWinSuccessEmbed(user *discordgo.User, username string, winCount, totalEloGain, previousElo, newElo int, oldRank, newRank string, countStats bool, winDetails []string) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully granted ELO to <@%s> (**%s**). This action has been logged for staff review.\n\n> **Wins Granted:** `%d`\n> **Total ELO Gain:** `+%d`\n> **Previous ELO:** `%d`\n> **New ELO:** `%d`\n> **Previous Rank:** `%s`\n> **New Rank:** `%s`\n> **Counted Towards Stats:** `%t`",
		user.ID, username, winCount, totalEloGain, previousElo, newElo, oldRank, newRank, countStats,
	)

	// Add win breakdown if there are rank changes
	if len(winDetails) > 0 {
		description += "\n\n__**ELO Breakdown:**__\n"
		for _, detail := range winDetails {
			description += fmt.Sprintf("> %s\n", detail)
		}
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       "ELO Granted",
		Description: description,
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
