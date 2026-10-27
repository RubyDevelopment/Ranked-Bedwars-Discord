package staff

import (
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
	helpers.Register(PlayerUnregisterCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerUnregisterCommand unlinks a player's Minecraft account and clears their guild roles.
var PlayerUnregisterCommand = &types.Command{
	Name:         "unregister",
	Requirements: types.StaffRegistrationRequirements(),
	Description:  "Unlinks a player's Minecraft account and clears their guild roles.",
	Ephemeral:    true,
	Cooldown:     5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: helpers.AdministratorPermission(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to unregister",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Execute: executePlayerUnregisterCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerUnregisterCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerUnregisterInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerUnregisterBotTargetEmbed(i))
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerUnregisterNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerUnregisterEmbed(i))
	}
	if view.Config.Username == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerUnregisterNotRegisteredEmbed(i))
	}

	// 3. Remove Registration from Database
	// Remove also clears the guild's stats, strikes, punishments, and decays for this player.
	username := view.Config.Username
	actor := helpers.InteractionUserID(i)
	if err := database.Must().Player.Remove(actor, i.GuildID, sdk.UintToString(view.Player.ID)); err != nil && !sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerUnregisterEmbed(i))
	}

	// 4. Reset Roles & Nickname
	if member, _ := helpers.FetchGuildMember(s, i.GuildID, target.ID); member != nil {
		_ = helpers.ResetMemberRegistrationProfile(s, i.GuildID, member)
	}

	return helpers.EditInteractionEmbed(s, i, helpers.PlayerUnregisterSuccessEmbed(i, target, username))
}
