package general

import (
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
	helpers.Register(StrikeListCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// StrikeListCommand lists the strike history of a player.
var StrikeListCommand = &types.Command{
	Name:        "strikes",
	Description: "Displays the strike history of a player.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to view strikes for (defaults to you)",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    false,
			},
			{
				Name:        "filter",
				Description: "Only show strikes from the last duration (e.g. 1d, 7d, 1w)",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
				MaxLength:   32,
			},
		},
	},
	Execute: executeStrikeListCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeStrikeListCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
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

	// 2. Validate Target User
	target := helpers.ResolveUserOption(s, i, "user")
	if target == nil || target.Bot {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeListBotTargetEmbed(i))
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeListNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorStrikeListEmbed(i))
	}

	// 4. Fetch & Filter Strike History
	entries, err := database.Must().Strikes.AllByPlayer(i.GuildID, sdk.UintToString(view.Player.ID), 0, false)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorStrikeListEmbed(i))
	}

	filtered := helpers.FilterStrikeHistoryEntries(entries, filterDuration)
	if len(filtered) == 0 {
		if filterInput != "" {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeListEmptyFilteredEmbed(i, view.Config.Username, filterInput))
		}
		return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeListEmptyEmbed(i, view.Config.Username))
	}

	// 5. Render First Page
	totalPages := helpers.HistoryTotalPages(len(filtered))
	embed := helpers.BuildStrikeHistoryEmbed(view.Config.Username, filtered, 0)
	components := helpers.BuildHistoryPageComponents(helpers.HistoryPageStrikes, target.ID, 1, totalPages, []string{filterInput})
	return helpers.EditInteractionComponents(s, i, embed, components)
}
