package general

import (
	"fmt"
	"strings"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/match"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(GameViewCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameViewCommand Displays deep metadata, rules, and configuration details for a specific match.
var GameViewCommand = &types.Command{
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
	Autocomplete: match.GameNumberAutocomplete(),
	Execute:      executeGameViewCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameViewCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	var game match.Game
	var ok bool

	// Resolve the game from the option, falling back to the current game channel
	if gameNumber := helpers.SubOptionInt(i.ApplicationCommandData().Options, "game"); gameNumber > 0 {
		var err error
		game, ok, err = match.Games.ByNumberOrStored(i.GuildID, gameNumber)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, match.GameActionErrorEmbed("look up the game", err))
		}
		if !ok {
			return helpers.EditInteractionEmbed(s, i, GameViewNotFoundEmbed(i, gameNumber))
		}
	} else {
		game, ok = match.Games.ByChannel(i.ChannelID)
		if !ok {
			return helpers.EditInteractionEmbed(s, i, GameViewNoGameEmbed(i))
		}
	}

	return helpers.EditInteractionEmbed(s, i, GameViewOverviewEmbed(i, game))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// ============================================================================
// Helper functions
// ============================================================================

func GameViewNotFoundEmbed(i *discordgo.InteractionCreate, gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameViewNoGameEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Game",
		Description: "You must either run this command in a game channel or specify a game number.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func GameViewOverviewEmbed(i *discordgo.InteractionCreate, game match.Game) *discordgo.MessageEmbed {
	var description strings.Builder

	// Basic info
	description.WriteString(fmt.Sprintf("**Game Number:** %s\n", helpers.MatchLink(game.GuildID, game.SeasonID, game.Number, game.Total, fmt.Sprintf("#%d", game.Number))))
	description.WriteString(fmt.Sprintf("**Mode:** %s\n", game.Mode.Name))
	description.WriteString(fmt.Sprintf("**State:** %s\n", game.State))
	description.WriteString(fmt.Sprintf("**Map:** %s\n", game.Map))

	// Timing
	if !game.CreatedAt.IsZero() {
		description.WriteString(fmt.Sprintf("**Created:** %s\n", game.CreatedAt.UTC().Format("Jan 2, 2006 15:04")))
	}
	if !game.StartedAt.IsZero() {
		description.WriteString(fmt.Sprintf("**Started:** %s\n", game.StartedAt.UTC().Format("Jan 2, 2006 15:04")))
	}

	// Teams, side by side
	var fields []*discordgo.MessageEmbedField
	for _, teamName := range game.Teams {
		fields = append(fields, &discordgo.MessageEmbedField{Name: teamName, Value: match.TeamStats(game, teamName), Inline: true})
	}

	// Custom game info
	if game.Custom != nil {
		description.WriteString("\n**Custom Game:**\n")
		description.WriteString(fmt.Sprintf("> **Ranked:** %s\n", OnOff(game.Custom.Ranked)))
		description.WriteString(fmt.Sprintf("> **Stats:** %s\n", OnOff(game.Custom.Stats)))
		if game.Custom.HostedBy != "" {
			description.WriteString(fmt.Sprintf("> **Host:** <@%s>\n", game.Custom.HostedBy))
		}
	}

	// Void info
	if game.State == match.GameVoided {
		description.WriteString("\n**Void Information:**\n")
		description.WriteString(fmt.Sprintf("> **Reason:** %s\n", game.VoidReason))
		if game.VoidedBy != "" {
			description.WriteString(fmt.Sprintf("> **Voided By:** <@%s>\n", game.VoidedBy))
		}
	}

	// Winner info
	if game.Winner != "" {
		description.WriteString(fmt.Sprintf("\n**Winner:** %s\n", game.Winner))
	}
	if game.MVP != "" {
		description.WriteString(fmt.Sprintf("**MVP:** <@%s>\n", game.MVP))
	}

	return helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Game Information · #%d", game.Number),
		Description: description.String(),
		Fields:      fields,
		Thumbnail:   helpers.ThumbDoubleSwords,
	})
}

func OnOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
