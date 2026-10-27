package general

import (
	"fmt"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
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
	Name:        "party",
	Description: "Manages party matching, custom party groups, and team formations.",
	Ephemeral:   true,
	Cooldown:    3 * time.Second,
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
	helpers.BotSession.Store(s)
	subcommand, options := types.SelectedSubcommand(i)
	if subcommand == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.PartyInvalidSubcommandEmbed())
	}

	userID := helpers.InteractionUserID(i)
	guildID := i.GuildID

	// Check registration
	_, err := database.Must().Player.Get(guildID, userID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PartyRegistrationRequiredEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPartyEmbed())
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
		return helpers.EditInteractionEmbed(s, i, helpers.PartyInvalidSubcommandEmbed())
	}

	if result != nil {
		if e := helpers.PartyErrorEmbed(result); e != nil {
			return helpers.EditInteractionEmbed(s, i, e)
		}
		return helpers.EditInteractionEmbed(s, i, helpers.UnexpectedErrorPartyEmbed())
	}

	return nil
}

// ============================================================================
// Subcommands
// ============================================================================

func partyCreate(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string) error {
	if _, err := helpers.Parties.Create(guildID, userID); err != nil {
		return err
	}
	cfg := helpers.Parties.Config(guildID)
	return helpers.EditInteractionEmbed(s, i, helpers.PartyCreatedEmbed(cfg.InactivityDisbandTime))
}

func partyInvite(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PartyPlayerNotFoundEmbed())
	}
	if target.Bot {
		return helpers.EditInteractionEmbed(s, i, helpers.PartyBotTargetEmbed())
	}

	_, err := database.Must().Player.Get(guildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return helpers.EditInteractionEmbed(s, i, helpers.PartyTargetNotRegisteredEmbed(target))
		}
		return err
	}

	party, auto, err := helpers.Parties.Invite(guildID, userID, target.ID)
	if err != nil {
		return err
	}

	expires := time.Now().Add(time.Duration(helpers.Parties.Config(guildID).InviteExpireSeconds) * time.Second)
	invite := helpers.PartyInvitationEmbed(target.ID, userID, len(party.Members), party.MaxSize, expires)
	sendErr := helpers.SendPartyAlert(s, guildID, helpers.PartyCmdName, helpers.PartyAlertsKey, target.ID, invite, helpers.InviteButtons("party", party.Leader, target.ID))

	head := "Invite Sent"
	body := fmt.Sprintf("Invited <@%s> to your party.", target.ID)
	if auto {
		head, body = "Party Created & Invite Sent", "Created your party and "+helpers.LowerFirst(body)
	}
	if sendErr != nil {
		apputil.DiscordLogger.Error(fmt.Sprintf("[Party] could not post invite for %s in %s: %v", target.ID, guildID, sendErr))
		return helpers.EditInteractionEmbed(s, i, helpers.PartyInviteSentNoAlertEmbed(head, body))
	}
	return helpers.EditInteractionEmbed(s, i, helpers.PartyInviteSentEmbed(head, body))
}

func partyAccept(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	leader := helpers.SubOptionUser(s, options, "leader")
	if leader == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PartyLeaderNotFoundEmbed())
	}

	party, err := helpers.Parties.Accept(guildID, leader.ID, userID)
	if err != nil {
		return err
	}
	helpers.AnnouncePartyJoin(s, guildID, party, userID)
	return helpers.EditInteractionEmbed(s, i, helpers.PartyJoinedEmbed(party.Leader, len(party.Members), party.MaxSize))
}

func partyDecline(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	leader := helpers.SubOptionUser(s, options, "leader")
	if leader == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PartyLeaderNotFoundEmbed())
	}

	if err := helpers.Parties.Decline(guildID, leader.ID, userID); err != nil {
		return err
	}
	_ = helpers.SendPartyAlert(s, guildID, helpers.PartyCmdName, helpers.PartyAlertsKey, leader.ID, helpers.PartyInvitationDeclinedEmbed(userID), nil)
	return helpers.EditInteractionEmbed(s, i, helpers.PartyDeclinedEmbed())
}

func partyLeave(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string) error {
	before := helpers.Parties.PartyOf(guildID, userID)
	if err := helpers.Parties.Leave(guildID, userID); err != nil {
		return err
	}
	if before != nil && before.Leader != userID && len(before.Members) > 2 {
		_ = helpers.SendPartyAlert(s, guildID, helpers.PartyCmdName, helpers.PartyAlertsKey, before.Leader, helpers.PartyPlayerLeftEmbed(userID), nil)
	}
	return helpers.EditInteractionEmbed(s, i, helpers.PartyLeftEmbed())
}

func partyKick(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PartyPlayerNotFoundEmbed())
	}

	if err := helpers.Parties.Kick(guildID, userID, target.ID); err != nil {
		return err
	}
	return helpers.EditInteractionEmbed(s, i, helpers.PartyPlayerKickedEmbed(target.ID))
}

func partyPromote(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	target := helpers.SubOptionUser(s, options, "user")
	if target == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.PartyPlayerNotFoundEmbed())
	}

	if err := helpers.Parties.Promote(guildID, userID, target.ID); err != nil {
		return err
	}
	_ = helpers.SendPartyAlert(s, guildID, helpers.PartyCmdName, helpers.PartyAlertsKey, target.ID, helpers.PartyLeadershipTransferredEmbed(userID), nil)
	return helpers.EditInteractionEmbed(s, i, helpers.PartyLeadershipTransferredToLeaderEmbed(target.ID))
}

func partyDisband(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string) error {
	if err := helpers.Parties.Disband(guildID, userID); err != nil {
		return err
	}
	return helpers.EditInteractionEmbed(s, i, helpers.PartyDisbandedEmbed())
}

func partyInvites(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	open := helpers.SubOptionBool(options, "open")
	if err := helpers.Parties.SetInvitesOpen(guildID, userID, open); err != nil {
		return err
	}
	if open {
		return helpers.EditInteractionEmbed(s, i, helpers.PartyInvitesOpenedEmbed())
	}
	return helpers.EditInteractionEmbed(s, i, helpers.PartyInvitesClosedEmbed())
}

func partyInfo(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, userID string, options []*discordgo.ApplicationCommandInteractionDataOption) error {
	target := helpers.SubOptionUser(s, options, "user")
	who, self := userID, true
	if target != nil {
		who, self = target.ID, target.ID == userID
	}
	party := helpers.Parties.PartyOf(guildID, who)
	if party == nil {
		if self {
			return helpers.EditInteractionEmbed(s, i, helpers.PartyNotInPartyEmbed())
		}
		return helpers.EditInteractionEmbed(s, i, helpers.PartyTargetNotInPartyEmbed(who))
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
		{Name: "Members", Value: helpers.MentionList(party.Members)},
	}
	if len(party.Invites) > 0 {
		ids := make([]string, 0, len(party.Invites))
		for id := range party.Invites {
			ids = append(ids, id)
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Pending Invites", Value: helpers.MentionList(ids)})
	}
	return helpers.EditInteractionEmbed(s, i, helpers.PartyInfoEmbed(who, fields))
}
