package general

import (
	"fmt"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/partymgr"
	"ranked-bedwars/src/types"
	apputil "ranked-bedwars/src/utility"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.Register(PartyManageCommand)
}

// ============================================================================
// Command Definition
// ============================================================================

// PartyManageCommand Manages party matching, custom party groups, and team formations.
var PartyManageCommand = &types.Command{
	Name:         "party",
	Requirements: types.PartyRequirements,
	Description:  "Manages party matching, custom party groups, and team formations.",
	Cooldown:     3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "create", Description: "Create a new party group", Type: discordgo.ApplicationCommandOptionSubCommand},
			{Name: "disband", Description: "Disband your current party group", Type: discordgo.ApplicationCommandOptionSubCommand},
			{
				Name: "invite", Description: "Invite a player to your party group", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "Player to invite", Type: discordgo.ApplicationCommandOptionUser, Required: true},
				},
			},
			{
				Name: "accept", Description: "Accept a party invitation", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "leader", Description: "The party leader who invited you", Type: discordgo.ApplicationCommandOptionUser, Required: true},
				},
			},
			{
				Name: "decline", Description: "Decline a party invitation", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "leader", Description: "The party leader who invited you", Type: discordgo.ApplicationCommandOptionUser, Required: true},
				},
			},
			{Name: "leave", Description: "Leave your current party group", Type: discordgo.ApplicationCommandOptionSubCommand},
			{
				Name: "kick", Description: "Kick a player from your party group", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "Player to kick", Type: discordgo.ApplicationCommandOptionUser, Required: true},
				},
			},
			{
				Name: "promote", Description: "Promote a member to party leader", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "New party leader", Type: discordgo.ApplicationCommandOptionUser, Required: true},
				},
			},
			{
				Name: "invites", Description: "Open or close invites to your party", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "open", Description: "true to accept invites, false to pause them", Type: discordgo.ApplicationCommandOptionBoolean, Required: true},
				},
			},
			{
				Name: "info", Description: "View current party members and party state", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "Show this player's party (default: yours)", Type: discordgo.ApplicationCommandOptionUser, Required: false},
				},
			},
		},
	},
	Execute: executePartyManageCommand,
}

// ============================================================================
// Command Execution
// ============================================================================

func executePartyManageCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	partymgr.BotSession.Store(s)
	subcommand, options := types.SelectedSubcommand(i)
	if subcommand == "" {
		return helpers.EditInteractionEmbed(s, i, PartyInvalidSubcommandEmbed())
	}

	userID := helpers.InteractionUserID(i)
	guildID := i.GuildID

	// Check registration
	_, err := database.Must().Player.Get(guildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, PartyRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPartyEmbed())
	}

	var result error
	switch subcommand {
	case "create":
		result = partyCreate(s, i, guildID, userID)
	case "invite":
		result = partyInvite(s, i, guildID, userID, options)
	case "accept":
		result = partyAccept(s, i, guildID, userID, options)
	case "decline":
		result = partyDecline(s, i, guildID, userID, options)
	case "leave":
		result = partyLeave(s, i, guildID, userID)
	case "kick":
		result = partyKick(s, i, guildID, userID, options)
	case "promote":
		result = partyPromote(s, i, guildID, userID, options)
	case "disband":
		result = partyDisband(s, i, guildID, userID)
	case "invites":
		result = partyInvites(s, i, guildID, userID, options)
	case "info":
		result = partyInfo(s, i, guildID, userID, options)
	default:
		return helpers.EditInteractionEmbed(s, i, PartyInvalidSubcommandEmbed())
	}

	if result != nil {
		if e := partymgr.PartyErrorEmbed(result); e != nil {
			return helpers.EditInteractionEmbed(s, i, e)
		}
		return helpers.EditInteractionEmbed(s, i, UnexpectedErrorPartyEmbed())
	}

	return nil
}

// ============================================================================
// Subcommands
// ============================================================================

func partyCreate(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string) error {
	if _, err := partymgr.Parties.Create(guildID, userID); err != nil {
		return err
	}
	cfg := partymgr.Parties.Config(guildID)
	return helpers.EditInteractionEmbed(s, i, PartyCreatedEmbed(cfg.InactivityDisbandTime))
}

func partyInvite(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, PartyPlayerNotFoundEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, PartyBotTargetEmbed())
	}

	_, err := database.Must().Player.Get(guildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, PartyTargetNotRegisteredEmbed(target))
		}
		return err
	}

	party, auto, err := partymgr.Parties.Invite(guildID, userID, target.ID)
	if err != nil {
		return err
	}

	expires := time.Now().Add(time.Duration(partymgr.Parties.Config(guildID).InviteExpireSeconds) * time.Second)
	invite := PartyInvitationEmbed(target.ID, userID, len(party.Members), party.MaxSize, expires)
	sendErr := partymgr.SendPartyAlert(s, guildID, partymgr.PartyCmdName, partymgr.PartyAlertsKey, target.ID, invite, InviteButtons("party", party.Leader, target.ID))

	head := "Invite Sent"
	body := fmt.Sprintf("Invited <@%s> to your party.", target.ID)
	if auto {
		head, body = "Party Created & Invite Sent", "Created your party and "+LowerFirst(body)
	}
	if sendErr != nil {
		apputil.DiscordLogger.Error(fmt.Sprintf("[Party] could not post invite for %s in %s: %v", target.ID, guildID, sendErr))
		return helpers.EditInteractionEmbed(s, i, PartyInviteSentNoAlertEmbed(head, body))
	}
	return helpers.EditInteractionEmbed(s, i, PartyInviteSentEmbed(head, body))
}

