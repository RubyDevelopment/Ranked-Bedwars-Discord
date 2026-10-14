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
	helpers.Register(PlayerDecaysCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerDecaysCommand Checks ELO decay warnings and inactivity timers for ranked profiles.
var PlayerDecaysCommand = &types.Command{
	Name:        "player_decays",
	Description: "Checks ELO decay warnings and inactivity timers for ranked profiles.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "The Discord user to view decays for (defaults to you)",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    false,
			},
			{
				Name:        "filter",
				Description: "Only show decays from the last duration (e.g. 1d, 7d, 1w)",
				Type:        discordgo.ApplicationCommandOptionString,
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
	Execute: executePlayerDecaysCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerDecaysCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
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
		return helpers.EditInteractionEmbed(s, i, decaysBotTargetEmbed())
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, decaysNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorDecaysEmbed())
	}

	// 4. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorDecaysEmbed())
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, decaysNoActiveSeasonEmbed())
	}

	// 5. Fetch & Filter Decay History
	entries, err := database.Must().Decays.AllByPlayer(i.GuildID, sdk.UintToString(view.Player.ID), seasonID, false)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorDecaysEmbed())
	}

	filtered := helpers.FilterDecayHistoryEntries(entries, filterDuration)
	if len(filtered) == 0 {
		if filterInput != "" {
			return helpers.EditInteractionEmbed(s, i, decaysEmptyFilteredEmbed(view.Config.Username, filterInput))
		}
		return helpers.EditInteractionEmbed(s, i, decaysEmptyEmbed(view.Config.Username))
	}

	// 6. Render First Page
	totalPages := helpers.HistoryTotalPages(len(filtered))
	embed := helpers.BuildDecayHistoryEmbed(view.Config.Username, filtered, 0)
	components := helpers.BuildHistoryPageComponents(helpers.HistoryPageDecays, target.ID, 1, totalPages, []string{filterInput})
	return helpers.EditInteractionComponents(s, i, embed, components)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorDecaysEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the decay history. Please contact an administrator for assistance or try again later.",
	})
}

func decaysBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a decay history. Target a registered player instead.",
	})
}

func decaysNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/player_register` to link a Minecraft account first.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func decaysNoActiveSeasonEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
	})
}

func decaysEmptyEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "No Decays Found",
		Description: "No ELO decay was found for this player this season. This player has stayed active.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func decaysEmptyFilteredEmbed(username, filter string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Decays Empty",
		Description: fmt.Sprintf("No ELO decay was found for this player in the last **%s**. Try a longer filter or remove it.", filter),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
