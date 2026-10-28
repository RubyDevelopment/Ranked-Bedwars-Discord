package staff

import (
	"regexp"
	"strings"
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

var snowflakePattern = regexp.MustCompile(`^\d{17,20}$`)

func init() {
	helpers.Register(ModerationUnbanCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationUnbanCommand removes a Discord guild ban by user ID.
var ModerationUnbanCommand = &types.Command{
	Name:         "unban",
	Requirements: types.PunishmentRequirements("server_ban_logs"),
	Description:  "Revokes a guild-wide ban by user ID.",
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
	Execute: executeModerationUnbanCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeModerationUnbanCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User ID
	targetID := strings.TrimSpace(helpers.OptionString(i, "user_id"))
	if !snowflakePattern.MatchString(targetID) {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationUnbanInvalidUserIDEmbed(i))
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
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationUnbanEmbed(i))
	}
	pid := sdk.UintToString(playerID)

	// 3. Remove Active Server Bans
	active, err := database.Must().Punishments.ByPlayer(i.GuildID, pid, true, "", sdk.Page{})
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationUnbanEmbed(i))
	}
	found := false
	for _, entry := range active.Data {
		if entry.Type != "server_ban" {
			continue
		}
		found = true
		if err := database.Must().Punishments.Delete(actor, i.GuildID, entry.ID); err != nil && !sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationUnbanEmbed(i))
		}
	}

	// 4. Validation Checks
	_, banErr := s.GuildBan(i.GuildID, targetID)
	discordBanned := banErr == nil
	if !found && !discordBanned {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationUnbanNotActiveEmbed(i))
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
	embed := helpers.ModerationUnbanSuccessEmbed(i, targetID, username, reason, unbanned)
	helpers.LogCommandResult(s, i.GuildID, "unban", actor, embed)
	helpers.SendPunishmentAlert(s, i.GuildID, "ban", "revoked", &discordgo.User{ID: targetID}, username, "server_ban", "removed", reason)
	return helpers.EditInteractionEmbed(s, i, embed)
}
