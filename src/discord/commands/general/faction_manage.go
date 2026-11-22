package general

import (
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/factionmgr"
)

// ============================================================================
// Globals & Initialization
// ============================================================================

func init() {
	helpers.RegisterComponentHandler(handleFactionInvite)
}

var factionInviteButton = regexp.MustCompile(`^faction_(accept|deny)_(\d+)_(\d+)$`)

// ============================================================================
// Joining and leaving
// ============================================================================

func factionCreate(c *factionCtx) error {
	days := 0
	if m := c.i.Member; m != nil && !m.JoinedAt.IsZero() {
		days = int(time.Since(m.JoinedAt).Hours() / 24)
	}
	p, err := database.FactionCreate(c.guildID, c.userID, c.str("name"), c.str("tag"), days)
	if err != nil {
		return err
	}
	go factionmgr.RefreshMember(c.guildID, c.userID)
	return c.send(FactionCreatedEmbed(p))
}

func factionInvites(c *factionCtx) error {
	list, err := database.FactionInvites(c.guildID, c.userID)
	if err != nil {
		return err
	}
	return c.send(FactionInvitesEmbed(list))
}

// factionAnswer accepts or declines an invitation: the faction named, or the only one waiting when none is.
func factionAnswer(accept bool) func(*factionCtx) error {
	return func(c *factionCtx) error {
		var factionID uint
		if name := c.str("faction"); strings.TrimSpace(name) != "" {
			p, err := c.named(name)
			if err != nil {
				return err
			}
			factionID = p.Faction.ID
		}
		p, err := database.FactionAnswer(c.guildID, c.userID, factionID, accept)
		if err != nil {
			return err
		}
		if !accept {
			return c.send(FactionDeclinedEmbed())
		}
		go factionmgr.SyncFaction(c.guildID, p)
		return c.send(FactionJoinedEmbed(p))
	}
}

func factionLeave(c *factionCtx) error {
	f, err := database.FactionLeave(c.guildID, c.userID)
	if err != nil {
		return err
	}
	go factionmgr.RemoveFrom(c.guildID, c.userID, f.RoleID)
	return c.send(FactionLeftEmbed(f.Name))
}

// ============================================================================
// Inviting
// ============================================================================

func factionInvite(c *factionCtx) error {
	target, err := c.target()
	if err != nil {
		return err
	}
	mine, err := c.mine()
	if err != nil {
		return err
	}
	if _, err := database.Must().Player.Get(c.guildID, target.ID); err != nil {
		if sdk.IsNotFound(err) {
			return FactionReply(FactionTargetNotRegisteredEmbed(target.ID))
		}
		return err
	}
	expires, err := database.FactionInvitePlayer(c.guildID, mine.Faction.ID, c.userID, target.ID)
	if err != nil {
		return err
	}
	msg := &discordgo.MessageSend{
		Content:         "<@" + target.ID + ">",
		Embeds:          []*discordgo.MessageEmbed{FactionInvitationEmbed(mine, c.userID, expires)},
		Components:      InviteButtons("faction", strconv.FormatUint(uint64(mine.Faction.ID), 10), target.ID),
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{target.ID}},
	}
	if _, sendErr := helpers.SendChannelMessage(c.s, c.guildID, c.i.ChannelID, msg); sendErr != nil {
		log.Printf("[Faction] could not post the invitation for %s in %s: %v", target.ID, c.guildID, sendErr)
		return c.send(FactionInviteSentNoPostEmbed(target.ID, mine.Faction.Name))
	}
	return c.send(FactionInviteSentEmbed(target.ID, mine.Faction.Name))
}

