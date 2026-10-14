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

const maxEmbedDescriptionLen = 2000

func init() {
	helpers.Register(GameMapsCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// GameMapsCommand Lists active competitive map pools and map rotation info.
var GameMapsCommand = &types.Command{
	Name:        "maps",
	Description: "Lists active competitive map pools and map rotation info.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data:        &discordgo.ApplicationCommand{},
	Execute:     executeGameMapsCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeGameMapsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// Fetch the registered maps & modes for this guild
	maps, err := helpers.GuildMetaMaps(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorMapsEmbed())
	}
	modes, err := helpers.GuildMetaModes(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorMapsEmbed())
	}

	// Filter the maps that are enabled and available for rotation
	rotation := make([]types.Map, 0, len(maps))
	for _, mapRecord := range maps {
		if mapRecord.IsEnabled && mapRecord.IsMapAvailable {
			rotation = append(rotation, mapRecord)
		}
	}

	// If no maps are in rotation, return an error embed
	if len(rotation) == 0 {
		return helpers.EditInteractionEmbed(s, i, emptyMapsEmbed())
	}

	// Get the active mode names for this guild
	targetModes := helpers.ActiveModeNames(modes)
	if len(targetModes) == 0 {
		targetModes = []string{"Default"}
	}

	return helpers.EditInteractionEmbed(s, i, mapsRotationEmbed(rotation, targetModes))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

func formatMapDisplayLine(mapRecord types.Map, modes []string) string {
	mapName := strings.TrimSpace(mapRecord.Name)
	if mapName == "" {
		mapName = "Unnamed Map"
	}

	height := "N/A"
	if mapRecord.MapHeight > 0 {
		height = fmt.Sprintf("%d", mapRecord.MapHeight)
	}

	// Each mode can have its own teams on a map; modes that share a list are shown once.
	var segments []string
	for _, mode := range modes {
		teams := mapRecord.TeamsFor(mode)
		if len(teams) == 0 {
			segments = append(segments, fmt.Sprintf("**%s** • `Not configured`", mode))
			continue
		}
		segments = append(segments, fmt.Sprintf("**%s** • `%s`", mode, strings.Join(teams, " & ")))
	}
	teamsLine := "Teams: `Not configured`"
	if len(segments) > 0 {
		teamsLine = strings.Join(segments, ", ")
	}

	return fmt.Sprintf("**%s** • Max Height: `%s`\n> %s", mapName, height, teamsLine)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorMapsEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the registered game maps for this guild. Please contact an administrator for assistance or try again later.",
	})
}

func emptyMapsEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Registered Maps",
		Description: "This guild has no registered game maps. If you think this is an error, please contact an administrator for assistance or try again later.",
	})
}

func mapsRotationEmbed(maps []types.Map, targetModes []string) *discordgo.MessageEmbed {
	lines := make([]string, 0, len(maps)+1)
	currentLength := 0

	for _, mapRecord := range maps {
		line := formatMapDisplayLine(mapRecord, targetModes)
		if currentLength+len(line)+1 > maxEmbedDescriptionLen {
			break
		}
		lines = append(lines, line)
		currentLength += len(line) + 1
	}

	modesLine := fmt.Sprintf("*Active modes: `%s`*", strings.Join(targetModes, ", "))
	if currentLength+len(modesLine)+1 <= maxEmbedDescriptionLen {
		lines = append(lines, modesLine)
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Registered Maps",
		Description: fmt.Sprintf("View the status of all (%d) registered game maps for this guild.\n\n%s", len(maps), strings.Join(lines, "\n")),
	})
}
