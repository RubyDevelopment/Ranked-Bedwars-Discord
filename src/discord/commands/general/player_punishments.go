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
	helpers.Register(PlayerPunishmentsCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerPunishmentsCommand lists the punishment history of a player.
var PlayerPunishmentsCommand = &types.Command{
	Name:        "punishments",
	Description: "Views the active bans, mutes, and disciplinary history of a player.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
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
		return helpers.EditInteractionEmbed(s, i, playerPunishmentsBotTargetEmbed())
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerPunishmentsNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerPunishmentsEmbed())
	}

	// 4. Fetch & Filter Punishment History
	entries, err := database.Must().Punishments.ByPlayer(i.GuildID, sdk.UintToString(view.Player.ID), false, "", sdk.Page{})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerPunishmentsEmbed())
	}

	filtered := helpers.FilterPunishmentHistoryEntries(entries.Data, activeOnly, filterDuration)
	if len(filtered) == 0 {
		if activeOnly {
			return helpers.EditInteractionEmbed(s, i, playerPunishmentsEmptyActiveEmbed(view.Config.Username))
		}
		if filterInput != "" {
			return helpers.EditInteractionEmbed(s, i, playerPunishmentsEmptyFilteredEmbed(view.Config.Username, filterInput))
		}
		return helpers.EditInteractionEmbed(s, i, playerPunishmentsEmptyEmbed(view.Config.Username))
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

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerPunishmentsEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the punishment history. Please try again later or contact support.",
	})
}

func playerPunishmentsBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a punishment history. Target a registered player instead.",
	})
}

func playerPunishmentsNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/player_register` to link a Minecraft account first.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func playerPunishmentsEmptyEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "History Clear",
		Description: "No punishment history was found for this player. This player maintains a clean record.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerPunishmentsEmptyActiveEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "No Active Punishments",
		Description: "No active punishments were found for this player. This player maintains a clean record.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func playerPunishmentsEmptyFilteredEmbed(username, filter string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "History Empty",
		Description: fmt.Sprintf("No punishment history was found for this player in the last **%s**. Try a longer filter or remove it.", filter),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
