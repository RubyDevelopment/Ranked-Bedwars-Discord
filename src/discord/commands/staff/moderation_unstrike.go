package staff

import (
	"fmt"
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
	helpers.Register(ModerationUnstrikeCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationUnstrikeCommand voids a player's most recent strike, or a specific one by ID.
var ModerationUnstrikeCommand = &types.Command{
	Name:         "unstrike",
	Requirements: types.PunishmentRequirements("strike_logs", "queue_banned_role", "chat_muted_role", "vc_muted_role"),
	Description:  "Removes a specific strike entry from a player's record.",
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
	Execute: executeModerationUnstrikeCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeModerationUnstrikeCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationUnstrikeInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationUnstrikeBotTargetEmbed(i))
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.ModerationUnstrikePlayerNotFoundEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationUnstrikeEmbed(i))
	}
	pid := sdk.UintToString(view.Player.ID)
	actor := helpers.InteractionUserID(i)

	// 3. Resolve Strike
	strikeID := uint(helpers.OptionInt(i, "id"))
	if strikeID == 0 {
		latest, err := latestUnvoidedStrikeID(i.GuildID, pid)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationUnstrikeEmbed(i))
		}
		if latest == 0 {
			return helpers.EditInteractionEmbed(s, i, helpers.ModerationUnstrikeNoStrikesEmbed(i))
		}
		strikeID = latest
	} else {
		strike, err := database.Must().Strikes.Get(i.GuildID, strikeID)
		if err != nil && !sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationUnstrikeEmbed(i))
		}
		if strike == nil || strike.PlayerID != view.Player.ID {
			return helpers.EditInteractionEmbed(s, i, helpers.ModerationUnstrikeNotFoundEmbed(i))
		}
		if strike.IsVoided {
			return helpers.EditInteractionEmbed(s, i, helpers.ModerationUnstrikeAlreadyVoidedEmbed(i))
		}
	}

	// 4. Void the strike (the row stays in history and stops counting toward the ladder), then give back the ELO it
	// took. If the refund fails the strike is un-voided again so the record and the rating never disagree.
	removed, _ := database.Must().Strikes.Get(i.GuildID, strikeID)
	if err := database.Must().Strikes.Void(actor, i.GuildID, strikeID); err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.ModerationUnstrikeNotFoundEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationUnstrikeEmbed(i))
	}
	if removed != nil {
		if err := helpers.Elo.RefundStrike(actor, i.GuildID, target.ID, removed); err != nil {
			_ = database.Must().Strikes.Unvoid(actor, i.GuildID, strikeID)
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationUnstrikeEmbed(i))
		}
	}

	// Lift the mute, queue ban... the strike applied, so removing the strike really removes its consequences.
	ended := helpers.RevokeStrikePunishments(s, i.GuildID, actor, view.Player.ID, strikeID)

	username := target.Username
	if view.Config != nil && view.Config.Username != "" {
		username = view.Config.Username
	}
	embed := helpers.ModerationUnstrikeSuccessEmbed(i, strikeID, target, username)
	if ended > 0 {
		embed.Description += fmt.Sprintf("\n> **Also lifted:** %d punishment(s) this strike applied", ended)
	}
	helpers.LogCommandResult(s, i.GuildID, "unstrike", actor, embed)
	helpers.SendPunishmentAlert(s, i.GuildID, helpers.PunishmentNotice{Command: "strike", Action: "revoked", User: target, Username: username, Type: "strike", Reason: "Strike voided by staff", By: actor})
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
