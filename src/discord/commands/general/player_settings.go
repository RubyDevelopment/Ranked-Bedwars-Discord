package general

import (
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/playersettings"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(PlayerSettingsCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerSettingsCommand lists the caller's ranked profile settings, each switched on or off with a button.
var PlayerSettingsCommand = &types.Command{
	Name:         "settings",
	Requirements: types.GeneralRequirements,
	Description:  "Views and changes your ranked profile settings",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data:         &discordgo.ApplicationCommand{},
	Execute:      executePlayerSettingsCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

// executePlayerSettingsCommand shows every setting with its state and the buttons that switch them; the buttons are handled in
// the playersettings helper, which also enforces the guild's settings restrictions.
func executePlayerSettingsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return playersettings.Show(s, i)
}
