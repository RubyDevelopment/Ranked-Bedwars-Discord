package general

import (
	"fmt"
	"strings"
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
	Name:        "gameinfo",
	Description: "Displays deep metadata, rules, and configuration details for a specific match.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
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
			return helpers.EditInteractionEmbed(s, i, gameInfoNotFoundEmbed(gameNumber))
		}
	} else {
		game, ok = helpers.Games.ByChannel(i.ChannelID)
		if !ok {
			return helpers.EditInteractionEmbed(s, i, gameInfoNoGameEmbed())
		}
	}

	return helpers.EditInteractionEmbed(s, i, gameInfoOverviewEmbed(game))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// ============================================================================
// Embed Builders
// ============================================================================

func gameInfoNotFoundEmbed(gameNumber int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Game Not Found",
		Description: fmt.Sprintf("Game **#%d** could not be found. It may have already been cleaned up.", gameNumber),
	})
}

func gameInfoNoGameEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Game",
		Description: "You must either run this command in a game channel or specify a game number.",
	})
}

func gameInfoOverviewEmbed(game helpers.Game) *discordgo.MessageEmbed {
	var description strings.Builder

	// Basic info
	description.WriteString(fmt.Sprintf("**Game Number:** #%d\n", game.Number))
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

	// Teams
	if len(game.Teams) > 0 {
		description.WriteString("\n**Teams:**\n")
		for _, teamName := range game.Teams {
			teamPlayers := game.TeamPlayers(teamName)
			playerList := make([]string, len(teamPlayers))
			for idx, p := range teamPlayers {
				captainMark := ""
				if p.Captain {
					captainMark = "👑 "
				}
				playerList[idx] = fmt.Sprintf("%s<@%s>", captainMark, p.UserID)
			}
			description.WriteString(fmt.Sprintf("> **%s:** %s\n", teamName, strings.Join(playerList, ", ")))
		}
	}

	// Custom game info
	if game.Custom != nil {
		description.WriteString("\n**Custom Game:**\n")
		description.WriteString(fmt.Sprintf("> **Ranked:** %s\n", onOff(game.Custom.Ranked)))
		description.WriteString(fmt.Sprintf("> **Stats:** %s\n", onOff(game.Custom.Stats)))
		if game.Custom.HostedBy != "" {
			description.WriteString(fmt.Sprintf("> **Host:** <@%s>\n", game.Custom.HostedBy))
		}
	}

	// Void info
	if game.State == helpers.GameVoided {
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

	// Channels
	if game.Channels.Text != "" {
		description.WriteString("\n**Channels:**\n")
		description.WriteString(fmt.Sprintf("> **Text:** <#%s>\n", game.Channels.Text))
		if len(game.Channels.Voice) > 0 {
			for team, channelID := range game.Channels.Voice {
				description.WriteString(fmt.Sprintf("> **%s Voice:** <#%s>\n", team, channelID))
			}
		}
	}

	// Duration if in game or completed
	if !game.StartedAt.IsZero() {
		var endTime time.Time
		if game.State == helpers.GameCleanup || game.State == helpers.GameVoided {
			endTime = game.StateAt
		} else {
			endTime = time.Now()
		}
		duration := endTime.Sub(game.StartedAt)
		description.WriteString(fmt.Sprintf("\n**Duration:** %s\n", duration.Round(time.Second)))
	}

	return helpers.Preset("info", types.PresetOptions{
		Title:       fmt.Sprintf("Game Information · #%d", game.Number),
		Description: description.String(),
	})
}
