package staff

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(GameScoreCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameScoreCommand scores a game in progress by marking the winning team.
var GameScoreCommand = &types.Command{
	Name:         "score",
	Requirements: types.StaffRequirements,
	Description:  "Scores a game in progress by marking the winning team.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:         "game",
				Description:  "Game number to score",
				Type:         discordgo.ApplicationCommandOptionInteger,
				Required:     true,
				Autocomplete: true,
			},
			{
				Name:        "winner",
				Description: "Winning team",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    true,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Team A", Value: 0},
					{Name: "Team B", Value: 1},
					{Name: "Team C", Value: 2},
					{Name: "Team D", Value: 3},
				},
			},
		},
	},
	Autocomplete: helpers.GameNumberAutocomplete(helpers.GameInGame, helpers.GamePostGame),
	Execute:      executeGameScoreCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameScoreCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)
	opts := i.ApplicationCommandData().Options

	// 1. Parse Options
	gameNumber := helpers.SubOptionInt(opts, "game")
	teamName := helpers.TeamName(helpers.SubOptionInt(opts, "winner"))

	// 2. Fetch the Game
	game, ok := helpers.Games.ByNumber(i.GuildID, gameNumber)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, gameScoreNotFoundEmbed(gameNumber))
	}

	// 3. Score the Game
	result, err := helpers.Games.Score(actor, i.GuildID, game.ID, teamName, helpers.ScoreOptions{})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("score the game", err))
	}

	return helpers.EditInteractionEmbed(s, i, gameScoreSuccessEmbed(gameNumber, teamName, result.Results))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// splitResultMentions lists the winners and losers of a scored game as mentions.
func splitResultMentions(results []helpers.EloResult) (winners, losers string) {
	var won, lost []string
	for _, r := range results {
		if r.Win {
			won = append(won, fmt.Sprintf("<@%s>", r.UserID))
		} else {
			lost = append(lost, fmt.Sprintf("<@%s>", r.UserID))
		}
	}
	return strings.Join(won, " "), strings.Join(lost, " ")
}

// ============================================================================
// Embed Builders
// ============================================================================

func gameScoreNotFoundEmbed(gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
	})
}

func gameScoreSuccessEmbed(gameNumber int, teamName string, results []helpers.EloResult) *discordgo.MessageEmbed {
	winners, losers := splitResultMentions(results)

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Game Scored",
		Description: fmt.Sprintf("Game **#%d** has been scored. This action has been logged for staff review.\n\n> **Winning Team:** `%s`\n> **Winners:** %s\n> **Losers:** %s", gameNumber, teamName, winners, losers),
	})
}
