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
	helpers.Register(PunishmentClearCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PunishmentClearCommand deletes a player's most recent active punishment, or a specific one by ID.
var PunishmentClearCommand = &types.Command{
	Name:         "clearpunishment",
	Requirements: types.StaffRequirements,
	Description:  "Clears a punishment from a player's record.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to clear a punishment from",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "id",
				Description: "Punishment ID to clear (defaults to their most recent active punishment)",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    false,
			},
		},
	},
	Execute: executePunishmentClearCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePunishmentClearCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PunishmentClearInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.PunishmentClearBotTargetEmbed(i))
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PunishmentPlayerNotFoundEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPunishmentClearEmbed(i))
	}
	pid := sdk.UintToString(view.Player.ID)
	actor := helpers.InteractionUserID(i)

	// 3. Resolve Punishment
	punishmentID := uint(helpers.OptionInt(i, "id"))
	punishType := ""
	if punishmentID == 0 {
		active, err := database.Must().Punishments.ByPlayer(i.GuildID, pid, true, "", sdk.Page{})
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPunishmentClearEmbed(i))
		}
		if len(active.Data) == 0 {
			return helpers.EditInteractionEmbed(s, i, helpers.PunishmentClearNoneActiveEmbed(i))
		}
		punishmentID = active.Data[0].ID
		punishType = active.Data[0].Type
	} else {
		entry, err := database.Must().Punishments.Get(i.GuildID, punishmentID)
		if err != nil && !sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPunishmentClearEmbed(i))
		}
		if entry == nil || entry.PlayerID != view.Player.ID {
			return helpers.EditInteractionEmbed(s, i, helpers.PunishmentClearNotFoundEmbed(i))
		}
		punishType = entry.Type
	}

	// 4. Delete Punishment from Database
	if err := database.Must().Punishments.Delete(actor, i.GuildID, punishmentID); err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PunishmentClearNotFoundEmbed(i))
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPunishmentClearEmbed(i))
	}

	embed := helpers.PunishmentClearSuccessEmbed(i, punishmentID, punishType, target)
	helpers.LogCommandResult(s, i.GuildID, "punishment_clear", actor, embed)
	return helpers.EditInteractionEmbed(s, i, embed)
}
