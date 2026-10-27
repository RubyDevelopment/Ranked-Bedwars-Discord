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
	helpers.Register(PlayerForceTransferCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerForceTransferCommand force-transfers a Minecraft account link between Discord users.
var PlayerForceTransferCommand = &types.Command{
	Name:          "forcetransfer",
	Requirements:  types.StaffRegistrationRequirements(),
	Description:   "Force-transfers a Minecraft account link between Discord users.",
	PrefixAliases: []string{"ftransfer"},
	Ephemeral:     true,
	Cooldown:      5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "old_user",
				Description: "Current linked Discord user",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "new_user",
				Description: "Discord user to receive the link",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Execute: executePlayerForceTransferCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerForceTransferCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target Users
	oldUser := helpers.OptionUser(s, i, "old_user")
	newUser := helpers.OptionUser(s, i, "new_user")

	if oldUser == nil || newUser == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceTransferInvalidTargetEmbed(i))
	}
	if oldUser.ID == newUser.ID {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceTransferSameUserEmbed(i))
	}

	// 2. Fetch Source Player Profile
	oldView, err := database.Must().Player.Get(i.GuildID, oldUser.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceTransferSourceNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceTransferEmbed(i))
	}
	if oldView.Config.Username == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceTransferSourceProfileNotFoundEmbed(i))
	}

	// 3. Check Target Registration Status
	if newView, err := database.Must().Player.Get(i.GuildID, newUser.ID); err == nil && newView.Player.ID != oldView.Player.ID {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceTransferDestinationAlreadyRegisteredEmbed(i, newUser, newView.Config.Username))
	} else if err != nil && !sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceTransferEmbed(i))
	}

	// 4. Update Snowflake Link in Database
	username := oldView.Config.Username
	actor := helpers.InteractionUserID(i)
	pid := sdk.UintToString(oldView.Player.ID)

	if _, err := database.Must().Player.SetSnowflake(actor, pid, newUser.ID); err != nil {
		if sdk.IsConflict(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceTransferConflictEmbed(i, newUser))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceTransferEmbed(i))
	}

	// 5. Reset Source Member Profile
	if oldMember, _ := helpers.FetchGuildMember(s, i.GuildID, oldUser.ID); oldMember != nil {
		_ = helpers.ResetMemberRegistrationProfile(s, i.GuildID, oldMember)
	}

	// 6. Synchronize Destination Member Profile
	var actions []string
	if newMember, _ := helpers.FetchGuildMember(s, i.GuildID, newUser.ID); newMember != nil {
		syncActions, syncErr := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, oldView.Player.ID, newUser.ID)
		if syncErr != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerForceTransferEmbed(i))
		}
		actions = syncActions
	}

	return helpers.EditInteractionEmbed(s, i, helpers.PlayerForceTransferSuccessEmbed(i, oldUser, newUser, username, actions))
}
