package general

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/factions"
	"ranked-bedwars/src/types"
)

func init() {
	helpers.Register(PlayerFactionCommand)
}

// PlayerFactionCommand is the server's factions: list them, join one, see how they stand. Staff can move people. The factions
// themselves (a name, an emoji and a role each) are set up on the dashboard under Community → Factions.
var PlayerFactionCommand = &types.Command{
	Name:          "faction",
	Description:   "Join a faction and see how the factions stand this season.",
	PrefixAliases: []string{"factions"},
	Requirements:  types.StaffListRequirements, // the staff role, for /faction set (members do not need it)
	Ephemeral:     false,
	Cooldown:      3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "list", Description: "List the factions and who is in them", Type: discordgo.ApplicationCommandOptionSubCommand},
			{
				Name: "join", Description: "Join a faction", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{{Name: "faction", Description: "The faction to join", Type: discordgo.ApplicationCommandOptionString, Required: true, Autocomplete: true}},
			},
			{Name: "leave", Description: "Leave your faction", Type: discordgo.ApplicationCommandOptionSubCommand},
			{
				Name: "info", Description: "How one faction stands (yours by default)", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{{Name: "faction", Description: "The faction to look at", Type: discordgo.ApplicationCommandOptionString, Autocomplete: true}},
			},
			{Name: "standings", Description: "The factions ranked by their points this season", Type: discordgo.ApplicationCommandOptionSubCommand},
			{
				Name: "set", Description: "Staff: put a member in a faction, or take them out of theirs", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "The member", Type: discordgo.ApplicationCommandOptionUser, Required: true},
					{Name: "faction", Description: "The faction, or none to take them out", Type: discordgo.ApplicationCommandOptionString, Required: true, Autocomplete: true},
				},
			},
		},
	},
	Autocomplete: factionAutocomplete,
	Execute:      executeFactionCommand,
}

func factionAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	sub, opts := types.SelectedSubcommand(i)
	typed := ""
	for _, o := range opts {
		if o.Focused {
			typed = strings.ToLower(o.StringValue())
		}
	}
	var choices []*discordgo.ApplicationCommandOptionChoice
	if _, list, err := factions.Load(i.GuildID); err == nil {
		if sub == "set" && strings.Contains("none", typed) {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: "None (take them out of their faction)", Value: "none"})
		}
		for _, f := range list {
			if typed == "" || strings.Contains(strings.ToLower(f.Name), typed) {
				choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: truncateRunes(f.Label(), 100), Value: f.Name})
			}
			if len(choices) == 25 {
				break
			}
		}
	}
	_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func executeFactionCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	settings, list, err := factions.Load(i.GuildID)
	if err != nil {
		return factionReply(s, i, "error", "Unexpected Error", "The factions could not be loaded. Please try again in a moment.", nil)
	}
	if !settings.Enabled || len(list) == 0 {
		return factionReply(s, i, "info", "No Factions", "This server has no factions yet. Staff can set them up on the dashboard under Community → Factions.", nil)
	}
	sub, opts := types.SelectedSubcommand(i)
	arg := func(name string) string {
		for _, o := range opts {
			if o.Name == name {
				return strings.TrimSpace(o.StringValue())
			}
		}
		return ""
	}
	switch sub {
	case "list":
		return factionList(s, i, list)
	case "join":
		return factionJoin(s, i, settings, list, arg("faction"))
	case "leave":
		return factionLeave(s, i, settings, list)
	case "info":
		return factionInfo(s, i, settings, list, arg("faction"))
	case "standings":
		return factionStandings(s, i, settings, list)
	case "set":
		return factionSet(s, i, list, arg("faction"))
	}
	return factionReply(s, i, "error", "Invalid Subcommand", "Use `/faction list`, `join`, `leave`, `info`, `standings` or `set`.", nil)
}

func factionReply(s *discordgo.Session, i *discordgo.InteractionCreate, kind, title, text string, f *factions.Faction) error {
	emb := helpers.Preset(kind, types.PresetOptions{Title: title, Description: text, Thumbnail: helpers.ThumbPlayers})
	if f != nil {
		emb.Color = f.ColorInt()
	}
	return helpers.EditInteractionEmbed(s, i, emb)
}