func partyAccept(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	leader := helpers.SubOptionUser(s, options, "leader")
	if leader == nil {
		return helpers.EditInteractionEmbed(s, i, PartyLeaderNotFoundEmbed())
	}

	party, err := partymgr.Parties.Accept(guildID, leader.ID, userID)
	if err != nil {
		return err
	}
	partymgr.AnnouncePartyJoin(s, guildID, party, userID)
	return helpers.EditInteractionEmbed(s, i, partymgr.PartyJoinedEmbed(party.Leader, len(party.Members), party.MaxSize))
}

func partyDecline(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	leader := helpers.SubOptionUser(s, options, "leader")
	if leader == nil {
		return helpers.EditInteractionEmbed(s, i, PartyLeaderNotFoundEmbed())
	}

	if err := partymgr.Parties.Decline(guildID, leader.ID, userID); err != nil {
		return err
	}
	_ = partymgr.SendPartyAlert(s, guildID, partymgr.PartyCmdName, partymgr.PartyAlertsKey, leader.ID, partymgr.PartyInvitationDeclinedEmbed(userID), nil)
	return helpers.EditInteractionEmbed(s, i, partymgr.PartyDeclinedEmbed())
}

func partyLeave(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string) error {
	before := partymgr.Parties.PartyOf(guildID, userID)
	if err := partymgr.Parties.Leave(guildID, userID); err != nil {
		return err
	}
	if before != nil && before.Leader != userID && len(before.Members) > 2 {
		_ = partymgr.SendPartyAlert(s, guildID, partymgr.PartyCmdName, partymgr.PartyAlertsKey, before.Leader, PartyPlayerLeftEmbed(userID), nil)
	}
	return helpers.EditInteractionEmbed(s, i, PartyLeftEmbed())
}

func partyKick(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, PartyPlayerNotFoundEmbed())
	}

	if err := partymgr.Parties.Kick(guildID, userID, target.ID); err != nil {
		return err
	}
	return helpers.EditInteractionEmbed(s, i, PartyPlayerKickedEmbed(target.ID))
}

func partyPromote(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, PartyPlayerNotFoundEmbed())
	}

	if err := partymgr.Parties.Promote(guildID, userID, target.ID); err != nil {
		return err
	}
	_ = partymgr.SendPartyAlert(s, guildID, partymgr.PartyCmdName, partymgr.PartyAlertsKey, target.ID, PartyLeadershipTransferredEmbed(userID), nil)
	return helpers.EditInteractionEmbed(s, i, PartyLeadershipTransferredToLeaderEmbed(target.ID))
}

func partyDisband(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string) error {
	if err := partymgr.Parties.Disband(guildID, userID); err != nil {
		return err
	}
	return helpers.EditInteractionEmbed(s, i, PartyDisbandedEmbed())
}

func partyInvites(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	open := helpers.SubOptionBool(options, "open")
	if err := partymgr.Parties.SetInvitesOpen(guildID, userID, open); err != nil {
		return err
	}
	if open {
		return helpers.EditInteractionEmbed(s, i, PartyInvitesOpenedEmbed())
	}
	return helpers.EditInteractionEmbed(s, i, PartyInvitesClosedEmbed())
}

