package general

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/factionmgr"
	"ranked-bedwars/src/types"
)

// Every embed the faction commands answer with.

func FactionInvalidSubcommandEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "That faction subcommand does not exist. Use the slash command menu to see the available options.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func UnexpectedErrorFactionEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing the faction request. Please contact an administrator for assistance or try again later.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionRefusedEmbed(message string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Faction Request Refused",
		Description: message,
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You must be registered to use factions. Run `/register` first, then try again.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionNotInFactionEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not in a Faction",
		Description: "You are not in a faction. Use `/faction create` to found one or ask a faction's Founder or an Officer to invite you.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionPlayerHasNoneEmbed(userID string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "No Faction",
		Description: fmt.Sprintf("<@%s> is not in a faction.", userID),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionNotFoundEmbed(name string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Faction Not Found",
		Description: fmt.Sprintf("No faction is named or tagged **%s**. Check the spelling or use `/faction leaderboard` to browse the factions.", name),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionPlayerNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "Could not find that player. Select a user from the command options.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "Automated bot accounts cannot be in a faction. Select a registered player instead.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionTargetNotRegisteredEmbed(userID string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild. They must run `/register` before they can join a faction.", userID),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionNotYourInvitationEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not Your Invitation",
		Description: "Only the invited player can answer this invitation.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionNothingToChangeEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Nothing to Change",
		Description: "Fill in at least one option: `name`, `tag`, `color`, `icon` or `symbol`.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionCreatedEmbed(p *database.FactionProfile) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Faction Founded",
		Description: fmt.Sprintf("**%s** `[%s]` is now a faction, and you are its **Founder**.\n\n> **Seats:** %d (%d Officer)\n> **Level:** %d\n\nInvite players with `/faction invite`. Every ranked win by a member earns the faction trophies.", p.Faction.Name, p.Faction.Tag, p.Slots, p.OfficerSlots, p.Level),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionInviteSentEmbed(targetID, faction string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Invite Sent",
		Description: fmt.Sprintf("Invited <@%s> to **%s**. They can accept with the buttons below the invitation or with `/faction accept`.", targetID, faction),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionInviteSentNoPostEmbed(targetID, faction string) *discordgo.MessageEmbed {
	return helpers.Preset("caution", types.PresetOptions{
		Title:       "Invite Saved",
		Description: fmt.Sprintf("Invited <@%s> to **%s**, but the invitation message could not be posted here. Ask them to run `/faction accept`.", targetID, faction),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionInvitationEmbed(p *database.FactionProfile, inviterID string, expires time.Time) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Faction Invitation",
		Description: fmt.Sprintf("<@%s> invited you to join **%s**.\n\n> **Level:** %d\n> **Members:** %d/%d\n> **Founder:** <@%s>\n> **Expires:** <t:%d:R>", inviterID, factionHeading(p.Faction.Name, p.DisplayTag), p.Level, len(p.Members), p.Slots, p.Founder, expires.Unix()),
		Color:       factionColor(p),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionInvitesEmbed(list []database.FactionInvite) *discordgo.MessageEmbed {
	if len(list) == 0 {
		return helpers.Preset("info", types.PresetOptions{
			Title:       "No Invitations",
			Description: "You have no faction invitations right now. Ask a faction's Founder or an Officer to invite you.",
			Thumbnail:   helpers.ThumbPlayers,
		})
	}
	var b strings.Builder
	for _, inv := range list {
		fmt.Fprintf(&b, "> **%s** `[%s]` · from <@%s> · expires <t:%d:R>\n", inv.FactionName, inv.FactionTag, inv.InvitedBy, inv.ExpiresAt.Unix())
	}
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Faction Invitations",
		Description: "Accept one with `/faction accept`, or decline it with `/faction decline`.\n\n" + b.String(),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionJoinedEmbed(p *database.FactionProfile) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Faction Joined",
		Description: fmt.Sprintf("You joined **%s**!\n\n> **Members:** %d/%d\n> **Level:** %d\n> **Founder:** <@%s>\n\nEvery ranked win you earn adds trophies to the faction.", factionHeading(p.Faction.Name, p.DisplayTag), len(p.Members), p.Slots, p.Level, p.Founder),
		Color:       factionColor(p),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionDeclinedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Invitation Declined",
		Description: "You declined the faction invitation. If you change your mind, ask for a new one.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionLeftEmbed(name string) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Faction Left",
		Description: fmt.Sprintf("You left **%s**. You can create a faction with `/faction create` or accept another invitation.", name),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionKickedEmbed(targetID, name string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Member Removed",
		Description: fmt.Sprintf("Removed <@%s> from **%s**.", targetID, name),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionRoleChangedEmbed(targetID, name string, promoted bool) *discordgo.MessageEmbed {
	if promoted {
		return helpers.Preset("success", types.PresetOptions{
			Title:       "Member Promoted",
			Description: fmt.Sprintf("<@%s> is now an **Officer** of **%s**. They can invite players and remove members.", targetID, name),
			Thumbnail:   helpers.ThumbPlayers,
		})
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Officer Demoted",
		Description: fmt.Sprintf("<@%s> is now a **Member** of **%s**.", targetID, name),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionTransferredEmbed(targetID, name string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Ownership Transferred",
		Description: fmt.Sprintf("<@%s> is now the **Founder** of **%s**. You stay in the faction as an Officer, or a Member when no Officer seat is free.", targetID, name),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionDisbandUnconfirmedEmbed(name string) *discordgo.MessageEmbed {
	return helpers.Preset("caution", types.PresetOptions{
		Title:       "Confirm Disband",
		Description: fmt.Sprintf("Disbanding **%s** removes every member and erases its trophies and badges. This cannot be undone. Run `/faction disband` again with `confirm` set to true.", name),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func FactionDisbandedEmbed(name string) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Faction Disbanded",
		Description: fmt.Sprintf("**%s** has been disbanded. You can create a new faction with `/faction create`.", name),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionCustomizedEmbed(p *database.FactionProfile, changed []string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Faction Updated",
		Description: fmt.Sprintf("Updated the %s of **%s**. Members' names and the faction role are refreshing now.\n\n> **Tag:** %s", strings.Join(changed, ", "), p.Faction.Name, orDash(p.DisplayTag)),
		Color:       factionColor(p),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func FactionLeaderboardEmptyEmbed(board string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Faction Leaderboard · " + strings.ToUpper(board[:1]) + board[1:],
		Description: "No factions exist yet. Be the first with `/faction create`.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func FactionLeaderboardEmptyPageEmbed(page int) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Page Empty",
		Description: fmt.Sprintf("There are no leaderboard entries on page `%d`. Try a lower page number.", page),
		Thumbnail:   helpers.ThumbFlag,
	})
}

// FactionLeaderboardEmbed lists a page of factions with their level, trophies and rank.
func FactionLeaderboardEmbed(rows []database.FactionRow, board string, page, totalPages int) *discordgo.MessageEmbed {
	var b strings.Builder
	for _, r := range rows {
		trophies := r.LifetimeTrophies
		if board == "seasonal" {
			trophies = r.SeasonTrophies
		}
		if r.Legendary {
			b.WriteString("👑 ")
		}
		fmt.Fprintf(&b, "**#%d** %s · Lv **%d** · %s · %d members", r.Rank, factionHeading(r.Name, r.DisplayTag), r.Level, factionmgr.Trophies(trophies), r.MemberCount)
		if r.Prestige != "" {
			b.WriteString(" · " + r.Prestige)
		}
		b.WriteString("\n")
	}
	title := "Faction Leaderboard · Lifetime"
	if board == "seasonal" {
		title = "Faction Leaderboard · Seasonal"
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       title,
		Description: b.String(),
		FooterText:  fmt.Sprintf("Page %d/%d", page, totalPages),
		Thumbnail:   helpers.ThumbPlayers,
	})
}
