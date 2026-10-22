package staff

import (
	"fmt"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(PlayerVerifyCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerVerifyCommand reports whether a player is currently eligible to queue and why not.
var PlayerVerifyCommand = &types.Command{
	Name:         "verify",
	Requirements: types.StaffRequirements,
	Description:  "Checks a player's queue eligibility: registration, account age, restrictions, and game state.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to verify",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Execute: executePlayerVerifyCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerVerifyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerVerifyInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerVerifyBotTargetEmbed(i))
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerVerifyNotRegisteredEmbed(i, target))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerVerifyEmbed(i))
	}

	// 3. Check Account Age
	problems := []string{}
	cfg := helpers.LoadAccountAgeConfig(i.GuildID)
	created := helpers.AccountCreatedAt(target.ID)
	ageDays := int(time.Since(created).Hours() / 24)
	whitelisted := false
	for _, id := range cfg.Whitelist {
		if id == target.ID {
			whitelisted = true
		}
	}
	if cfg.Enabled && cfg.MinDays > 0 && !whitelisted && ageDays < cfg.MinDays {
		problems = append(problems, fmt.Sprintf("Discord account is younger than **%d** days", cfg.MinDays))
	}

	// 4. Check Active Restrictions
	active, err := database.Must().Punishments.ByPlayer(i.GuildID, sdk.UintToString(view.Player.ID), true, "", sdk.Page{})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerVerifyEmbed(i))
	}
	restrictions := []string{}
	for _, row := range active.Data {
		restrictions = append(restrictions, row.Type)
		if helpers.PunishmentTypeIn(row.Type, []string{"ban", "server_ban"}) {
			problems = append(problems, fmt.Sprintf("Active **%s** punishment", row.Type))
		}
	}

	// 5. Check Game State
	inGame := helpers.Games.InGame(i.GuildID, target.ID)
	if inGame {
		problems = append(problems, "Currently in a game")
	}

	return helpers.EditInteractionEmbed(s, i, helpers.PlayerVerifyReportEmbed(i, target, view.Config.Username, ageDays, whitelisted, restrictions, inGame, problems))
}
