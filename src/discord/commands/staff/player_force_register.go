package staff

import (
	"strings"
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
	helpers.Register(PlayerForceRegisterCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerForceRegisterCommand force-registers a target user with a Minecraft username without verification.
var PlayerForceRegisterCommand = &types.Command{
	Name:          "forceregister",
	Requirements:  types.StaffRegistrationRequirements(),
	Description:   "Force-registers a Minecraft account to a Discord account without verification.",
	PrefixAliases: []string{"fregister"},
	Ephemeral:     true,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to register",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "username",
				Description: "Minecraft username to link",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
		},
	},
	Execute: executePlayerForceRegisterCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerForceRegisterCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRegisterInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRegisterBotTargetEmbed(i))
	}

	// 2. Validate Username Format
	username := strings.TrimSpace(helpers.OptionString(i, "username"))
	if !helpers.IsValidMcUsername(username) {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRegisterInvalidUsernameEmbed(i))
	}

	// 3. Check for Existing Registration
	if existing, err := database.Must().Player.Get(i.GuildID, target.ID); err == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRegisterAlreadyRegisteredEmbed(i, existing.Config.Username))
	} else if !sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceRegisterEmbed(i))
	}

	// 4. Require at least one configured rank so the player can be given one (and its role)
	if ok, rankErr := helpers.HasConfiguredRank(i.GuildID); rankErr != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceRegisterEmbed(i))
	} else if !ok {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRegisterNoRanksEmbed(i))
	}

	// 5. Register (creates the global player, config, and starting stats atomically)
	actor := helpers.InteractionUserID(i)
	reg, err := database.Must().Player.Register(actor, i.GuildID, target.ID, username, 0)
	if err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRegisterConflictEmbed(i, username))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceRegisterEmbed(i))
	}

	// 6. Apply the guild's configured starting rank/ELO for new registrations
	if reg.CreatedGuild {
		if err := helpers.ApplyStartingRank(i.GuildID, actor, reg.PlayerID); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceRegisterEmbed(i))
		}
	}
	if _, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, reg.PlayerID, target.ID); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceRegisterEmbed(i))
	}

	return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceRegisterSuccessEmbed(i, target, username))
}
