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
	helpers.Register(PlayerDecaysCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerDecaysCommand Checks ELO decay warnings and inactivity timers for ranked profiles.
var PlayerDecaysCommand = &types.Command{
	Name:         "decays",
	Requirements: types.DecayChannelRequirements,
	Description:  "Checks ELO decay warnings and inactivity timers for ranked profiles.",
	Cooldown:     3 * time.Second,
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
	if target == nil && helpers.OptionUserUnresolved(i, "user") {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerProfileNotRegisteredEmbed(i))
	}
	if target == nil || target.Bot {
		return helpers.EditInteractionEmbed(s, i, DecaysBotTargetEmbed(i))
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, DecaysNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorDecaysEmbed(i))
	}

	// 4. Validate Active Season
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorDecaysEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, DecaysNoActiveSeasonEmbed(i))
	}

	// 5. Fetch & Filter Decay History
	entries, err := database.Must().Decays.AllByPlayer(i.GuildID, sdk.UintToString(view.Player.ID), seasonID, false)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorDecaysEmbed(i))
	}

	filtered := helpers.FilterDecayHistoryEntries(entries, filterDuration)
	if len(filtered) == 0 {
		if filterInput != "" {
			return helpers.EditInteractionEmbed(s, i, DecaysEmptyFilteredEmbed(i, view.Config.Username, filterInput))
		}
		return helpers.EditInteractionEmbed(s, i, DecaysEmptyEmbed(i, view.Config.Username))
	}

	// 6. Render First Page
	totalPages := helpers.HistoryTotalPages(len(filtered))
	embed := helpers.BuildDecayHistoryEmbed(view.Config.Username, filtered, 0)
	components := pagination.BuildHistoryPageComponents(pagination.HistoryPageDecays, target.ID, 1, totalPages, []string{filterInput})
	return helpers.EditInteractionComponents(s, i, embed, components)
}

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorDecaysEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while loading the decay history. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func DecaysBotTargetEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts do not have a decay history. Target a registered player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func DecaysNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Register with `/register` to link a Minecraft account first.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func DecaysNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func DecaysEmptyEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "No Decays Found",
		Description: "No ELO decay was found for this player this season. This player has stayed active.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func DecaysEmptyFilteredEmbed(i *discordgo.InteractionCreate, username, filter string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Decays Empty",
		Description: fmt.Sprintf("No ELO decay was found for this player in the last **%s**. Try a longer filter or remove it.", filter),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
