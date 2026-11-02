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
	Name:         "serverunban",
	Requirements: types.PunishmentRequirements("server_ban_logs"),
	Description:  "Revokes a guild-wide ban by user ID.",
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
	var held []uint
	for _, entry := range active.Data {
		if entry.Type == "server_ban" {
			held = append(held, entry.ID)
		}
	}
	found := len(held) > 0

	// 4. Validation Checks
	_, banErr := s.GuildBan(i.GuildID, targetID)
	discordBanned := banErr == nil
	if !found && !discordBanned {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationUnbanNotActiveEmbed(i))
	}

	// 5. Lift the Discord ban first: if it cannot be lifted the ban stays recorded as active, so staff can fix the bot's
	// permissions and run the command again. Then revoke (not delete) the rows and record the history entry.
	if discordBanned {
		if err := s.GuildBanDelete(i.GuildID, targetID); err != nil && !helpers.IsDiscordNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationUnbanEmbed(i))
		}
	}
	unbanned := true
	for _, id := range held {
		if err := database.Must().Punishments.Revoke(actor, i.GuildID, id, actor, reason); err != nil && !sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationUnbanEmbed(i))
		}
	}
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
	helpers.LogCommandResult(s, i.GuildID, "serverunban", actor, embed)
	helpers.SendPunishmentAlert(s, i.GuildID, helpers.PunishmentNotice{Command: "serverban", Action: "revoked", User: &discordgo.User{ID: targetID}, Username: username, Type: "server_ban", Reason: reason, By: actor})
	return helpers.EditInteractionEmbed(s, i, embed)
}
