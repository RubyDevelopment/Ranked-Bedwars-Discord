package staff

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
	helpers.Register(PlayerForceSyncCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerForceSyncCommand force-synchronizes a target player's registered role, rank role, and nickname.
var PlayerForceSyncCommand = &types.Command{
	Name:          "forcesync",
	Requirements:  types.StaffRegistrationRequirements(),
	Description:   "Force-synchronizes a player's registered role, rank role, and nickname with the guild.",
	PrefixAliases: []string{"fsync"},
	Ephemeral:     true,
	Cooldown:      5 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Player to sync",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	},
	Execute: executePlayerForceSyncCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerForceSyncCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerForceSyncInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerForceSyncBotTargetEmbed(i))
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerForceSyncRegistrationRequiredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerForceSyncEmbed(i))
	}

	// 3. Validate Guild Membership
	member, err := helpers.FetchGuildMember(s, i.GuildID, target.ID)
	if err != nil || member == nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerForceSyncMemberNotFoundEmbed(i))
	}

	// 4. Synchronize Roles & Nickname
	actions, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, target.ID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorPlayerForceSyncEmbed(i))
	}

	// 5. Return Result
	if len(actions) == 0 {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerForceSyncUpToDateEmbed(i, view.Config.Username))
	}
	return helpers.EditInteractionEmbed(s, i, cmdcfg.PlayerForceSyncSuccessEmbed(i, target, view.Config.Username, actions))
}
