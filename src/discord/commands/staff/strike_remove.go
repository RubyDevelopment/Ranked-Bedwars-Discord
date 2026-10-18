package staff

import (
	"fmt"
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
	helpers.Register(StrikeRemoveCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// StrikeRemoveCommand voids a player's most recent strike, or a specific one by ID.
var StrikeRemoveCommand = &types.Command{
	Name:         "unstrike",
	Description:  "Removes a specific strike entry from a player's record.",
	Requirements: []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}, {Type: types.ReqRole, Key: "queue_banned_role"}},
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
		return helpers.EditInteractionEmbed(s, i, strikeRemoveInvalidTargetEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, strikeRemoveBotTargetEmbed())
	}

	// 2. Fetch Existing Player Profile
	view, err := database.Must().Player.Get(i.GuildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, strikeRemovePlayerNotFoundEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorStrikeRemoveEmbed())
	}
	pid := sdk.UintToString(view.Player.ID)
	actor := helpers.InteractionUserID(i)

	// 3. Resolve Strike
	strikeID := uint(helpers.OptionInt(i, "id"))
	if strikeID == 0 {
		latest, err := latestUnvoidedStrikeID(i.GuildID, pid)
		if err != nil {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorStrikeRemoveEmbed())
		}
		if latest == 0 {
			return helpers.EditInteractionEmbed(s, i, strikeRemoveNoStrikesEmbed())
		}
		strikeID = latest
	} else {
		strike, err := database.Must().Strikes.Get(i.GuildID, strikeID)
		if err != nil && !sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorStrikeRemoveEmbed())
		}
		if strike == nil || strike.PlayerID != view.Player.ID {
			return helpers.EditInteractionEmbed(s, i, strikeRemoveNotFoundEmbed())
		}
		if strike.IsVoided {
			return helpers.EditInteractionEmbed(s, i, strikeRemoveAlreadyVoidedEmbed())
		}
	}

	// 4. Void Strike in Database
	if err := database.Must().Strikes.Delete(actor, i.GuildID, strikeID); err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, strikeRemoveNotFoundEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorStrikeRemoveEmbed())
	}

	username := target.Username
	if view.Config != nil && view.Config.Username != "" {
		username = view.Config.Username
	}
	embed := strikeRemoveSuccessEmbed(strikeID, target, username)
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

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorStrikeRemoveEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while voiding the strike. Please try again later or contact support.",
	})
}

func strikeRemoveInvalidTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You must specify a player to remove a strike from. Select a registered player from the command options.",
	})
}

func strikeRemoveBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot receive strikes. Target a registered player instead.",
	})
}

func strikeRemovePlayerNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: "That player is not registered in this guild. Only registered players can receive strikes.",
	})
}

func strikeRemoveNoStrikesEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Strikes Found",
		Description: "This player has no active strikes to void. Their strike history can be reviewed with `/strike_list`.",
	})
}

func strikeRemoveNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Strike Not Found",
		Description: "No strike with that ID exists for this player in this guild. Double-check the ID from `/strike_list`.",
	})
}

func strikeRemoveAlreadyVoidedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Already Voided",
		Description: "That strike has already been voided. No further action is needed.",
	})
}

func strikeRemoveSuccessEmbed(id uint, user *discordgo.User, username string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: "Strike Voided",
		Description: fmt.Sprintf(
			"Successfully voided the strike. This action has been logged for staff review.\n\n> **Strike ID:** `#%d`\n> **Player:** <@%s> (**%s**)",
			id, user.ID, username,
		),
		Thumbnail: helpers.MinecraftBustAvatarURL(username),
	})
}