func partyInfo(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	target := helpers.SubOptionUser(s, options, "user")
	who, self := userID, true
	if target != nil {
		who, self = target.ID, target.ID == userID
	}
	party := partymgr.Parties.PartyOf(guildID, who)
	if party == nil {
		if self {
			return helpers.EditInteractionEmbed(s, i, PartyNotInPartyEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, PartyTargetNotInPartyEmbed(who))
	}

	invitesStatus := "Closed"
	if party.IsInvitesOpen {
		invitesStatus = "Open"
	}

	fields := []*discordgo.MessageEmbedField{
		{Name: "Leader", Value: fmt.Sprintf("<@%s>", party.Leader), Inline: true},
		{Name: "Size", Value: fmt.Sprintf("%d/%d", len(party.Members), party.MaxSize), Inline: true},
		{Name: "Invites", Value: invitesStatus, Inline: true},
		{Name: "Games Played", Value: fmt.Sprintf("%d", party.GamesPlayed), Inline: true},
		{Name: "Members", Value: MentionList(party.Members)},
	}
	if len(party.Invites) > 0 {
		ids := make([]string, 0, len(party.Invites))
		for id := range party.Invites {
			ids = append(ids, id)
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Pending Invites", Value: MentionList(ids)})
	}
	return helpers.EditInteractionEmbed(s, i, PartyInfoEmbed(who, fields))
}

// ============================================================================
// Helper functions
// ============================================================================

func UnexpectedErrorPartyEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Unexpected Error",
		Description: "An unexpected error occurred while processing your party request. Please try again later or contact support.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PartyInvalidSubcommandEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Subcommand",
		Description: "You must specify a valid subcommand. Select one from the command options.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PartyRegistrationRequiredEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Registration Required",
		Description: "You are not registered. Run `/register` to link a Minecraft account first.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PartyPlayerNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "You must select a player to invite. Use the user selection menu to choose a registered player.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PartyBotTargetEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Invalid Target",
		Description: "You cannot invite bot accounts to a party. Please select a registered player.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PartyTargetNotRegisteredEmbed(user *discordgo.User) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Registered",
		Description: fmt.Sprintf("<@%s> is not registered in this guild. Ask them to run `/register` to link their Minecraft account before inviting them to a party.", user.ID),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PartyCreatedEmbed(inactivity int) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Party Created",
		Description: fmt.Sprintf("Your party is ready! Invite players with `/party invite` to build your team.\n\n> **Leader:** You\n> **Members:** 1/4\n> **Invites:** Open\n> **Disbands After:** %d minutes of inactivity", inactivity),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyInviteSentEmbed(head, body string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       head,
		Description: body + "\n\nThe player can accept or decline the invitation from the party alerts channel.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyInviteSentNoAlertEmbed(head, body string) *discordgo.MessageEmbed {
	return helpers.Preset("caution", types.PresetOptions{
		Title:       head,
		Description: body + "\n\n> **Note:** I could not post the invitation to the party alerts channel. Ask the player to run `/party accept` with you as the leader to join manually.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyInvitationEmbed(targetID, leaderID string, members, maxSize int, expires time.Time) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title: "Party Invitation",
		Description: fmt.Sprintf(
			"<@%s>, <@%s> invited you to join their party!\n\n> **Party Size:** %d/%d\n> **Leader:** <@%s>\n> **Expires:** %s\n\nClick **Accept** to join or **Deny** to decline the invitation.",
			targetID, leaderID, members, maxSize, leaderID, helpers.DiscordRelativeTimestamp(expires),
		),
		Thumbnail: helpers.ThumbPlayers,
	})
}

func PartyLeaderNotFoundEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Player Not Found",
		Description: "Select the party leader who invited you.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PartyLeftEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Left Party",
		Description: "You have successfully left the party. You can create a new party with `/party create` or join another party by accepting an invitation.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyPlayerLeftEmbed(userID string) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Player Left",
		Description: fmt.Sprintf("<@%s> has left your party. You can invite a new player to fill the slot with `/party invite`.", userID),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyPlayerKickedEmbed(targetID string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Player Removed",
		Description: fmt.Sprintf("<@%s> has been removed from your party. You can invite a new player to fill the slot with `/party invite`.", targetID),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyLeadershipTransferredEmbed(userID string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "You Are the Party Leader",
		Description: fmt.Sprintf("<@%s> has transferred party leadership to you. You can now manage the party, invite members, kick players, and disband the party.", userID),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyLeadershipTransferredToLeaderEmbed(targetID string) *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Leadership Transferred",
		Description: fmt.Sprintf("You have successfully transferred party leadership to <@%s>. They are now responsible for managing the party.", targetID),
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyDisbandedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Party Disbanded",
		Description: "Your party has been successfully disbanded. All members have been notified.\n\nYou can create a new party at any time with `/party create`.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyInvitesOpenedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Invites Open",
		Description: "Party invites have been reopened. Players can now be invited to your party using `/party invite`.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyInvitesClosedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Invites Closed",
		Description: "Party invites have been paused. No new players can be invited until you reopen invites with `/party invites`.",
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func PartyNotInPartyEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not in a Party",
		Description: "You are not currently in a party. Create a new party with `/party create` or accept a pending invitation from another player.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PartyTargetNotInPartyEmbed(userID string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{
		Title:       "Not in a Party",
		Description: fmt.Sprintf("<@%s> is not currently in a party. They can create one with `/party create` or may have pending invitations to accept.", userID),
		Thumbnail:   helpers.ThumbFlag,
	})
}

func PartyInfoEmbed(who string, fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Party Information",
		Description: fmt.Sprintf("View the current party details for <@%s>.", who),
		Fields:      fields,
		Thumbnail:   helpers.ThumbPlayers,
	})
}

func InviteButtons(prefix, subjectID, targetID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "Accept", Style: discordgo.SuccessButton, CustomID: fmt.Sprintf("%s_accept_%s_%s", prefix, subjectID, targetID)},
		discordgo.Button{Label: "Deny", Style: discordgo.DangerButton, CustomID: fmt.Sprintf("%s_deny_%s_%s", prefix, subjectID, targetID)},
	}}}
}

func MentionList(ids []string) string {
	if len(ids) == 0 {
		return "—"
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = "<@" + id + ">"
	}
	return strings.Join(out, "\n")
}

func LowerFirst(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]|0x20) + s[1:]
}