// handleFactionInvite answers an invitation when its button is pressed. Only the invited player can.
func handleFactionInvite(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	m := factionInviteButton.FindStringSubmatch(customID)
	if m == nil {
		return false
	}
	accept := m[1] == "accept"
	factionID, _ := strconv.ParseUint(m[2], 10, 64)
	userID := helpers.InteractionUserID(i)
	if userID != m[3] {
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Embeds: []*discordgo.MessageEmbed{FactionNotYourInvitationEmbed()}},
		})
		return true
	}
	p, err := database.FactionAnswer(i.GuildID, userID, uint(factionID), accept)
	var embed *discordgo.MessageEmbed
	switch {
	case err != nil && database.FactionMessage(err) != "":
		embed = FactionRefusedEmbed(database.FactionMessage(err))
	case err != nil:
		log.Printf("[Faction] invitation button in %s: %v", i.GuildID, err)
		embed = UnexpectedErrorFactionEmbed()
	case !accept:
		embed = FactionDeclinedEmbed()
	default:
		embed = FactionJoinedEmbed(p)
		go factionmgr.SyncFaction(i.GuildID, p)
	}
	// The answer replaces the invitation, so its buttons are gone.
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Components: []discordgo.MessageComponent{}},
	})
	return true
}

// ============================================================================
// Running the faction
// ============================================================================

func factionKick(c *factionCtx) error {
	target, err := c.target()
	if err != nil {
		return err
	}
	mine, err := c.mine()
	if err != nil {
		return err
	}
	if err := database.FactionKick(c.guildID, mine.Faction.ID, c.userID, target.ID); err != nil {
		return err
	}
	go factionmgr.RemoveFrom(c.guildID, target.ID, mine.Faction.RoleID)
	return c.send(FactionKickedEmbed(target.ID, mine.Faction.Name))
}

// factionRole promotes a member to Officer, or demotes an Officer to Member.
func factionRole(promote bool) func(*factionCtx) error {
	return func(c *factionCtx) error {
		target, err := c.target()
		if err != nil {
			return err
		}
		mine, err := c.mine()
		if err != nil {
			return err
		}
		role := "member"
		if promote {
			role = "officer"
		}
		if err := database.FactionSetRole(c.guildID, mine.Faction.ID, c.userID, target.ID, role); err != nil {
			return err
		}
		return c.send(FactionRoleChangedEmbed(target.ID, mine.Faction.Name, promote))
	}
}

func factionTransfer(c *factionCtx) error {
	target, err := c.target()
	if err != nil {
		return err
	}
	mine, err := c.mine()
	if err != nil {
		return err
	}
	if err := database.FactionTransfer(c.guildID, mine.Faction.ID, c.userID, target.ID); err != nil {
		return err
	}
	return c.send(FactionTransferredEmbed(target.ID, mine.Faction.Name))
}

func factionCustomize(c *factionCtx) error {
	mine, err := c.mine()
	if err != nil {
		return err
	}
	fields := map[string]any{}
	var changed []string
	for _, name := range []string{"name", "tag", "color", "icon", "symbol"} {
		if v := c.str(name); strings.TrimSpace(v) != "" {
			fields[name] = v
			changed = append(changed, name)
		}
	}
	if len(fields) == 0 {
		return FactionReply(FactionNothingToChangeEmbed())
	}
	p, err := database.FactionCustomize(c.guildID, mine.Faction.ID, c.userID, false, fields)
	if err != nil {
		return err
	}
	go factionmgr.SyncFaction(c.guildID, p)
	return c.send(FactionCustomizedEmbed(p, changed))
}

func factionDisband(c *factionCtx) error {
	mine, err := c.mine()
	if err != nil {
		return err
	}
	if !helpers.SubOptionBool(c.options, "confirm") {
		return FactionReply(FactionDisbandUnconfirmedEmbed(mine.Faction.Name))
	}
	out, err := database.FactionDisband(c.guildID, mine.Faction.ID, c.userID, false)
	if err != nil {
		return err
	}
	go func() {
		factionmgr.DeleteRole(c.guildID, out.Faction.RoleID)
		for _, id := range out.Members {
			factionmgr.RemoveFrom(c.guildID, id, "")
		}
	}()
	return c.send(FactionDisbandedEmbed(out.Faction.Name))
}
