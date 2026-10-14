package staff

import (
	"fmt"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/sdk"
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
	Description:  "Clears a punishment from a player's record.",
	Requirements: []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}},
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
		return helpers.EditInteractionEmbed(s, i, punishmentClearInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, punishmentClearBotTargetEmbed())
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PunishmentPlayerNotFoundEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPunishmentClearEmbed())
	}
	pid := sdk.UintToString(view.Player.ID)
	actor := helpers.InteractionUserID(i)

	// 3. Resolve Punishment
	punishmentID := uint(helpers.OptionInt(i, "id"))
	punishType := ""
	if punishmentID == 0 {
		active, err := database.Must().Punishments.ByPlayer(i.GuildID, pid, true, "", sdk.Page{})
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorPunishmentClearEmbed())
		}
		if len(active.Data) == 0 {
			return helpers.EditInteractionEmbed(s, i, punishmentClearNoneActiveEmbed())
		}
		punishmentID = active.Data[0].ID
		punishType = active.Data[0].Type
	} else {
		entry, err := database.Must().Punishments.Get(i.GuildID, punishmentID)
		if err != nil && !sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorPunishmentClearEmbed())
		}
		if entry == nil || entry.PlayerID != view.Player.ID {
			return helpers.EditInteractionEmbed(s, i, punishmentClearNotFoundEmbed())
		}
		punishType = entry.Type
	}

	// 4. Delete Punishment from Database
	if err := database.Must().Punishments.Delete(actor, i.GuildID, punishmentID); err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, punishmentClearNotFoundEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorPunishmentClearEmbed())
	}

	embed := punishmentClearSuccessEmbed(punishmentID, punishType, target)
	helpers.LogCommandResult(s, i.GuildID, "punishment_clear", actor, embed)
	return helpers.EditInteractionEmbed(s, i, embed)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorPunishmentClearEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while clearing the punishment. Please try again later or contact support.",
	})
}

func punishmentClearInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to clear a punishment from. Select a registered player from the command options.",
	})
}

func punishmentClearBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive punishments. Target a registered player instead.",
	})
}

func punishmentClearNoneActiveEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Active Punishments",
		Description: "This player has no active punishments to clear. Their history can be reviewed with `/player_punishments`.",
	})
}

func punishmentClearNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Punishment Not Found",
		Description: "No punishment with that ID exists for this player in this guild. Double-check the ID from `/player_punishments`.",
	})
}

func punishmentClearSuccessEmbed(id uint, punishType string, user *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Punishment Cleared",
		Description: fmt.Sprintf(
			"Successfully removed the punishment record. This action has been logged for staff review.\n\n> **Punishment ID:** `#%d`\n> **Player:** <@%s>\n> **Type:** `%s`",
			id, user.ID, punishType,
		),
	})
}
