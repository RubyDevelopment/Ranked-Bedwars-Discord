package general

import (
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
	helpers.Register(PlayerRegisterCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerRegisterCommand links the caller's Minecraft account to their Discord account.
var PlayerRegisterCommand = &types.Command{
	Name:         "register",
	Requirements: types.RegistrationRequirements,
	Description:  "Links your Minecraft account to your Discord account.",
	Ephemeral:    true,
	Cooldown:     5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "username",
				Description: "Your Minecraft username",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
		},
	},
	Execute: executePlayerRegisterCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerRegisterCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	username := helpers.OptionString(i, "username")
	if !helpers.IsValidMcUsername(username) {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerRegisterInvalidUsernameEmbed(i))
	}

	userID := helpers.InteractionUserID(i)

	// Check if player is already registered
	existing, err := database.Must().Player.Get(i.GuildID, userID)
	if err == nil && existing != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerRegisterAlreadyRegisteredEmbed(i, existing.Config.Username))
	} else if err != nil && !sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerRegisterEmbed(i))
	}

	// A rank must exist so the new player can be given one (and its role)
	if ok, rankErr := helpers.HasConfiguredRank(i.GuildID); rankErr != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerRegisterEmbed(i))
	} else if !ok {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerRegisterNoRanksEmbed(i))
	}

	// Registration creates the player's stats in the active season, so one has to exist
	if _, err := database.Must().Season.Active(i.GuildID); sdk.IsNotFound(err) {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerRegisterNoSeasonEmbed(i))
	} else if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerRegisterEmbed(i))
	}

	verification, _ := helpers.LoadRegistrationVerification(i.GuildID)

	// Party-code mode: the user proves they own the account first; the code modal finishes the registration
	if helpers.RequiresPartyCodeVerification(verification) {
		return startPartyCodeRegistration(s, i, userID, username, verification.InGame)
	}

	// Verify Minecraft account existence if required by guild setup
	if helpers.RequiresRegistrationAPIVerification(verification) {
		exists, verifyErr := helpers.VerifyMinecraftUsernameExistsWithConfig(username, verification)
		if verifyErr != nil {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerRegisterAPIOutageEmbed(i, helpers.RegistrationNetworkLabel(verification)))
		}
		if !exists {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerRegisterAccountNotFoundEmbed(i, username, helpers.RegistrationNetworkLabel(verification)))
		}
	}

	// Register player atomically
	return helpers.EditInteractionEmbed(s, i, registerNewPlayer(i, s, i.GuildID, userID, username))
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// registerNewPlayer links the Minecraft name to the Discord user and returns the embed that says how it went.
func registerNewPlayer(i *discordgo.InteractionCreate, s *discordgo.Session, guildID, userID, username string) *discordgo.MessageEmbed {
	reg, err := database.Must().Player.Register(userID, guildID, userID, username, 0)
	if err != nil {
		if sdk.IsConflict(err) {
			return cmdcfg.PlayerRegisterConflictEmbed(i, username)
		}
		return cmdcfg.UnexpectedErrorPlayerRegisterEmbed(i)
	}
	// Apply starting rank/ELO for brand new registrations
	if reg.CreatedGuild {
		if err := helpers.ApplyStartingRank(guildID, userID, reg.PlayerID); err != nil {
			return cmdcfg.UnexpectedErrorPlayerRegisterEmbed(i)
		}
	}
	if _, err := helpers.SyncRegisteredPlayerProfile(s, guildID, reg.PlayerID, userID); err != nil {
		return cmdcfg.UnexpectedErrorPlayerRegisterEmbed(i)
	}
	return cmdcfg.PlayerRegisterSuccessEmbed(i, username)
}
