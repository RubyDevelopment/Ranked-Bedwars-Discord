package staff

import (
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"
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
	helpers.Register(GameModifyCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameModifyCommand manually adjusts one of a player's seasonal stats.
var GameModifyCommand = &types.Command{
	Name:         "modify",
	Requirements: types.StaffRequirements,
	Description:  "Manually adjusts a player's seasonal statistics.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "add",
				Description: "Add to a player's statistics",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "User to adjust", Type: discordgo.ApplicationCommandOptionUser, Required: true},
					{Name: "stat", Description: "Statistic to adjust", Type: discordgo.ApplicationCommandOptionString, Required: true, Choices: modifyStatChoices()},
					{Name: "value", Description: "Stat value", Type: discordgo.ApplicationCommandOptionInteger, Required: true, MinValue: ptrToFloat64(0)},
				},
			},
			{
				Name:        "remove",
				Description: "Remove from a player's statistics",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "User to adjust", Type: discordgo.ApplicationCommandOptionUser, Required: true},
					{Name: "stat", Description: "Statistic to adjust", Type: discordgo.ApplicationCommandOptionString, Required: true, Choices: modifyStatChoices()},
					{Name: "value", Description: "Stat value", Type: discordgo.ApplicationCommandOptionInteger, Required: true, MinValue: ptrToFloat64(0)},
				},
			},
			{
				Name:        "set",
				Description: "Set a player's statistics",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "User to adjust", Type: discordgo.ApplicationCommandOptionUser, Required: true},
					{Name: "stat", Description: "Statistic to adjust", Type: discordgo.ApplicationCommandOptionString, Required: true, Choices: modifyStatChoices()},
					{Name: "value", Description: "Stat value", Type: discordgo.ApplicationCommandOptionInteger, Required: true, MinValue: ptrToFloat64(0)},
				},
			},
		},
	},
	Execute: executeGameModifyCommand,
}

// modifyStatLabels maps each stat key to its display label.
var modifyStatLabels = map[string]string{
	"elo":                "ELO Rating",
	"highest_elo":        "Highest ELO",
	"wins":               "Wins",
	"losses":             "Losses",
	"kills":              "Kills",
	"deaths":             "Deaths",
	"final_kills":        "Final Kills",
	"first_bloods":       "First Bloods",
	"beds_destroyed":     "Beds Destroyed",
	"beds_lost":          "Beds Lost",
	"assists":            "Assists",
	"mvp_count":          "MVP Count",
	"games_played":       "Games Played",
	"win_streak":         "Win Streak",
	"highest_win_streak": "Highest Win Streak",
	"loss_streak":        "Loss Streak",
}

// modifyStatKeys keeps the choice list in a stable display order.
var modifyStatKeys = []string{
	"elo", "highest_elo", "wins", "losses", "kills", "deaths", "final_kills", "first_bloods",
	"beds_destroyed", "beds_lost", "assists", "mvp_count", "games_played", "win_streak",
	"highest_win_streak", "loss_streak",
}

func modifyStatChoices() []*discordgo.ApplicationCommandOptionChoice {
	choices := make([]*discordgo.ApplicationCommandOptionChoice, len(modifyStatKeys))
	for idx, key := range modifyStatKeys {
		choices[idx] = &discordgo.ApplicationCommandOptionChoice{Name: modifyStatLabels[key], Value: key}
	}
	return choices
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameModifyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Subcommand
	subcommand, options := types.SelectedSubcommand(i)
	if subcommand == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.GameModifyInvalidSubcommandEmbed(i))
	}

	// 2. Validate Target User
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameModifyInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.GameModifyBotTargetEmbed(i))
	}

	// 3. Validate Stat Key
	statKey := helpers.SubOptionString(options, "stat")
	statLabel := modifyStatLabels[statKey]
	if statLabel == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.GameModifyUnknownStatEmbed(i))
	}
	value := helpers.SubOptionInt(options, "value")
	actor := helpers.InteractionUserID(i)

	// 4. Fetch Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.GameModifyRegistrationRequiredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameModifyEmbed(i))
	}
	pid := sdk.UintToString(view.Player.ID)

	// 5. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameModifyEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.GameModifyNoActiveSeasonEmbed(i))
	}

	// 6. Get or Create Stats
	stats, err := database.Must().Stats.GetOrCreate(i.GuildID, pid, seasonID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameModifyEmbed(i))
	}

	// 7. Apply Stat Modification
	currentVal := getStatValue(stats, statKey)
	previousRank := stats.Rank
	var newVal int

	if statKey == "elo" {
		var updated *models.PlayerStatsModel
		switch subcommand {
		case "set":
			absolute := value
			if absolute < 0 {
				absolute = 0
			}
			updated, err = database.Must().Stats.SetElo(actor, i.GuildID, pid, seasonID, absolute)
		case "remove":
			updated, err = helpers.AdjustElo(actor, i.GuildID, pid, seasonID, stats, -value)
		default: // add
			updated, err = helpers.AdjustElo(actor, i.GuildID, pid, seasonID, stats, value)
		}
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameModifyEmbed(i))
		}
		newVal = updated.Elo
		if _, rankErr := helpers.SyncRankForElo(actor, i.GuildID, pid, seasonID, previousRank, newVal); rankErr != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameModifyEmbed(i))
		}
	} else if subcommand == "set" {
		absolute := value
		if absolute < 0 {
			absolute = 0
		}
		updated, err := database.Must().Stats.Patch(actor, i.GuildID, pid, seasonID, map[string]any{statKey: absolute})
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameModifyEmbed(i))
		}
		newVal = getStatValue(updated, statKey)
	} else {
		delta := value
		if subcommand == "remove" {
			delta = -min(value, max(currentVal, 0)) // counters never go below zero
		}
		updated, err := database.Must().Stats.Increment(actor, i.GuildID, pid, seasonID, map[string]int{statKey: delta})
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameModifyEmbed(i))
		}
		newVal = getStatValue(updated, statKey)
	}

	return helpers.EditInteractionEmbed(s, i, helpers.GameModifySuccessEmbed(i, statLabel, target, view.Config.Username, subcommand, newVal, currentVal))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// getStatValue reads a single seasonal stat from the model by its key.
func getStatValue(stats *models.PlayerStatsModel, statKey string) int {
	if stats == nil {
		return 0
	}
	switch statKey {
	case "elo":
		return stats.Elo
	case "highest_elo":
		return stats.HighestElo
	case "wins":
		return stats.Wins
	case "losses":
		return stats.Losses
	case "kills":
		return stats.Kills
	case "deaths":
		return stats.Deaths
	case "final_kills":
		return stats.FinalKills
	case "first_bloods":
		return stats.FirstBloods
	case "beds_destroyed":
		return stats.BedsDestroyed
	case "beds_lost":
		return stats.BedsLost
	case "assists":
		return stats.Assists
	case "mvp_count":
		return stats.MvpCount
	case "games_played":
		return stats.GamesPlayed
	case "win_streak":
		return stats.WinStreak
	case "highest_win_streak":
		return stats.HighestWinStreak
	case "loss_streak":
		return stats.LossStreak
	default:
		return 0
	}
}
