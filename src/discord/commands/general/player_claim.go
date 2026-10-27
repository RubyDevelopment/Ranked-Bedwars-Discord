package general

import (
	"errors"
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
	helpers.Register(PlayerClaimCommand)
	// Perks need the member's roles, so the manager resolves them through the live session.
	helpers.Elo.SetPerkResolver(helpers.NewPerkResolver(helpers.BotSession.Load))
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerClaimCommand Claims starting ELO or seasonal reward bonuses based on verified player status.
var PlayerClaimCommand = &types.Command{
	Name:        "claim",
	Description: "Claims starting ELO or seasonal reward bonuses based on verified player status.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data:        &discordgo.ApplicationCommand{},
	Execute:     executePlayerClaimCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerClaimCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	helpers.BotSession.Store(s)
	userID := helpers.InteractionUserID(i)

	// 1. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.ClaimEloNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorClaimEloEmbed(i))
	}
	username := view.Config.Username

	// 2. Validate Active Season
	seasonKey, _, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorClaimEloEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.ClaimEloNoActiveSeasonEmbed(i))
	}

	// 3. Pay & Record the Claim
	claim, err := helpers.Elo.ClaimStartingElo(userID, i.GuildID, userID)
	switch {
	case errors.Is(err, helpers.ErrNoStartingGrant):
		return helpers.EditInteractionEmbed(s, i, helpers.ClaimEloNoPerkEmbed(i, username))
	case errors.Is(err, helpers.ErrStartingGrantUsed):
		return helpers.EditInteractionEmbed(s, i, helpers.ClaimEloUsedEmbed(username, i.GuildID, userID))
	case claim == nil:
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorClaimEloEmbed(i))
	}
	return helpers.EditInteractionEmbed(s, i, helpers.ClaimEloSuccessEmbed(i, username, claim))
}

// ============================================================================
// Embed Builders
// ============================================================================
