package general

import (
	"errors"
	"fmt"
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
	helpers.Register(PlayerClaimEloCommand)
	// Perks need the member's roles, so the manager resolves them through the live session.
	helpers.Elo.SetPerkResolver(helpers.NewPerkResolver(helpers.BotSession.Load))
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerClaimEloCommand Claims starting ELO or seasonal reward bonuses based on verified player status.
var PlayerClaimEloCommand = &types.Command{
	Name:        "claim",
	Description: "Claims starting ELO or seasonal reward bonuses based on verified player status.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
	Data:        &discordgo.ApplicationCommand{},
	Execute:     executePlayerClaimEloCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerClaimEloCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	helpers.BotSession.Store(s)
	userID := helpers.InteractionUserID(i)

	// 1. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, cmdcfg.ClaimEloNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorClaimEloEmbed(i))
	}
	username := view.Config.Username

	// 2. Validate Active Season
	seasonKey, _, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorClaimEloEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, cmdcfg.ClaimEloNoActiveSeasonEmbed(i))
	}

	// 3. Pay & Record the Claim
	claim, err := helpers.Elo.ClaimStartingElo(userID, i.GuildID, userID)
	switch {
	case errors.Is(err, helpers.ErrNoStartingGrant):
		return helpers.EditInteractionEmbed(s, i, cmdcfg.ClaimEloNoPerkEmbed(i, username))
	case errors.Is(err, helpers.ErrStartingGrantUsed):
		return helpers.EditInteractionEmbed(s, i, claimEloUsedEmbed(username, i.GuildID, userID))
	case claim == nil:
		return helpers.EditInteractionEmbed(s, i, cmdcfg.UnexpectedErrorClaimEloEmbed(i))
	}
	return helpers.EditInteractionEmbed(s, i, cmdcfg.ClaimEloSuccessEmbed(i, username, claim))
}

// ============================================================================
// Embed Builders
// ============================================================================

// claimEloUsedEmbed explains why the grant is closed, quoting the stored claim
// record when it was already claimed.
func claimEloUsedEmbed(username, guildID, userID string) *discordgo.MessageEmbed {
	description := "You can only claim starting ELO before your first ranked game of the season."
	if st, err := database.Must().Stats.Get(guildID, userID, 0); err == nil && st.StartingEloClaimed {
		description = fmt.Sprintf("You already claimed **%d** ELO", st.StartingEloClaimedAmount)
		if st.StartingEloClaimedAt != nil {
			description += " " + helpers.DiscordRelativeTimestamp(*st.StartingEloClaimedAt)
		}
		if st.StartingEloClaimedPerks != "" {
			description += fmt.Sprintf(" from **%s**", st.StartingEloClaimedPerks)
		}
		description += " this season."
	}
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already Claimed",
		Description: description,
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
