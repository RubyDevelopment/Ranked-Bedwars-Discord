package general

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
	helpers.Register(StrikeListCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// StrikeListCommand lists the strike history of a player.
var StrikeListCommand = &types.Command{
	Name:        "strike_list",
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
		return helpers.EditInteractionEmbed(s, i, strikeListBotTargetEmbed())
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, strikeListNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorStrikeListEmbed())
	}

	// 4. Fetch & Filter Strike History
	entries, err := database.Must().Strikes.AllByPlayer(i.GuildID, sdk.UintToString(view.Player.ID), 0, false)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorStrikeListEmbed())
	}

	filtered := helpers.FilterStrikeHistoryEntries(entries, filterDuration)
	if len(filtered) == 0 {
		if filterInput != "" {
			return helpers.EditInteractionEmbed(s, i, strikeListEmptyFilteredEmbed(view.Config.Username, filterInput))
		}
		return helpers.EditInteractionEmbed(s, i, strikeListEmptyEmbed(view.Config.Username))
	}

	// 5. Render First Page
	totalPages := helpers.HistoryTotalPages(len(filtered))
	embed := helpers.BuildStrikeHistoryEmbed(view.Config.Username, filtered, 0)
	components := helpers.BuildHistoryPageComponents(helpers.HistoryPageStrikes, target.ID, 1, totalPages, []string{filterInput})
	return helpers.EditInteractionComponents(s, i, embed, components)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorStrikeListEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the strike history. Please try again later or contact support.",
	})
}

func strikeListBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a strike history. Target a registered player instead.",
	})
}

func strikeListNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/player_register` to link a Minecraft account first.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func strikeListEmptyEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Strikes Clear",
		Description: "No strike history was found for this player. This player maintains a clean record.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func strikeListEmptyFilteredEmbed(username, filter string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Strikes Empty",
		Description: fmt.Sprintf("No strike history was found for this player in the last **%s**. Try a longer filter or remove it.", filter),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
