package general

import (
	"fmt"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/pagination"
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
	Name:         "games",
	Requirements: types.GameChannelRequirements,
	Description:  "Retrieves the detailed match history and past game outcomes for a player.",
	Cooldown:     3 * time.Second,
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
	if target == nil && helpers.OptionUserUnresolved(i, "user") {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerProfileNotRegisteredEmbed(i))
	}
	if target == nil || target.Bot {
		return helpers.EditInteractionEmbed(s, i, GameHistoryBotTargetEmbed(i))
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, GameHistoryNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorGameHistoryEmbed(i))
	}

	// 4. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorGameHistoryEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, GameHistoryNoActiveSeasonEmbed(i))
	}

	// 5. Fetch & Filter Game History
	participants, err := database.Must().Player.AllGames(i.GuildID, sdk.UintToString(view.Player.ID))
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorGameHistoryEmbed(i))
	}

	entries := helpers.FilterGameHistoryEntries(participants, seasonID, filterDuration, hideVoided)
	if len(entries) == 0 {
		return helpers.EditInteractionEmbed(s, i, GameHistoryEmptyEmbed(i, view.Config.Username, filterInput))
	}

	// 6. Render First Page
	playersByGame, err := helpers.LoadGameHistoryPlayers(database.Must(), i.GuildID, entries, 0)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorGameHistoryEmbed(i))
	}
	voidedFlag := "0"
	if hideVoided {
		voidedFlag = "1"
	}
	totalPages := helpers.HistoryTotalPages(len(entries))
	embed := helpers.BuildGameHistoryEmbedWithPlayers(i.GuildID, view.Config.Username, entries, 0, playersByGame)
	components := pagination.BuildHistoryPageComponents(pagination.HistoryPageGames, target.ID, 1, totalPages, []string{filterInput, voidedFlag})
	return helpers.EditInteractionComponents(s, i, embed, components)
}

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorGameHistoryEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the game history. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameHistoryBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a game history. Target a registered player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameHistoryNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/register` to link a Minecraft account first.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func GameHistoryNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameHistoryEmptyEmbed(i *discordgo.InteractionCreate, username, filter string) *discordgo.MessageEmbed {
	description := "No game history was found for this player in the current season."
	if filter != "" {
		description = fmt.Sprintf("No game history was found for this player in the last **%s**. Try a longer filter or remove it.", filter)
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       "No Games Found",
		Description: description,
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
