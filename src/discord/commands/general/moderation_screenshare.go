package general

import (
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ModerationScreenshareCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationScreenshareCommand requests a screenshare for a player.
var ModerationScreenshareCommand = &types.Command{
	Name:          "screenshare",
	Description:   "Requests a screenshare for a player to keep competitive play fair.",
	PrefixAliases: []string{"ss"},
	Ephemeral:     true,
	Cooldown:      5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "user", Description: "Player to request a screenshare for", Type: discordgo.ApplicationCommandOptionUser, Required: true},
			{Name: "reason", Description: "Reason for the screenshare request", Type: discordgo.ApplicationCommandOptionString, Required: false},
		},
	},
	Execute: executeModerationScreenshareCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

// executeModerationScreenshareCommand files a screenshare review request for a player.
func executeModerationScreenshareCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	svc := database.Get()
	if svc == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationScreenshareDatabaseUnavailableEmbed(i))
	}

	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationScreenshareInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationScreenshareBotTargetEmbed(i))
	}

	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	username := target.Username

	if view, err := svc.Player.Get(i.GuildID, target.ID); err == nil && view.Config.Username != "" {
		username = view.Config.Username
	}

	return helpers.EditInteractionEmbed(s, i, helpers.ModerationScreenshareSubmittedEmbed(i, target, username, reason))
}
