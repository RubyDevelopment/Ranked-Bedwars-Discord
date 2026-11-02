package general

import (
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(ModerationStrikeRequestCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationStrikeRequestCommand requests a strike review for a player.
var ModerationStrikeRequestCommand = &types.Command{
	Name:          "strikerequest",
	Description:   "Requests a strike review for a player who broke the rules.",
	PrefixAliases: []string{"sr"},
	Ephemeral:     true,
	Cooldown:      5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to request a strike review for",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Why the strike should be reviewed",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
			{
				Name:        "game",
				Description: "Game number the request is about (default: the player's latest game)",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    false,
				MinValue:    func() *float64 { v := 1.0; return &v }(),
			},
		},
	},
	Execute: executeModerationStrikeRequestCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeModerationStrikeRequestCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationStrikeRequestInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationStrikeRequestBotTargetEmbed(i))
	}

	// 2. Validate Reason
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	if reason == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationStrikeRequestReasonRequiredEmbed(i))
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.ModerationStrikeRequestNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationStrikeRequestEmbed(i))
	}

	username := target.Username
	if view.Config != nil && view.Config.Username != "" {
		username = view.Config.Username
	}
	// 4. Open the thread (the request and its vote live in it). The thread is the answer: a prefix request shows it under the
	// player's own message, and a slash request, whose reply is private, gets a link to it.
	thread, problem := helpers.OpenStrikeRequest(s, i, target, username, reason, helpers.OptionInt(i, "game"))
	if problem != nil {
		return helpers.EditInteractionEmbed(s, i, problem)
	}
	if helpers.IsPrefixInteraction(i) {
		return nil
	}
	text := "Strike request opened: <#" + thread.ID + ">"
	return helpers.EditInteractionResponse(s, i, &discordgo.WebhookEdit{Content: &text})
}
