package general

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/pagination"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(GameMapsCommand)
	helpers.RegisterComponentHandler(handleGameMapsPage)
}

const gameMapsPrefix = "gmpg|" // gmpg|<page to go to>; an inert button is gmpg|off-<slot>

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
	embed, components, err := mapsPage(i, 1)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorMapsEmbed(i))
	}
	if embed == nil {
		return helpers.EditInteractionEmbed(s, i, EmptyMapsEmbed(i))
	}
	return helpers.EditInteractionComponents(s, i, embed, components)
}

// mapsPage builds one page of the map list with its page buttons. It returns a nil embed when no maps are in rotation.
func mapsPage(i *discordgo.InteractionCreate, page int) (*discordgo.MessageEmbed, []discordgo.MessageComponent, error) {
	maps, err := helpers.GuildMetaMaps(i.GuildID)
	if err != nil {
		return nil, nil, err
	}
	modes, err := helpers.GuildMetaModes(i.GuildID)
	if err != nil {
		return nil, nil, err
	}

	// Only maps that are enabled and available for rotation are listed
	rotation := make([]types.Map, 0, len(maps))
	targetModes := ActiveModeNames(modes)
	if len(targetModes) == 0 {
		targetModes = []string{"Default"}
	}
	// A map is listed when it is in rotation for at least one queue
	for _, mapRecord := range maps {
		for _, mode := range targetModes {
			if eff := mapRecord.For(mode); eff.IsEnabled && eff.IsMapAvailable {
				rotation = append(rotation, mapRecord)
				break
			}
		}
	}
	if len(rotation) == 0 {
		return nil, nil, nil
	}

	pages := mapPages(helpers.GuildOf(i), rotation, targetModes)
	page = min(max(page, 1), len(pages))
	embed := MapsRotationEmbed(i, len(rotation), pages[page-1], targetModes)
	components := pagination.BuildPageRow(page, len(pages),
		func(target int) string { return gameMapsPrefix + strconv.Itoa(target) },
		func(slot string) string { return gameMapsPrefix + "off-" + slot })
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	return embed, components, nil
}

// mapPages splits the map lines into pages that each fit in an embed description.
func mapPages(guildID string, maps []types.Map, modes []string) [][]string {
	const budget = helpers.MaxEmbedDescriptionLen - 300 // room for the intro and footer line
	pages := [][]string{{}}
	length := 0
	for _, mapRecord := range maps {
		line := FormatMapDisplayLine(guildID, mapRecord, modes)
		if cur := pages[len(pages)-1]; len(cur) > 0 && length+len(line)+1 > budget {
			pages = append(pages, []string{})
			length = 0
		}
		pages[len(pages)-1] = append(pages[len(pages)-1], line)
		length += len(line) + 1
	}
	return pages
}

// handleGameMapsPage turns the page of the map list. Only the player who ran the command can.
func handleGameMapsPage(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	rest, ok := strings.CutPrefix(customID, gameMapsPrefix)
	if !ok {
		return false
	}
	page, err := strconv.Atoi(rest)
	if err != nil || page < 1 {
		return true // an inert button: nothing to do
	}
	if factionOwnerOnly(s, i, "/maps") {
		return true
	}
	embed, components, err := mapsPage(i, page)
	if err != nil || embed == nil {
		embed, components = UnexpectedErrorMapsEmbed(i), []discordgo.MessageComponent{}
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: components},
	})
	return true
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

func MapsRotationEmbed(i *discordgo.InteractionCreate, total int, lines []string, targetModes []string) *discordgo.MessageEmbed {
	lines = append(append([]string{}, lines...), helpers.FillLayout(helpers.GuildOf(i), helpers.LayoutMapsFooter, map[string]string{"modes": strings.Join(targetModes, ", "), "count": fmt.Sprint(total)}))

	return helpers.Preset("success", types.PresetOptions{
		Title:       "Registered Maps",
		Description: fmt.Sprintf("View the status of all (%d) registered game maps for this guild.\n\n%s", total, strings.Join(lines, "\n")),
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

	// Modes that share a team list are shown together. The most common list covers "All queues" (or "All other queues"
	// when some queue differs); each differing queue is then named with its own teams.
	type group struct {
		list  string
		modes []string
	}
	var groups []*group
	for _, mode := range modes {
		eff := mapRecord.For(mode)
		list := helpers.FillLayout(guildID, helpers.LayoutMapsNoTeams, nil)
		if teams := mapRecord.TeamsFor(mode); len(teams) > 0 {
			list = strings.Join(teams, helpers.FillLayout(guildID, helpers.LayoutMapsTeamSeparator, nil))
		}
		if !eff.IsEnabled || !eff.IsMapAvailable {
			list = "Not in rotation"
		} else if eff.MapHeight > 0 && eff.MapHeight != mapRecord.MapHeight {
			list += fmt.Sprintf(" · Height %d", eff.MapHeight)
		}
		var g *group
		for _, existing := range groups {
			if existing.list == list {
				g = existing
				break
			}
		}
		if g == nil {
			g = &group{list: list}
			groups = append(groups, g)
		}
		g.modes = append(g.modes, mode)
	}
	common := 0
	for idx, g := range groups {
		if len(g.modes) > len(groups[common].modes) {
			common = idx
		}
	}
	var segments []string
	for idx, g := range groups {
		label := strings.Join(g.modes, ", ")
		if idx == common {
			label = "All queues"
			if len(groups) > 1 {
				label = "All other queues"
			}
		}
		segments = append(segments, helpers.FillLayout(guildID, helpers.LayoutMapsMode, map[string]string{"mode": label, "teams": g.list}))
	}
	if len(groups) > 1 { // the common list first, then the exceptions
		segments = append([]string{segments[common]}, append(segments[:common:common], segments[common+1:]...)...)
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
