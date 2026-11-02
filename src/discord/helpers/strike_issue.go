package helpers

import (
	"fmt"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
)

// IssueStrike gives target a strike for reason, as the strike ladder prescribes: the step's ELO is taken, the strike is
// recorded, and the step's punishment (warning, mute, queue ban...) is applied through the same path as the punishment
// commands. It returns the embed that tells what happened, and whether the strike was recorded. actor is the staff member
// (or actorSystem for a strike a vote decided); it is who the log and the player's alert credit.
func IssueStrike(s *discordgo.Session, guildID, actor string, target *discordgo.User, reason, strikeType string) (*discordgo.MessageEmbed, bool) {
	// 1. The strike ladder (and the named level) must exist before anything is created
	if err := RequireStrikeLadder(guildID, strikeType); err != nil {
		return LadderNotConfiguredEmbed(err), false
	}

	// 2. Resolve the target's profile
	playerID, profile, err := EnsureModerationTarget(actor, guildID, target)
	if err != nil {
		return UnexpectedErrorModerationStrikeEmbed(nil), false
	}
	pid := sdk.UintToString(playerID)
	username := target.Username
	if profile != nil && profile.Username != "" {
		username = profile.Username
	}
	stats, err := database.Must().Stats.GetOrCreate(guildID, pid, 0)
	if err != nil {
		return UnexpectedErrorModerationStrikeEmbed(nil), false
	}

	// 3. The ladder picks the step for this offence count
	plan, err := PlanStrike(guildID, pid, strikeType, stats.SeasonID)
	if err != nil {
		return LadderNotConfiguredEmbed(err), false
	}

	// 4. Take the step's ELO (if any), then record the strike with what it actually took
	newStrike := sdk.NewStrike{StaffID: actor, Reason: reason, LadderLevel: plan.Level}
	if plan.EloRemoval > 0 {
		taken, err := Elo.StrikePenalty(actor, guildID, target.ID, plan.EloRemoval)
		if err != nil && taken == nil {
			return UnexpectedErrorModerationStrikeEmbed(nil), false
		}
		newStrike.EloOld, newStrike.EloNew, newStrike.EloChange = taken.Before, taken.After, taken.After-taken.Before
	}
	strike, err := database.Must().Strikes.Create(actor, guildID, pid, newStrike)
	if err != nil {
		if newStrike.EloChange < 0 { // no strike was recorded: give the ELO back
			_ = Elo.RefundStrike(actor, guildID, target.ID, &models.PlayerStrikesModel{SeasonID: stats.SeasonID, EloChange: newStrike.EloChange})
		}
		return UnexpectedErrorModerationStrikeEmbed(nil), false
	}

	// 5. Apply the step's punishment. It is recorded without a punishment-ladder level, so it does not count toward the
	// standard ladders.
	notes := ""
	if newStrike.EloChange < 0 {
		notes += fmt.Sprintf("\n> **ELO removed:** `%d` (`%d` → `%d`)", -newStrike.EloChange, newStrike.EloOld, newStrike.EloNew)
	}
	strikeReason := "Strike #" + sdk.UintToString(strike.ID) + ": " + reason
	if spec := LadderActionSpec(plan.Action); spec != nil {
		expiresAt, label := "", "Permanent"
		if spec.Duration && plan.Span > 0 {
			expiresAt, label = time.Now().Add(plan.Span).UTC().Format(time.RFC3339), FormatSpan(plan.Span)
		}
		if _, ok := ApplyPunishmentTo(s, guildID, actor, target, playerID, username, strikeReason, spec, "", expiresAt, label); ok {
			notes += fmt.Sprintf("\n> **Action:** %s (%s)", spec.Title, label)
		} else {
			notes += fmt.Sprintf("\n> **Action:** %s could not be applied (already active, or the member cannot be moderated).", spec.Action)
		}
	}
	notes += ApplyExtraActions(s, guildID, actor, target, playerID, username, strikeReason, plan.Also)

	embed := ModerationStrikeSuccessEmbedBy(actor, strike.ID, target, username, strikeType, reason)
	embed.Description += notes
	LogCommandResult(s, guildID, "strike", actor, embed)
	notice := PunishmentNotice{Command: "strike", Action: "applied", User: target, Username: username, Type: "strike", Reason: reason, By: actor}
	if newStrike.EloChange < 0 {
		notice.Elo = fmt.Sprintf("`%d` (`%d` → `%d`)", -newStrike.EloChange, newStrike.EloOld, newStrike.EloNew)
	}
	SendPunishmentAlert(s, guildID, notice)
	return embed, true
}
