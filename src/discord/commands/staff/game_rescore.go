package staff

import (
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(GameRescoreCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameRescoreCommand rescores a game that has already been completed or voided.
var GameRescoreCommand = &types.Command{
	Name:         "rescore",
	Requirements: types.StaffRequirements,
	Description:  "Recalculates ELO for a completed or voided game after a dispute or correction.",
	Ephemeral:    true,
	Cooldown:     5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:         "game",
				Description:  "Game number to rescore",
				Type:         discordgo.ApplicationCommandOptionInteger,
				Required:     true,
				Autocomplete: true,
			},
			{
				Name:        "winner_team",
				Description: "Winning team (Team A, Team B, or team name)",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
			{
				Name:        "mvp",
				Description: "MVP player",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Autocomplete: helpers.GameNumberAutocomplete(helpers.GameCleanup, helpers.GameVoided),
	Execute:      executeGameRescoreCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameRescoreCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	actor := helpers.InteractionUserID(i)
	opts := i.ApplicationCommandData().Options

	// 1. Validate Options
	gameNumber := helpers.SubOptionInt(opts, "game")
	winnerTeam := helpers.SubOptionString(opts, "winner_team")
	mvpUser := helpers.SubOptionUser(s, opts, "mvp")
	if mvpUser == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameRescoreInvalidMvpEmbed(i))
	}
	if mvpUser.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.GameRescoreBotMvpEmbed(i))
	}

	// 2. Validate the Game
	game, ok := helpers.Games.ByNumber(i.GuildID, gameNumber)
	if !ok {
		return helpers.EditInteractionEmbed(s, i, helpers.GameRescoreNotFoundEmbed(i, gameNumber))
	}
	if game.State != helpers.GameCleanup && game.State != helpers.GameVoided {
		return helpers.EditInteractionEmbed(s, i, helpers.GameRescoreInvalidStateEmbed(i, gameNumber))
	}
	if _, isParticipant := game.Player(mvpUser.ID); !isParticipant {
		return helpers.EditInteractionEmbed(s, i, helpers.GameRescoreMvpNotInMatchEmbed(i, mvpUser))
	}

	// 3. Rescore the Game
	result, err := helpers.Games.Rescore(actor, i.GuildID, game.ID, winnerTeam, mvpUser.ID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.GameActionErrorEmbed("rescore the game", err))
	}

	winners, losers := helpers.SplitResultMentions(result.Results)
	return helpers.EditInteractionEmbed(s, i, helpers.GameRescoreSuccessEmbed(i, gameNumber, winners, losers, mvpUser))
}
