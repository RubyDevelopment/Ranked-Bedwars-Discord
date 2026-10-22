package general

import (
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
	helpers.Register(PlayerPunishmentsCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerPunishmentsCommand lists the punishment history of a player.
var PlayerPunishmentsCommand = &types.Command{
	Name:          "punishments",
	Description:   "Views the active bans, mutes, and disciplinary history of a player.",
	PrefixAliases: []string{"puns", "hist", "history"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to view punishments for (defaults to you)",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    false,
			},
			{
				Name:        "active",
				Description: "Only show currently active punishments",
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Required:    false,
			},
			{
				Name:        "filter",
				Description: "Only show punishments from the last duration (e.g. 1d, 7d, 1w)",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
				MaxLength:   32,
			},
		},
	},
	Execute: executePlayerPunishmentsCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerPunishmentsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
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
	activeOnly, _ := helpers.OptionBool(i, "active")

	// 2. Validate Target User
	target := helpers.ResolveUserOption(s, i, "user")
	if target == nil || target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerPunishmentsBotTargetEmbed(i))
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerPunishmentsNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerPunishmentsEmbed(i))
	}

	// 4. Fetch & Filter Punishment History
	entries, err := database.Must().Punishments.ByPlayer(i.GuildID, sdk.UintToString(view.Player.ID), false, "", sdk.Page{})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerPunishmentsEmbed(i))
	}

	filtered := helpers.FilterPunishmentHistoryEntries(entries.Data, activeOnly, filterDuration)
	if len(filtered) == 0 {
		if activeOnly {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerPunishmentsEmptyActiveEmbed(i, view.Config.Username))
		}
		if filterInput != "" {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerPunishmentsEmptyFilteredEmbed(i, view.Config.Username, filterInput))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerPunishmentsEmptyEmbed(i, view.Config.Username))
	}

	// 5. Render First Page
	activeFlag := "0"
	if activeOnly {
		activeFlag = "1"
	}
	totalPages := helpers.HistoryTotalPages(len(filtered))
	embed := helpers.BuildPunishmentHistoryEmbed(view.Config.Username, filtered, 0, activeOnly)
	components := helpers.BuildHistoryPageComponents(helpers.HistoryPagePunishments, target.ID, 1, totalPages, []string{activeFlag, filterInput})
	return helpers.EditInteractionComponents(s, i, embed, components)
}
