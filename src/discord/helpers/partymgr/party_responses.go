package partymgr

import (
	"errors"
	"fmt"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
	apputil "ranked-bedwars/src/utility"

	"github.com/bwmarrin/discordgo"
)

func PartyInvitationDeclinedEmbed(userID string) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Invitation Declined",
		Description: fmt.Sprintf("<@%s> declined your party invitation. You can invite another player or ask them to request a new invitation.", userID),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyDeclinedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Invitation Declined",
		Description: "You declined the party invitation. If you change your mind, ask the leader to send you a new invitation.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyJoinedEmbed(leaderID string, members, maxSize int) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Party Joined",
		Description: fmt.Sprintf("You successfully joined <@%s>'s party!\n\n> **Party Size:** %d/%d\n> **Leader:** <@%s>\n\nYou can now queue together for matches.", leaderID, members, maxSize, leaderID),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func partyPlayerJoinedEmbed(userID string, members, maxSize int) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Player Joined",
		Description: fmt.Sprintf("<@%s> has joined your party!\n\n> **Party Size:** %d/%d\n\nYour party is now ready to queue for matches.", userID, members, maxSize),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func partyDisbandedByLeaderEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "The party leader has disbanded the party. You can create a new party with `/party create` at any time.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func partyDisbandedLastMemberEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded because all other members have left. You can create a new party with `/party create` at any time.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func partyDisbandedInactivityEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded due to inactivity. Parties automatically disband after a period of no activity. You can create a new party with `/party create` at any time.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func partyDisbandedSystemOffEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been disbanded because parties have been disabled in this guild. Contact an administrator for more information.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func partyKickedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Removed from Party",
		Description: "You have been removed from the party by its leader. You can create a new party with `/party create` or accept invitations from other players.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func partyLeaderChangedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "You Are the Party Leader",
		Description: "The previous party leader has left, so you are now the party leader. You can manage the party, invite members, kick players, and disband the party.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func partyNotYourInvitationEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not Your Invitation",
		Description: "Only the invited player can answer this invitation.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PartyErrorEmbed(err error) *discordgo.MessageEmbed {
	var d *EloDenial
	if errors.As(err, &d) {
		return helpers.Preset("error", types.PresetOptions{
			Title:       "Party ELO Limit",
			Description: fmt.Sprintf("This party would exceed the ELO limit: the **%s** ELO would be **%d**, above the limit of **%d**.", d.Method, d.Aggregated, d.Max),
			Thumbnail:   helpers.ThumbFlag,
		})
	}
	for _, e := range partyErrorTexts {
		if errors.Is(err, e.err) {
			return helpers.Preset("error", types.PresetOptions{Title: e.title, Description: e.text, Thumbnail: helpers.ThumbFlag})
		}
	}
	return nil
}

func partyErrorEmbedOrGeneric(err error) *discordgo.MessageEmbed {
	if e := PartyErrorEmbed(err); e != nil {
		return e
	}
	apputil.DiscordLogger.Error(fmt.Sprintf("[Party] button action failed: %v", err))
	return helpers.CommandErrorEmbed()
}
