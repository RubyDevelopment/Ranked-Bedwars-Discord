package general

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
	helpers.Register(GameRanksCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameRanksCommand Outlines the competitive tier system, ELO brackets, and rank requirements.
var GameRanksCommand = &types.Command{
	Name:        "ranks",
	Description: "Outlines the competitive tier system, ELO brackets, and rank requirements.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data:        &discordgo.ApplicationCommand{},
	Execute:     executeGameRanksCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameRanksCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ranks, err := helpers.GuildMetaRanks(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorRanksEmbed())
	}

	sorted := helpers.SortedRanks(ranks)
	if len(sorted) == 0 {
		return helpers.EditInteractionEmbed(s, i, emptyRanksEmbed())
	}

	content := buildRanksContent(i, sorted)
	return helpers.EditInteractionEmbed(s, i, ranksOverviewEmbed(content))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

func buildRanksContent(i *discordgo.InteractionCreate, sorted []types.Rank) string {
	var description strings.Builder

	executorID := helpers.InteractionUserID(i)
	view, err := database.Must().Player.Get(i.GuildID, executorID)

	if err == nil && view != nil {
		seasonKey, seasonID, seasonErr := helpers.ActiveSeasonKey(i.GuildID)
		if seasonErr == nil && seasonKey != "" {
			stats, statsErr := database.Must().Stats.GetOrCreate(i.GuildID, sdk.UintToString(view.Player.ID), seasonID)
			if statsErr == nil && stats != nil {
				elo := stats.Elo
				current := helpers.RankForElo(sorted, elo)
				currentName := "Unranked"
				if current != nil {
					currentName = current.RankName
				}

				var nextRank *types.Rank
				for idx := range sorted {
					if sorted[idx].MinElo > elo {
						nextRank = &sorted[idx]
						break
					}
				}

				if nextRank != nil {
					description.WriteString(fmt.Sprintf("**Your ELO:** `%d`\n**Your Rank:** `%s` **Next Rank:** `%s` (in `%d` ELO)\n\n",
						elo, currentName, nextRank.RankName, nextRank.MinElo-elo))
				} else {
					description.WriteString(fmt.Sprintf("**Your ELO:** `%d`\n**Your Rank:** `%s`\nYou have achieved maximum competitive rank status!\n\n",
						elo, currentName))
				}
			}
		}
	}

	if description.Len() == 0 {
		description.WriteString("Use `/player_register` to track your seasonal elo and rank progression!\n\n")
	}

	description.WriteString("__**Available Ranks:**__\n")
	for _, rank := range sorted {
		maxElo := "∞"
		if rank.MaxElo != 0 {
			maxElo = fmt.Sprintf("%d", rank.MaxElo)
		}
		roleMention := rank.RoleID
		if strings.TrimSpace(roleMention) != "" {
			roleMention = fmt.Sprintf("<@&%s>", roleMention)
		}
		description.WriteString(fmt.Sprintf("%s — ELO: `%d - %s` | Win: `+%d` | Loss: `-%d`\n",
			roleMention, rank.MinElo, maxElo, rank.KFactorWin, absInt(rank.KFactorLoss)))
	}

	return description.String()
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorRanksEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the registered ranks for this guild. Please contact an administrator for assistance or try again later.",
	})
}

func emptyRanksEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Configured Ranks",
		Description: "This guild has no configured rank tiers. If you think this is an error, please contact an administrator for assistance or try again later.",
	})
}

func ranksOverviewEmbed(content string) *discordgo.MessageEmbed {
	header := "View the status of all registered game ranks for this guild.\n\n"
	fullDescription := header + content

	if len(fullDescription) > 2000 {
		fullDescription = fullDescription[:1997] + "..."
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Registered Ranks",
		Description: fullDescription,
	})
}
