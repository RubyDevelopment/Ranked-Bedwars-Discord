package general

import (
	"time"

	"github.com/bwmarrin/discordgo"

	cmdcfg "ranked-bedwars/src/discord/config"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
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
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorMapsEmbed(i))
	}
	modes, err := helpers.GuildMetaModes(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorMapsEmbed(i))
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
		return helpers.EditInteractionEmbed(s, i, cmdcfg.EmptyMapsEmbed(i))
	}

	// Get the active mode names for this guild
	targetModes := helpers.ActiveModeNames(modes)
	if len(targetModes) == 0 {
		targetModes = []string{"Default"}
	}

	return helpers.EditInteractionEmbed(s, i, cmdcfg.MapsRotationEmbed(i, rotation, targetModes))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================
