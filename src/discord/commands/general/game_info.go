package general

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
	helpers.Register(GameInfoCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameInfoCommand Displays deep metadata, rules, and configuration details for a specific match.
var GameInfoCommand = &types.Command{
	Name:          "viewgame",
	Description:   "Displays deep metadata, rules, and configuration details for a specific match.",
	PrefixAliases: []string{"gameinfo", "gi", "vg"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:         "game",
				Description:  "The game number to view (defaults to current game if in game channel)",
				Type:         discordgo.ApplicationCommandOptionInteger,
				Required:     false,
				Autocomplete: true,
			},
			{
				Name:        "match_id",
				Description: "Target Match ID identifier",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Autocomplete: helpers.GameNumberAutocomplete(),
	Execute:      executeGameInfoCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameInfoCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	var game helpers.Game
	var ok bool

	// Resolve the game from the option, falling back to the current game channel
	if gameNumber := helpers.SubOptionInt(i.ApplicationCommandData().Options, "game"); gameNumber > 0 {
		game, ok = helpers.Games.ByNumber(i.GuildID, gameNumber)
		if !ok {
			return helpers.EditInteractionEmbed(s, i, helpers.GameInfoNotFoundEmbed(i, gameNumber))
		}
	} else {
		game, ok = helpers.Games.ByChannel(i.ChannelID)
		if !ok {
			return helpers.EditInteractionEmbed(s, i, helpers.GameInfoNoGameEmbed(i))
		}
	}

	return helpers.EditInteractionEmbed(s, i, helpers.GameInfoOverviewEmbed(i, game))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================
