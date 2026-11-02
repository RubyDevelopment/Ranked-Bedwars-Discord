package punish

import (
	"errors"
	"fmt"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/bwmarrin/discordgo"
)

func punishmentSelfTargetEmbed(action string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: fmt.Sprintf("You cannot %s yourself. Select a different member to moderate.", action),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func punishmentReasonRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Reason Required",
		Description: "You must provide a reason when issuing a punishment. Include a clear explanation in the reason field.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func punishmentInvalidDurationEmbed(err error) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Duration",
		Description: "The duration could not be read: " + err.Error() + ".",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func punishmentHierarchyEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Hierarchy Error",
		Description: "That member cannot be moderated because their highest role is above or equal to mine. Adjust the role hierarchy or ask a higher-ranked moderator.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

// PunishmentPlayerNotFoundEmbed is shown when the target has no player profile in this guild.
func PunishmentPlayerNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "That player is not registered in this guild. They must register first before punishments can be managed.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func punishmentAlreadyActiveEmbed(adjective string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Punishment Already Active",
		Description: fmt.Sprintf("That player is already **%s**. Check `/punishments` for details.", adjective),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func punishmentNotActiveEmbed(adjective string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Punishment Not Active",
		Description: fmt.Sprintf("That player is not currently **%s**. Verify their status with `/punishments`.", adjective),
		Thumbnail:   helpers.ThumbFlag,
	})
}

// PunishmentExpiredLogEmbed is the line the moderation log gets when a punishment runs out.
func PunishmentExpiredLogEmbed(userID string, p models.PlayerPunishmentsModel) *discordgo.MessageEmbed {
	description := fmt.Sprintf("> **Player:** <@%s>\n> **Type:** `%s`\n> **Reason:** %s", userID, helpers.PunishmentTypeLabel(p.Type), p.Reason)
	description += helpers.PunishmentBy("Issued by", p.StaffID)
	return &discordgo.MessageEmbed{
		Title:       "Punishment Expired",
		Description: description,
		Timestamp:   time.Now().Format(time.RFC3339),
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: helpers.ThumbHammer},
	}
}

// PunishmentAlertEmbed is the alert a punishment that was applied, revoked or expired posts to the alerts channel.
func PunishmentAlertEmbed(n PunishmentNotice) *discordgo.MessageEmbed {
	title, byLabel := "Punishment Applied", "Issued by"
	switch n.Action {
	case "revoked":
		title, byLabel = "Punishment Revoked", "Removed by"
	case "expired":
		title = "Punishment Expired"
	}

	description := fmt.Sprintf("> **Player:** <@%s> (**%s**)\n> **Type:** `%s`", n.User.ID, n.Username, helpers.PunishmentTypeLabel(n.Type))
	if n.Duration != "" {
		description += fmt.Sprintf("\n> **Duration:** `%s`\n> **Expires:** %s", n.Duration, punishmentExpiry(n.Expires))
	}
	if n.Elo != "" {
		description += "\n> **ELO removed:** " + n.Elo
	}
	description += "\n> **Reason:** " + n.Reason
	description += helpers.PunishmentBy(byLabel, n.By)

	return &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Timestamp:   time.Now().Format(time.RFC3339),
		Thumbnail:   &discordgo.MessageEmbedThumbnail{URL: helpers.ThumbHammer},
	}
}

// LadderNotConfiguredEmbedOf tells staff what to set up on the dashboard (or that something went wrong reading it).
func LadderNotConfiguredEmbedOf(err error) *discordgo.MessageEmbed {
	if !IsLadderNotConfigured(err) {
		return UnexpectedErrorPunishmentEmbed("punishment")
	}
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Ladder Not Configured",
		Description: err.Error(),
		Thumbnail:   helpers.ThumbFlag,
	})
}

// LadderNotConfiguredEmbed is LadderNotConfiguredEmbedOf for commands outside this file (strikes).
func LadderNotConfiguredEmbed(err error) *discordgo.MessageEmbed {
	return LadderNotConfiguredEmbedOf(err)
}

func UnexpectedErrorPunishmentEmbed(action string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: fmt.Sprintf("An unexpected error occurred while processing the %s. Please try again later or contact support.", action),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func punishmentInvalidTargetEmbed(action string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: fmt.Sprintf("You must specify a player to %s. Select a member from the command options.", action),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func punishmentBotTargetEmbed(adjective string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: fmt.Sprintf("Automated bot accounts cannot be %s. Target a registered player instead.", adjective),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func punishmentMemberNotFoundEmbed(action string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Member Not Found",
		Description: fmt.Sprintf("That user is not currently in this guild. They must be in the guild to apply a %s.", action),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func punishmentAppliedEmbed(spec *ApplySpec, user *discordgo.User, username, duration, expiresAt, reason, actor string, applied bool) *discordgo.MessageEmbed {
	description := fmt.Sprintf(
		"Successfully recorded %s. This action has been logged for staff review.\n\n> **Player:** <@%s> (**%s**)\n> **Type:** `%s`\n",
		spec.Summary, user.ID, username, helpers.PunishmentTypeLabel(spec.Type),
	)
	if spec.Duration {
		description += fmt.Sprintf("> **Duration:** `%s`\n> **Expires:** %s\n", duration, punishmentExpiry(expiresAt))
	}
	description += fmt.Sprintf("> **Reason:** %s", reason)
	description += helpers.PunishmentBy("Issued by", actor)

	if applied && spec.AppliedNote != "" {
		description += "\n\n" + spec.AppliedNote
	} else if !applied && spec.FailedNote != "" {
		description += "\n\n" + spec.FailedNote
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       spec.Title,
		Description: description,
		Thumbnail:   helpers.ThumbHammer,
	})
}

func punishmentRevokedEmbed(action, pType string, user *discordgo.User, reason, actor string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title: action + " Removed",
		Description: fmt.Sprintf(
			"Successfully removed the %s. This action has been logged for staff review.\n\n> **Player:** <@%s>\n> **Type:** `%s`\n> **Reason:** %s%s",
			strings.ToLower(action), user.ID, helpers.PunishmentTypeLabel(pType), reason, helpers.PunishmentBy("Removed by", actor),
		),
		Thumbnail: helpers.ThumbHammer,
	})
}

// IsLadderNotConfigured reports whether err is an ErrLadderNotConfigured.
func IsLadderNotConfigured(err error) bool {
	var target *ErrLadderNotConfigured
	return errors.As(err, &target)
}
