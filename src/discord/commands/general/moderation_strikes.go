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
	helpers.Register(ModerationStrikesCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationStrikesCommand lists the strike history of a player.
var ModerationStrikesCommand = &types.Command{
	Name:        "strikes",
	Description: "Displays the strike history of a player.",
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
	Execute: executeModerationStrikesCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeModerationStrikesCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
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
	if target == nil && helpers.OptionUserUnresolved(i, "user") {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerProfileNotRegisteredEmbed(i))
	}
	if target == nil || target.Bot {
		return helpers.EditInteractionEmbed(s, i, ModerationStrikesBotTargetEmbed(i))
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, ModerationStrikesNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorModerationStrikesEmbed(i))
	}

	// 4. Fetch & Filter Strike History
	entries, err := database.Must().Strikes.AllByPlayer(i.GuildID, sdk.UintToString(view.Player.ID), 0, false)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorModerationStrikesEmbed(i))
	}

	filtered := helpers.FilterStrikeHistoryEntries(entries, filterDuration)
	if len(filtered) == 0 {
		if filterInput != "" {
			return helpers.EditInteractionEmbed(s, i, ModerationStrikesEmptyFilteredEmbed(i, view.Config.Username, filterInput))
		}
		return helpers.EditInteractionEmbed(s, i, ModerationStrikesEmptyEmbed(i, view.Config.Username))
	}

	// 5. Render First Page
	totalPages := helpers.HistoryTotalPages(len(filtered))
	embed := helpers.BuildStrikeHistoryEmbed(i.GuildID, view.Config.Username, filtered, 0)
	components := pagination.BuildHistoryPageComponents(pagination.HistoryPageStrikes, target.ID, 1, totalPages, []string{filterInput})
	return helpers.EditInteractionComponents(s, i, embed, components)
}

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorModerationStrikesEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the strike history. Please try again later or contact support.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationStrikesBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a strike history. Target a registered player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ModerationStrikesNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/register` to link a Minecraft account first.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func ModerationStrikesEmptyEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Strikes Clear",
		Description: "No strike history was found for this player. This player maintains a clean record.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func ModerationStrikesEmptyFilteredEmbed(i *discordgo.InteractionCreate, username, filter string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Strikes Empty",
		Description: fmt.Sprintf("No strike history was found for this player in the last **%s**. Try a longer filter or remove it.", filter),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
