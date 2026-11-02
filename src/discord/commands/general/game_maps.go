package general

import (
	"fmt"
	"strings"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

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
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorMapsEmbed(i))
	}
	modes, err := helpers.GuildMetaModes(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorMapsEmbed(i))
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
		return helpers.EditInteractionEmbed(s, i, EmptyMapsEmbed(i))
	}

	// Get the active mode names for this guild
	targetModes := ActiveModeNames(modes)
	if len(targetModes) == 0 {
		targetModes = []string{"Default"}
	}

	return helpers.EditInteractionEmbed(s, i, MapsRotationEmbed(i, rotation, targetModes))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorMapsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while trying to fetch the registered game maps for this guild. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func EmptyMapsEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Registered Maps",
		Description: "This guild has no registered game maps. If you think this is an error, please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func MapsRotationEmbed(i *discordgo.InteractionCreate, maps []types.Map, targetModes []string) *discordgo.MessageEmbed {
	lines := make([]string, 0, len(maps)+1)
	currentLength := 0

	for _, mapRecord := range maps {
		line := FormatMapDisplayLine(helpers.GuildOf(i), mapRecord, targetModes)
		if currentLength+len(line)+1 > helpers.MaxEmbedDescriptionLen {
			break
		}
		lines = append(lines, line)
		currentLength += len(line) + 1
	}

	modesLine := helpers.FillLayout(helpers.GuildOf(i), helpers.LayoutMapsFooter, map[string]string{"modes": strings.Join(targetModes, ", "), "count": fmt.Sprint(len(maps))})
	if currentLength+len(modesLine)+1 <= helpers.MaxEmbedDescriptionLen {
		lines = append(lines, modesLine)
	}

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Registered Maps",
		Description: fmt.Sprintf("View the status of all (%d) registered game maps for this guild.\n\n%s", len(maps), strings.Join(lines, "\n")),
		Thumbnail:   helpers.ThumbBed,
	})
}

func FormatMapDisplayLine(guildID string, mapRecord types.Map, modes []string) string {
	mapName := strings.TrimSpace(mapRecord.Name)
	if mapName == "" {
		mapName = helpers.FillLayout(guildID, helpers.LayoutMapsUnnamed, nil)
	}

	height := helpers.FillLayout(guildID, helpers.LayoutMapsNoHeight, nil)
	if mapRecord.MapHeight > 0 {
		height = fmt.Sprintf("%d", mapRecord.MapHeight)
	}

	// Each mode can have its own teams on a map; modes that share a list are shown once.
	var segments []string
	for _, mode := range modes {
		teams := mapRecord.TeamsFor(mode)
		list := helpers.FillLayout(guildID, helpers.LayoutMapsNoTeams, nil)
		if len(teams) > 0 {
			list = strings.Join(teams, helpers.FillLayout(guildID, helpers.LayoutMapsTeamSeparator, nil))
		}
		segments = append(segments, helpers.FillLayout(guildID, helpers.LayoutMapsMode, map[string]string{"mode": mode, "teams": list}))
	}
	teamsLine := helpers.FillLayout(guildID, helpers.LayoutMapsNoModes, nil)
	if len(segments) > 0 {
		teamsLine = strings.Join(segments, helpers.LayoutSeparator(guildID, helpers.LayoutMapsModeSeparator))
	}

	return helpers.FillLayout(guildID, helpers.LayoutMapsLine, map[string]string{"name": mapName, "height": height, "teams": teamsLine, "modes": strings.Join(modes, ", ")})
}

// ActiveModeNames lists enabled game mode names for a guild.
func ActiveModeNames(modes []types.Mode) []string {
	names := make([]string, 0, len(modes))
	for _, mode := range modes {
		if mode.IsEnabled && strings.TrimSpace(mode.Name) != "" {
			names = append(names, mode.Name)
		}
	}
	return names
}
