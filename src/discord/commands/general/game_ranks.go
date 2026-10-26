package general

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorRanksEmbed(i))
	}

	sorted := helpers.SortedRanks(ranks)
	if len(sorted) == 0 {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.EmptyRanksEmbed(i))
	}

	content := buildRanksContent(i, sorted)
	return helpers.EditInteractionEmbed(s, i, cmdcfg.RanksOverviewEmbed(i, content))
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
				currentName := helpers.FillLayout(i.GuildID, cmdcfg.LayoutRanksUnranked, nil)
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

				vars := map[string]string{"elo": strconv.Itoa(elo), "rank": currentName}
				layout := cmdcfg.LayoutRanksProgressMax
				if nextRank != nil {
					layout = cmdcfg.LayoutRanksProgress
					vars["next_rank"], vars["next_min_elo"], vars["next_in"] = nextRank.RankName, strconv.Itoa(nextRank.MinElo), strconv.Itoa(nextRank.MinElo-elo)
				}
				description.WriteString(helpers.FillLayout(i.GuildID, layout, vars) + "\n\n")
			}
		}
	}

	if description.Len() == 0 {
		description.WriteString(helpers.FillLayout(i.GuildID, cmdcfg.LayoutRanksGuest, nil) + "\n\n")
	}

	description.WriteString(helpers.FillLayout(i.GuildID, cmdcfg.LayoutRanksHeading, nil) + "\n")
	for _, rank := range sorted {
		maxElo := helpers.FillLayout(i.GuildID, cmdcfg.LayoutRanksNoMax, nil)
		if rank.MaxElo != 0 {
			maxElo = strconv.Itoa(rank.MaxElo)
		}
		roleMention := rank.RoleID
		if strings.TrimSpace(roleMention) != "" {
			roleMention = fmt.Sprintf("<@&%s>", roleMention)
		}
		description.WriteString(helpers.FillLayout(i.GuildID, cmdcfg.LayoutRanksLine, map[string]string{
			"role": roleMention, "name": rank.RankName, "min_elo": strconv.Itoa(rank.MinElo), "max_elo": maxElo,
			"win": strconv.Itoa(rank.KFactorWin), "loss": strconv.Itoa(absInt(rank.KFactorLoss)),
		}) + "\n")
	}

	return description.String()
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