// holders lists the Discord ids of the members holding a role, from the member list the bot keeps.
func holders(s *discordgo.Session, guildID string) func(roleID string) []string {
	return func(roleID string) []string {
		g, err := s.State.Guild(guildID)
		if err != nil || g == nil {
			return nil
		}
		var ids []string
		for _, m := range g.Members {
			if m != nil && m.User != nil && !m.User.Bot && slices.Contains(m.Roles, roleID) {
				ids = append(ids, m.User.ID)
			}
		}
		return ids
	}
}

func factionList(s *discordgo.Session, i *discordgo.InteractionCreate, list []factions.Faction) error {
	hold := holders(s, i.GuildID)
	var lines []string
	for _, f := range list {
		line := fmt.Sprintf("**%s** · %d members", f.Label(), len(hold(f.RoleID)))
		if d := strings.TrimSpace(f.Description); d != "" {
			line += "\n" + d
		}
		lines = append(lines, line)
	}
	return factionReply(s, i, "info", "Factions", strings.Join(lines, "\n\n")+"\n\nJoin one with `/faction join`.", nil)
}

func factionJoin(s *discordgo.Session, i *discordgo.InteractionCreate, st factions.Settings, list []factions.Faction, name string) error {
	if i.Member == nil || i.Member.User == nil {
		return nil
	}
	f := factions.Find(list, name)
	if f == nil {
		return factionReply(s, i, "error", "Faction Not Found", "No faction has that name. See `/faction list`.", nil)
	}
	if cur := factions.Of(i.Member.Roles, list); cur != nil {
		if cur.ID == f.ID {
			return factionReply(s, i, "info", "Already In", fmt.Sprintf("You are already in **%s**.", cur.Label()), cur)
		}
		return factionReply(s, i, "error", "Already In A Faction", fmt.Sprintf("You are in **%s**. Leave it first with `/faction leave`, or ask staff to move you.", cur.Label()), cur)
	}
	if st.RequireRegistered {
		if _, err := database.Must().Player.Get(i.GuildID, i.Member.User.ID); err != nil {
			return factionReply(s, i, "error", "Registration Needed", "Only registered players can join a faction. Register with `/register` first.", nil)
		}
	}
	if err := s.GuildMemberRoleAdd(i.GuildID, i.Member.User.ID, f.RoleID); err != nil {
		return factionReply(s, i, "error", "Could Not Join", "I could not give you the faction's role. Ask an admin to check my permissions and role order.", nil)
	}
	return factionReply(s, i, "success", "Joined "+f.Name, fmt.Sprintf("Welcome to **%s**, <@%s>! Your wins and games this season count towards its points.", f.Label(), i.Member.User.ID), f)
}

func factionLeave(s *discordgo.Session, i *discordgo.InteractionCreate, st factions.Settings, list []factions.Faction) error {
	if i.Member == nil || i.Member.User == nil {
		return nil
	}
	cur := factions.Of(i.Member.Roles, list)
	if cur == nil {
		return factionReply(s, i, "info", "Not In A Faction", "You are not in a faction.", nil)
	}
	if !st.AllowLeave {
		return factionReply(s, i, "error", "Leaving Is Closed", "This server does not let members leave a faction themselves. Ask staff to move you.", cur)
	}
	if err := s.GuildMemberRoleRemove(i.GuildID, i.Member.User.ID, cur.RoleID); err != nil {
		return factionReply(s, i, "error", "Could Not Leave", "I could not take the faction's role from you. Ask an admin to check my permissions and role order.", nil)
	}
	return factionReply(s, i, "success", "Left "+cur.Name, fmt.Sprintf("You left **%s**.", cur.Label()), cur)
}

func standingsOf(s *discordgo.Session, i *discordgo.InteractionCreate, st factions.Settings, list []factions.Faction) ([]factions.Standing, error) {
	return factions.Standings(i.GuildID, list, st, holders(s, i.GuildID))
}

func standingLine(st factions.Standing, withPosition bool) string {
	line := fmt.Sprintf("**%s** · %d points · %d members", st.Faction.Label(), st.Points, st.Members)
	if withPosition {
		line = fmt.Sprintf("**#%d** ", st.Position) + line
	}
	return line
}

