package staff

import (
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
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
	helpers.Register(ModerationStrikeCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// ModerationStrikeCommand issues an official strike to a player.
var ModerationStrikeCommand = &types.Command{
	Name:         "strike",
	Requirements: types.PunishmentRequirements("strike_logs", "queue_banned_role"),
	Description:  "Issues an official strike penalty to a player.",
	Ephemeral:    true,
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Discord user to strike",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "reason",
				Description: "Reason for the strike",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
			{
				Name:        "type",
				Description: "An optional label for the type of strike",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	},
	Execute: executeModerationStrikeCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executeModerationStrikeCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// 1. Validate Target User
	target := helpers.OptionUser(s, i, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationStrikeInvalidTargetEmbed(i))
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationStrikeBotTargetEmbed(i))
	}

	// 2. Validate Reason
	reason := strings.TrimSpace(helpers.OptionString(i, "reason"))
	if reason == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.ModerationStrikeReasonRequiredEmbed(i))
	}
	strikeType := strings.TrimSpace(helpers.OptionString(i, "type"))
	actor := helpers.InteractionUserID(i)

	// 3. Require the strike ladder (and the named level) before anything is created
	if err := helpers.RequireStrikeLadder(i.GuildID, strikeType); err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.LadderNotConfiguredEmbed(err))
	}

	// 4. Resolve Target Profile
	playerID, profile, err := helpers.EnsureModerationTarget(actor, i.GuildID, target)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationStrikeEmbed(i))
	}
	pid := sdk.UintToString(playerID)
	username := target.Username
	if profile != nil && profile.Username != "" {
		username = profile.Username
	}
	stats, err := database.Must().Stats.GetOrCreate(i.GuildID, pid, 0)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationStrikeEmbed(i))
	}

	// 5. The ladder picks the step for this offence count
	plan, err := helpers.PlanStrike(i.GuildID, pid, strikeType, stats.SeasonID)
	if err != nil {
		return helpers.EditInteractionEmbed(s, i, helpers.LadderNotConfiguredEmbed(err))
	}

	// 6. Take the step's ELO (if any), then record the strike with what it actually took
	newStrike := sdk.NewStrike{StaffID: actor, Reason: reason, LadderLevel: plan.Level}
	if plan.EloRemoval > 0 {
		taken, err := helpers.Elo.StrikePenalty(actor, i.GuildID, target.ID, plan.EloRemoval)
		if err != nil && taken == nil {
			return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationStrikeEmbed(i))
		}
		newStrike.EloOld, newStrike.EloNew, newStrike.EloChange = taken.Before, taken.After, taken.After-taken.Before
	}
	strike, err := database.Must().Strikes.Create(actor, i.GuildID, pid, newStrike)
	if err != nil {
		if newStrike.EloChange < 0 { // no strike was recorded: give the ELO back
			_ = helpers.Elo.RefundStrike(actor, i.GuildID, target.ID, &models.PlayerStrikesModel{SeasonID: stats.SeasonID, EloChange: newStrike.EloChange})
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorModerationStrikeEmbed(i))
	}

	// 7. Apply the step's punishment (warning, mute, queue ban...) through the same path as the punishment commands.
	// It is recorded without a punishment-ladder level, so it does not count toward the standard ladders.
	notes := ""
	if newStrike.EloChange < 0 {
		notes += fmt.Sprintf("\n> **ELO removed:** `%d` (`%d` → `%d`)", -newStrike.EloChange, newStrike.EloOld, newStrike.EloNew)
	}
	if spec := helpers.LadderActionSpec(plan.Action); spec != nil {
		expiresAt, label := "", "Permanent"
		if spec.Duration && plan.Span > 0 {
			expiresAt, label = time.Now().Add(plan.Span).UTC().Format(time.RFC3339), helpers.FormatSpan(plan.Span)
		}
		if _, ok := helpers.ApplyPunishmentTo(s, i.GuildID, actor, target, playerID, username, "Strike #"+sdk.UintToString(strike.ID)+": "+reason, spec, "", expiresAt, label); ok {
			notes += fmt.Sprintf("\n> **Action:** %s (%s)", spec.Title, label)
		} else {
			notes += fmt.Sprintf("\n> **Action:** %s could not be applied (already active, or the member cannot be moderated).", spec.Action)
		}
	}

	notes += helpers.ApplyExtraActions(s, i.GuildID, actor, target, playerID, username, "Strike #"+sdk.UintToString(strike.ID)+": "+reason, plan.Also)

	embed := helpers.ModerationStrikeSuccessEmbed(i, strike.ID, target, username, strikeType, reason)
	embed.Description += notes
	helpers.LogCommandResult(s, i.GuildID, "strike", actor, embed)
	helpers.SendPunishmentAlert(s, i.GuildID, "strike", "applied", target, username, "strike", "", reason)
	return helpers.EditInteractionEmbed(s, i, embed)
}
