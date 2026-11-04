package general

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/partymgr"
	"ranked-bedwars/src/discord/helpers/ratings"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(PlayerClaimCommand)
	// Perks need the member's roles, so the manager resolves them through the live session.
	ratings.Elo.SetPerkResolver(ratings.NewPerkResolver(partymgr.BotSession.Load))
}

// ============================================================================
// Command Definition
// ============================================================================

// PlayerClaimCommand Claims starting ELO or seasonal reward bonuses based on verified player status.
var PlayerClaimCommand = &types.Command{
	Name:        "claim",
	Description: "Claims starting ELO or seasonal reward bonuses based on verified player status.",
	Cooldown:    3 * time.Second,
	Data:        &discordgo.ApplicationCommand{},
	Execute:     executePlayerClaimCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePlayerClaimCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	partymgr.BotSession.Store(s)
	userID := helpers.InteractionUserID(i)

	// 1. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, ClaimEloNotRegisteredEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorClaimEloEmbed(i))
	}
	username := view.Config.Username

	// 2. Validate Active Season
	seasonKey, _, err := helpers.ActiveSeasonKey(i.GuildID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorClaimEloEmbed(i))
	}
	if seasonKey == "" {
		return helpers.EditInteractionEmbed(s, i, ClaimEloNoActiveSeasonEmbed(i))
	}

	// 3. Pay & Record the Claim
	claim, err := ratings.Elo.ClaimStartingElo(userID, i.GuildID, userID)
	switch {
	case errors.Is(err, ratings.ErrNoStartingGrant):
		return helpers.EditInteractionEmbed(s, i, ClaimEloNoPerkEmbed(i, username))
	case errors.Is(err, ratings.ErrStartingGrantUsed):
		return helpers.EditInteractionEmbed(s, i, ClaimEloUsedEmbed(username, i.GuildID, userID))
	case claim == nil:
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorClaimEloEmbed(i))
	}
	return helpers.EditInteractionEmbed(s, i, ClaimEloSuccessEmbed(i, username, claim))
}

// ============================================================================
// Embed Builders
// ============================================================================

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorClaimEloEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while claiming your starting ELO. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ClaimEloNotRegisteredEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not Registered",
		Description: "You must register with `/register` before claiming ELO.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(""),
	})
}

func ClaimEloNoActiveSeasonEmbed(i *discordgo.InteractionCreate) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Season",
		Description: "This guild does not have an active season configured. If you think this is an error, please contact an administrator for assistance.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func ClaimEloNoPerkEmbed(i *discordgo.InteractionCreate, username string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Starting ELO Perk",
		Description: "None of your perks grant starting ELO. Perks come from roles, so check that you hold the role a perk requires.",
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}

func ClaimEloSuccessEmbed(i *discordgo.InteractionCreate, username string, c *ratings.Claim) *discordgo.MessageEmbed {
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

// ClaimEloUsedEmbed explains why the grant is closed, quoting the stored claim
// record when it was already claimed.
func ClaimEloUsedEmbed(username, guildID, userID string) *discordgo.MessageEmbed {
	title := "Starting ELO Closed"
	description := "You can only claim starting ELO before your first ranked game of the season. A perk's starting ELO is also paid automatically with your first ranked game."
	if st, err := database.Must().Stats.Get(guildID, userID, 0); err == nil && st.StartingEloClaimed {
		description = fmt.Sprintf("You already claimed **%d** ELO", st.StartingEloClaimedAmount)
		if st.StartingEloClaimedAt != nil {
			description += " " + helpers.DiscordRelativeTimestamp(*st.StartingEloClaimedAt)
		}
		if st.StartingEloClaimedPerks != "" {
			description += fmt.Sprintf(" from **%s**", st.StartingEloClaimedPerks)
		}
		description += " this season."
		title = "Already Claimed"
	}
	return helpers.Preset("error", types.PresetOptions{
		Title:       title,
		Description: description,
		Thumbnail:   helpers.MinecraftBustAvatarURL(username),
	})
}
