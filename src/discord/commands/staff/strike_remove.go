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
	helpers.Register(StrikeRemoveCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// StrikeRemoveCommand voids a player's most recent strike, or a specific one by ID.
var StrikeRemoveCommand = &types.Command{
	Name:         "unstrike",
	Requirements: types.PunishmentRequirements("strike_logs", "queue_banned_role"),
	Description:  "Removes a specific strike entry from a player's record.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to unstrike",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "id",
				Description: "Strike ID to void (defaults to their most recent strike)",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    false,
			},
		},
	},
	Execute: executeStrikeRemoveCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeStrikeRemoveCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.StrikeRemoveInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.StrikeRemoveBotTargetEmbed(i))
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.StrikeRemovePlayerNotFoundEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorStrikeRemoveEmbed(i))
	}
	pid := sdk.UintToString(view.Player.ID)
	actor := helpers.InteractionUserID(i)

	// 3. Resolve Strike
	strikeID := uint(helpers.OptionInt(i, "id"))
	if strikeID == 0 {
		latest, err := latestUnvoidedStrikeID(i.GuildID, pid)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorStrikeRemoveEmbed(i))
		}
		if latest == 0 {
			return helpers.EditInteractionEmbed(s, i, helpers.StrikeRemoveNoStrikesEmbed(i))
		}
		strikeID = latest
	} else {
		strike, err := database.Must().Strikes.Get(i.GuildID, strikeID)
		if err != nil && !sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorStrikeRemoveEmbed(i))
		}
		if strike == nil || strike.PlayerID != view.Player.ID {
			return helpers.EditInteractionEmbed(s, i, helpers.StrikeRemoveNotFoundEmbed(i))
		}
		if strike.IsVoided {
			return helpers.EditInteractionEmbed(s, i, helpers.StrikeRemoveAlreadyVoidedEmbed(i))
		}
	}

	// 4. Void Strike in Database, then give back the ELO it took (a lapsed strike keeps its penalty; a removed one does not)
	removed, _ := database.Must().Strikes.Get(i.GuildID, strikeID)
	if err := database.Must().Strikes.Delete(actor, i.GuildID, strikeID); err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.StrikeRemoveNotFoundEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorStrikeRemoveEmbed(i))
	}
	if removed != nil {
		if err := helpers.Elo.RefundStrike(actor, i.GuildID, target.ID, removed); err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorStrikeRemoveEmbed(i))
		}
	}

	username := target.Username
	if view.Config != nil && view.Config.Username != "" {
		username = view.Config.Username
	}
	embed := helpers.StrikeRemoveSuccessEmbed(i, strikeID, target, username)
	helpers.LogCommandResult(s, i.GuildID, "strike_remove", actor, embed)
	helpers.SendPunishmentAlert(s, i.GuildID, "strike_issue", "revoked", target, username, "strike", "voided", "Strike voided by staff")
	return helpers.EditInteractionEmbed(s, i, embed)
}

// ============================================================================
// Core Logic & Helpers
// ============================================================================

// latestUnvoidedStrikeID returns the most recent non-voided strike for a player, or 0 if none exist.
func latestUnvoidedStrikeID(guildID, playerID string) (uint, error) {
	strikes, err := database.Must().Strikes.AllByPlayer(guildID, playerID, 0, false)
	if err != nil {
		return 0, err
	}
	for _, strike := range strikes {
		if !strike.IsVoided {
			return strike.ID, nil
		}
	}
	return 0, nil
}