func factionStandings(s *discordgo.Session, i *discordgo.InteractionCreate, st factions.Settings, list []factions.Faction) error {
	standings, err := standingsOf(s, i, st, list)
	if err != nil {
		return factionReply(s, i, "error", "Unexpected Error", "The standings could not be worked out right now. Please try again in a moment.", nil)
	}
	var lines []string
	for _, row := range standings {
		lines = append(lines, standingLine(row, true)+fmt.Sprintf("\n%d wins · %d games · avg ELO %d", row.Wins, row.Games, row.AvgElo))
	}
	note := fmt.Sprintf("\n\nPoints: %d per win, %d per game played this season. Updated every few minutes.", st.WinPoints, st.GamePoints)
	return factionReply(s, i, "success", "Faction Standings", strings.Join(lines, "\n\n")+note, &standings[0].Faction)
}

func factionInfo(s *discordgo.Session, i *discordgo.InteractionCreate, st factions.Settings, list []factions.Faction, name string) error {
	var f *factions.Faction
	if name != "" {
		f = factions.Find(list, name)
		if f == nil {
			return factionReply(s, i, "error", "Faction Not Found", "No faction has that name. See `/faction list`.", nil)
		}
	} else if i.Member != nil {
		f = factions.Of(i.Member.Roles, list)
	}
	if f == nil {
		return factionReply(s, i, "info", "No Faction", "You are not in a faction. Join one with `/faction join`, or name one to look at.", nil)
	}
	standings, err := standingsOf(s, i, st, list)
	if err != nil {
		return factionReply(s, i, "error", "Unexpected Error", "The standings could not be worked out right now. Please try again in a moment.", nil)
	}
	for _, row := range standings {
		if row.Faction.ID != f.ID {
			continue
		}
		text := strings.TrimSpace(f.Description)
		if text != "" {
			text += "\n\n"
		}
		text += fmt.Sprintf("**Place:** #%d of %d\n**Points:** %d\n**Members:** %d (%d have played this season)\n**Wins:** %d · **Games:** %d\n**Average ELO:** %d\n**Role:** <@&%s>",
			row.Position, len(standings), row.Points, row.Members, row.Active, row.Wins, row.Games, row.AvgElo, f.RoleID)
		return factionReply(s, i, "info", f.Label(), text, f)
	}
	return factionReply(s, i, "error", "Faction Not Found", "No faction has that name.", nil)
}

// factionSet is staff moving a member: out of any faction, then into the named one (or none).
func factionSet(s *discordgo.Session, i *discordgo.InteractionCreate, list []factions.Faction, name string) error {
	staffRole, _ := helpers.RequirementSlotID(i.GuildID, "faction", types.ReqRole, types.StaffRoleKey)
	if !helpers.MemberIsAdministrator(s, i) && (staffRole == "" || !helpers.MemberHasRole(i.Member, staffRole)) {
		return factionReply(s, i, "error", "Permission Denied", "Only staff can move members between factions.", nil)
	}
	target := helpers.SubOptionUser(s, i.ApplicationCommandData().Options, "user")
	if target == nil || target.Bot {
		return factionReply(s, i, "error", "Invalid Member", "Choose a member to move.", nil)
	}
	var dest *factions.Faction
	if !strings.EqualFold(name, "none") {
		if dest = factions.Find(list, name); dest == nil {
			return factionReply(s, i, "error", "Faction Not Found", "No faction has that name. Use `none` to take a member out of theirs.", nil)
		}
	}
	member, err := s.GuildMember(i.GuildID, target.ID)
	if err != nil {
		return factionReply(s, i, "error", "Member Not Found", "That user is not in this server.", nil)
	}
	for _, f := range list {
		if slices.Contains(member.Roles, f.RoleID) && (dest == nil || f.ID != dest.ID) {
			if err := s.GuildMemberRoleRemove(i.GuildID, target.ID, f.RoleID); err != nil {
				return factionReply(s, i, "error", "Could Not Move", "I could not take the old faction's role. Check my permissions and role order.", nil)
			}
		}
	}
	if dest == nil {
		return factionReply(s, i, "success", "Faction Removed", fmt.Sprintf("<@%s> is no longer in a faction.", target.ID), nil)
	}
	if err := s.GuildMemberRoleAdd(i.GuildID, target.ID, dest.RoleID); err != nil {
		return factionReply(s, i, "error", "Could Not Move", "I could not give the faction's role. Check my permissions and role order.", nil)
	}
	return factionReply(s, i, "success", "Moved to "+dest.Name, fmt.Sprintf("<@%s> is now in **%s**.", target.ID, dest.Label()), dest)
}
