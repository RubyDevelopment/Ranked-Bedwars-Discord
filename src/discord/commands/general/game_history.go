package general

import (
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
	helpers.Register(GameHistoryCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameHistoryCommand Retrieves the detailed match history and past game outcomes for a player.
var GameHistoryCommand = &types.Command{
	Name:        "games",
	Description: "Retrieves the detailed match history and past game outcomes for a player.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Target player (defaults to you)",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    false,
			},
			{
				Name:        "filter",
				Description: "Only show games from the last duration (e.g. 1d, 7d, 1w)",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
				MaxLength:   32,
			},
			{
				Name:        "hide_voided",
				Description: "Hide voided matches from the history",
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Required:    false,
			},
			{
				Name:        "page",
				Description: "Page number to view",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    false,
			},
		},
	},
	Execute: executeGameHistoryCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameHistoryCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Time Filter
	filterInput := helpers.OptionString(i, "filter")
	var filterDuration time.Duration
	if filterInput != "" {
		parsed, ok := helpers.ParseTimeFilter(filterInput)
		if !ok {
			return helpers.EditInteractionEmbed(s, i, helpers.InvalidTimeFilterEmbed())
		}
		filterDuration = parsed
	}
	hideVoided, _ := helpers.OptionBool(i, "hide_voided")

	// 2. Validate Target User
	target := helpers.ResolveUserOption(s, i, "user")
	if target == nil || target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.GameHistoryBotTargetEmbed(i))
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.GameHistoryNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameHistoryEmbed(i))
	}

	// 4. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameHistoryEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.GameHistoryNoActiveSeasonEmbed(i))
	}

	// 5. Fetch & Filter Game History
	participants, err := database.Must().Player.AllGames(i.GuildID, sdk.UintToString(view.Player.ID))
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorGameHistoryEmbed(i))
	}

	entries := helpers.FilterGameHistoryEntries(participants, seasonID, filterDuration, hideVoided)
	if len(entries) == 0 {
		return helpers.EditInteractionEmbed(s, i, helpers.GameHistoryEmptyEmbed(i, view.Config.Username, filterInput))
	}

	// 6. Render First Page
	voidedFlag := "0"
	if hideVoided {
		voidedFlag = "1"
	}
	totalPages := helpers.HistoryTotalPages(len(entries))
	embed := helpers.BuildGameHistoryEmbed(view.Config.Username, entries, 0)
	components := helpers.BuildHistoryPageComponents(helpers.HistoryPageGames, target.ID, 1, totalPages, []string{filterInput, voidedFlag})
	return helpers.EditInteractionComponents(s, i, embed, components)
}
