package general

import (
	"errors"
	"fmt"
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
			return helpers.EditInteractionEmbed(s, i, claimEloNotRegisteredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorClaimEloEmbed())
	}
	username := view.Config.Username

	// 2. Validate Active Season
	seasonKey, _, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorClaimEloEmbed())
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, claimEloNoActiveSeasonEmbed())
	}

	// 3. Pay & Record the Claim
	claim, err := helpers.Elo.ClaimStartingElo(userID, i.GuildID, userID)
	switch {
	case errors.Is(err, helpers.ErrNoStartingGrant):
		return helpers.EditInteractionEmbed(s, i, claimEloNoPerkEmbed(username))
	case errors.Is(err, helpers.ErrStartingGrantUsed):
		return helpers.EditInteractionEmbed(s, i, claimEloUsedEmbed(username, i.GuildID, userID))
	case claim == nil:
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorClaimEloEmbed())
	}
	return helpers.EditInteractionEmbed(s, i, claimEloSuccessEmbed(username, claim))
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorClaimEloEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while claiming your starting ELO. Please contact an administrator for assistance or try again later.",
	})
}

func claimEloNotRegisteredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not Registered",
		Description: "You must register with `/player_register` before claiming ELO.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func claimEloNoActiveSeasonEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
	})
}

func claimEloNoPerkEmbed(username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Starting ELO Perk",
		Description: "None of your perks grant starting ELO. Perks come from roles, so check that you hold the role a perk requires.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

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

func claimEloSuccessEmbed(username string, c *helpers.Claim) *discordgo.MessageEmbed {
	rank := c.RankAfter
	if c.RankBefore != c.RankAfter {
		rank = fmt.Sprintf("%s → %s", c.RankBefore, c.RankAfter)
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Starting ELO Claimed",
		Description: fmt.Sprintf("**%s** received **%d** starting ELO.", username, c.Amount),
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "ELO", Value: fmt.Sprintf("%d → %d", c.Before, c.After), Inline: true},
			{Name: "Rank", Value: rank, Inline: true},
			{Name: "Perks", Value: strings.Join(c.Perks, ", "), Inline: false},
		},
	})
}
