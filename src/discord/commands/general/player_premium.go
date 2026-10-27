package general

import (
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
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
	Name:        "premium",
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
	return helpers.EditInteractionEmbed(s, i, helpers.PlayerPremiumUnavailableEmbed(i))
}
