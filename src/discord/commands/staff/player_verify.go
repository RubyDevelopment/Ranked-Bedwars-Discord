package staff

import (
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
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
	Description:  "Checks a player's queue eligibility: registration, account age, restrictions, and game state.",
	Requirements: []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}},
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
		return helpers.EditInteractionEmbed(s, i, playerVerifyInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, playerVerifyBotTargetEmbed())
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, playerVerifyNotRegisteredEmbed(target))
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerVerifyEmbed())
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
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPlayerVerifyEmbed())
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

	return helpers.EditInteractionEmbed(s, i, playerVerifyReportEmbed(target, view.Config.Username, ageDays, whitelisted, restrictions, inGame, problems))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPlayerVerifyEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while verifying the player. Please try again later or contact support.",
	})
}

func playerVerifyInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to verify. Select a Discord member from the command options.",
	})
}

func playerVerifyBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be verified. Target a registered player instead.",
	})
}

func playerVerifyNotRegisteredEmbed(user *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild, so they cannot queue. Register them with `/player_force_register`.", user.ID),
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func playerVerifyReportEmbed(user *discordgo.User, username string, ageDays int, whitelisted bool, restrictions []string, inGame bool, problems []string) *discordgo.MessageEmbed {
	preset, verdict := "success", "Eligible"
	if len(problems) > 0 {
		preset, verdict = "error", "Not Eligible"
	}

	age := fmt.Sprintf("**%d** days", ageDays)
	if whitelisted {
		age += " (whitelisted)"
	}
	active := "`None`"
	if len(restrictions) > 0 {
		active = "`" + strings.Join(restrictions, "`, `") + "`"
	}
	state := "`Available`"
	if inGame {
		state = "`In a game`"
	}

	description := fmt.Sprintf(
		"Eligibility report for <@%s>.\n\n> **Username:** `%s`\n> **Verdict:** `%s`\n> **Account Age:** %s\n> **Active Punishments:** %s\n> **Game State:** %s",
		user.ID, username, verdict, age, active, state,
	)
	if len(problems) > 0 {
		description += "\n\n**Blocking:**\n> " + strings.Join(problems, "\n> ")
	}

	return helpers.Preset(preset, types.PresetOptions{
		Title:       fmt.Sprintf("Player Verification — %s", username),
		Description: description,
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
