package general

import (
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
	helpers.Register(PlayerSyncCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerSyncCommand synchronizes the caller's registered role, rank role, and nickname.
var PlayerSyncCommand = &types.Command{
	Name:          "sync",
	Requirements:  types.GeneralRequirements,
	Description:   "Synchronizes your registered role, rank role, and nickname with the guild.",
	PrefixAliases: []string{"refresh"},
	Ephemeral:     true,
	Cooldown:      5 * time.Second,
	Data:          &discordgo.ApplicationCommand{},
	Execute:       executePlayerSyncCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerSyncCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := helpers.InteractionUserID(i)

	// 1. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PlayerSyncRegistrationRequiredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerSyncEmbed(i))
	}

	// 2. Validate Guild Membership
	member, err := helpers.FetchGuildMember(s, i.GuildID, userID)
	if err != nil || member == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerSyncMemberNotFoundEmbed(i))
	}

	// 3. Synchronize Roles & Nickname
	actions, err := helpers.SyncRegisteredPlayerProfile(s, i.GuildID, view.Player.ID, userID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPlayerSyncEmbed(i))
	}
	if len(actions) == 0 {
		return helpers.EditInteractionEmbed(s, i, helpers.PlayerSyncUpToDateEmbed(i, view.Config.Username))
	}
	return helpers.EditInteractionEmbed(s, i, helpers.PlayerSyncSuccessEmbed(i, view.Config.Username, actions))
}
