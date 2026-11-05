package general

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.Register(StaffOnlineCommand)
}

// StaffOnlineCommand (/staff) lists everyone who holds the server's staff role, sorted by whether they are online, idle, on do not
// disturb or offline, so a player knows whether anyone can help with a ticket, a screenshare or a dispute. The status needs the Presence
// intent, which the bot always requests; if it is ever not granted the command still lists the staff, without a status.
var StaffOnlineCommand = &types.Command{
	Name:          "staff",
	Description:   "Lists the server's staff members and who is online right now.",
	PrefixAliases: []string{"online", "staffonline"},
	Requirements:  types.StaffListRequirements,
	Cooldown:      10 * time.Second,
	Data:          &discordgo.ApplicationCommand{},
	Execute:       executeStaffOnlineCommand,
}

// staffGroups are the categories of the list, in the order it shows them.
var staffGroups = []struct {
	status discordgo.Status
	label  string
}{
	{discordgo.StatusOnline, "🟢 Online"},
	{discordgo.StatusIdle, "🌙 Idle"},
	{discordgo.StatusDoNotDisturb, "⛔ Do not disturb"},
	{discordgo.StatusOffline, "⚫ Offline"},
}

type staffMember struct {
	name    string
	mention string
}

func executeStaffOnlineCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// The staff role is the one assigned to this command (the same setting as the staff commands' own).
	roleID, err := helpers.RequirementSlotID(i.GuildID, "staff", types.ReqRole, types.StaffRoleKey)
	if err != nil || roleID == "" {
		return helpers.EditInteractionEmbed(s, i, helpers.Preset("error", types.PresetOptions{
			Title:       "Staff Role Not Set",
			Description: "This server has not chosen its staff role yet, so there is no staff list to show.",
			Thumbnail:   helpers.ThumbFlag,
		}))
	}
	guild, err := s.State.Guild(i.GuildID)
	if err != nil || guild == nil {
		return helpers.EditInteractionEmbed(s, i, helpers.Preset("error", types.PresetOptions{
			Title:       "Could Not Load Staff",
			Description: "The server's member list is not loaded yet. Try again in a moment.",
			Thumbnail:   helpers.ThumbFlag,
		}))
	}

	withStatus := s.Identify.Intents&discordgo.IntentsGuildPresences != 0
	groups := map[discordgo.Status][]staffMember{}
	var all []staffMember
	for _, member := range guild.Members {
		if member == nil || member.User == nil || member.User.Bot || !slices.Contains(member.Roles, roleID) {
			continue
		}
		who := staffMember{name: strings.ToLower(staffName(member)), mention: "<@" + member.User.ID + ">"}
		all = append(all, who)
		if !withStatus {
			continue
		}
		status := discordgo.StatusOffline // no presence known: shown as offline, as Discord does
		if p, err := s.State.Presence(i.GuildID, member.User.ID); err == nil && p != nil && p.Status != "" && p.Status != discordgo.StatusInvisible {
			status = p.Status
		}
		groups[status] = append(groups[status], who)
	}
	if len(all) == 0 {
		return helpers.EditInteractionEmbed(s, i, helpers.Preset("info", types.PresetOptions{
			Title:       "No Staff Found",
			Description: "Nobody in the member list holds the staff role.",
			Thumbnail:   helpers.ThumbFlag,
		}))
	}

	list := func(people []staffMember) string {
		slices.SortFunc(people, func(a, b staffMember) int { return strings.Compare(a.name, b.name) })
		const limit = 25
		mentions := make([]string, 0, limit)
		for _, p := range people[:min(len(people), limit)] {
			mentions = append(mentions, p.mention)
		}
		text := strings.Join(mentions, " ")
		if len(people) > limit {
			text += fmt.Sprintf(" and %d more", len(people)-limit)
		}
		return text
	}

	var fields []*discordgo.MessageEmbedField
	description := fmt.Sprintf("**%d** staff members.", len(all))
	if withStatus {
		for _, g := range staffGroups {
			if people := groups[g.status]; len(people) > 0 {
				fields = append(fields, &discordgo.MessageEmbedField{Name: fmt.Sprintf("%s (%d)", g.label, len(people)), Value: list(people)})
			}
		}
		active := len(all) - len(groups[discordgo.StatusOffline])
		description = fmt.Sprintf("**%d** of **%d** staff members are online.", active, len(all))
		if active == 0 {
			description = fmt.Sprintf("No staff members are online right now (%d in total). Open a ticket and someone will answer when they are back.", len(all))
		}
	} else {
		fields = append(fields, &discordgo.MessageEmbedField{Name: fmt.Sprintf("Staff (%d)", len(all)), Value: list(all)})
		description += " Online status is not available on this bot yet."
	}
	return helpers.EditInteractionEmbed(s, i, helpers.Preset("success", types.PresetOptions{
		Title:       "Staff",
		Description: description,
		Fields:      fields,
		Thumbnail:   helpers.ThumbPlayers,
	}))
}

// staffName is how a member is named for sorting: server nickname, else display name, else username.
func staffName(member *discordgo.Member) string {
	for _, name := range []string{member.Nick, member.User.GlobalName, member.User.Username} {
		if name != "" {
			return name
		}
	}
	return member.User.ID
}
