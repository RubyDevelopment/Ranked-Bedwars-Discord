package general

import (
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	cmdcfg "ranked-bedwars/src/discord/config"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(StrikeRequestCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// StrikeRequestCommand requests a strike review for a player.
var StrikeRequestCommand = &types.Command{
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
		},
	},
	Execute: executeStrikeRequestCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeStrikeRequestCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeRequestInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeRequestBotTargetEmbed(i))
	}

	// 2. Validate Reason
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	if reason == "" {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeRequestReasonRequiredEmbed(i))
	}

	// 3. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeRequestNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorStrikeRequestEmbed(i))
	}

	username := target.Username
	if view.Config != nil && view.Config.Username != "" {
		username = view.Config.Username
	}
	return helpers.EditInteractionEmbed(s, i, cmdcfg.StrikeRequestSubmittedEmbed(i, target, username, reason))
}
