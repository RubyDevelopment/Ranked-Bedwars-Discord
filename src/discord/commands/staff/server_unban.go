package staff

import (
	"fmt"
	"regexp"
	"strings"
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

var snowflakePattern = regexp.MustCompile(`^\d{17,20}$`)

func init() {
	helpers.Register(ServerUnbanCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ServerUnbanCommand removes a Discord guild ban by user ID.
var ServerUnbanCommand = &types.Command{
	Name:         "unban",
	Description:  "Revokes a guild-wide ban by user ID.",
	Requirements: []types.Requirement{{Type: types.ReqRole, Key: "staff_role"}},
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user_id",
				Description: "Discord user ID to unban",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the server unban",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeServerUnbanCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeServerUnbanCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User ID
	targetID := strings.TrimSpace(helpers.OptionString(i, "user_id"))
	if !snowflakePattern.MatchString(targetID) {
		return helpers.EditInteractionEmbed(s, i, serverUnbanInvalidUserIDEmbed())
	}

	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	if reason == "" {
		reason = "Server unban by staff"
	}
	actor := helpers.InteractionUserID(i)

	// 2. Resolve Target Profile
	// The target may have left (or never joined) the guild, and may never have
	// registered; resolve them as a moderation target regardless.
	playerID, profile, err := helpers.EnsureModerationTarget(actor, i.GuildID, &discordgo.User{ID: targetID, Username: targetID})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerUnbanEmbed())
	}
	pid := sdk.UintToString(playerID)

	// 3. Remove Active Server Bans
	active, err := database.Must().Punishments.ByPlayer(i.GuildID, pid, true, "", sdk.Page{})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerUnbanEmbed())
	}
	found := false
	for _, entry := range active.Data {
		if entry.Type != "server_ban" {
			continue
		}
		found = true
		if err := database.Must().Punishments.Delete(actor, i.GuildID, entry.ID); err != nil && !sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, unexpectedErrorServerUnbanEmbed())
		}
	}

	// 4. Validation Checks
	_, banErr := s.GuildBan(i.GuildID, targetID)
	discordBanned := banErr == nil
	if !found && !discordBanned {
		return helpers.EditInteractionEmbed(s, i, serverUnbanNotActiveEmbed())
	}

	// 5. Reverse Discord Ban & Record History
	unbanned := !discordBanned || s.GuildBanDelete(i.GuildID, targetID) == nil
	if found {
		_, _ = database.Must().Punishments.Create(actor, i.GuildID, pid, sdk.NewPunishment{
			StaffID: actor, Reason: reason, Type: "server_unban",
		})
	}

	username := targetID
	if profile != nil && profile.Username != "" {
		username = profile.Username
	}
	embed := serverUnbanSuccessEmbed(targetID, username, reason, unbanned)
	helpers.LogCommandResult(s, i.GuildID, "server_unban", actor, embed)
	helpers.SendPunishmentAlert(s, i.GuildID, "server_ban", "revoked", &discordgo.User{ID: targetID}, username, "server_ban", "removed", reason)
	return helpers.EditInteractionEmbed(s, i, embed)
}

// ============================================================================
// Embed Builders
// ============================================================================

func unexpectedErrorServerUnbanEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing the server unban. Please try again later or contact support.",
	})
}

func serverUnbanInvalidUserIDEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid User ID",
		Description: "That is not a valid Discord user ID. Provide the 17-20 digit ID of the user to unban.",
	})
}

func serverUnbanNotActiveEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Punishment Not Active",
		Description: "That player is not currently **server banned**. Verify their status with `/player_punishments`.",
	})
}

func serverUnbanSuccessEmbed(targetID, username, reason string, unbanned bool) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully processed the server unban. This action has been logged for staff review.\n\n> **User ID:** `%s`\n> **Username:** `%s`\n> **Reason:** %s",
		targetID, username, reason,
	)
	if !unbanned {
		description += "\n\nThe Discord unban could not be applied due to missing permissions."
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Server Unban Applied",
		Description: description,
	})
}
