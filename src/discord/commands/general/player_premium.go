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
	helpers.Register(PlayerPremiumCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerPremiumCommand Manages premium status perks, benefits, and subscription features.
var PlayerPremiumCommand = &types.Command{
	Name:        "player_premium",
	Description: "Manages premium status perks, benefits, and subscription features.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data:        &discordgo.ApplicationCommand{},
	Execute:     executePlayerPremiumCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerPremiumCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// TODO: Implement premium management logic and status overview embed
	return helpers.EditInteractionEmbed(s, i, playerPremiumUnavailableEmbed())
}

// ============================================================================
// Embed Builders
// ============================================================================

func playerPremiumUnavailableEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Premium Features",
		Description: "Premium management features are currently under development. Check back soon for subscription benefits, perk overrides, and custom styling options.",
	})
}
