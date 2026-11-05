// Package division holds the PUGS, PUPS and Premium divisions: a member role, a spec role and a manager role each, the commands that
// manage them (add, remove, list, vote), the managers overview and the alerts they post.
//
// The roles and channels are ordinary requirement slots of the division's own command ("pugs", "pups", "premium"), so the dashboard,
// /config and the division commands all read and write the same assignments.
package division

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"
)

// ============================================================================
// Divisions & settings
// ============================================================================

// Div is one division. Key is its command name and the prefix of its requirement slots; Vote says whether managers can
// start a vote for a player (Premium has none).
type Div struct {
	Key   string
	Label string
	Vote  bool
}

var (
	Premium = Div{"premium", "Premium", false}
	Pugs    = Div{"pugs", "PUGS", true}
	Pups    = Div{"pups", "PUPS", true}
	Pit     = Div{"pit", "Pit", true}
	All     = []Div{Premium, Pugs, Pups, Pit}
)

// Setting is one role or channel a division is configured with.
type Setting struct {
	Key    string // the value set-role / set-channel take
	Name   string
	Type   types.RequirementType
	suffix string // appended to the division key to make the requirement slot
	Vote   bool   // only divisions that vote have it
}

var (
	SetMember  = Setting{"member_role", "Member role", types.ReqRole, "_role", false}
	SetSpec    = Setting{"spec_role", "Spec role", types.ReqRole, "_spec_role", false}
	SetManager = Setting{"manager_role", "Manager role", types.ReqRole, "_manager_role", false}
	SetVotes   = Setting{"vote_channel", "Vote alerts channel", types.ReqChannel, "_vote_alerts", true}
	SetLogs    = Setting{"vote_log_channel", "Vote log channel", types.ReqChannel, "_vote_logs", true}
	SetRoles   = Setting{"role_alert_channel", "Promotion & demotion alerts channel", types.ReqChannel, "_role_alerts", false}

	Settings = []Setting{SetMember, SetSpec, SetManager, SetVotes, SetLogs, SetRoles}
)

// Find returns the division with this key.
func Find(key string) (Div, bool) {
	for _, d := range All {
		if d.Key == key {
			return d, true
		}
	}
	return Div{}, false
}

// FindSetting returns the setting with this key.
func FindSetting(key string) (Setting, bool) {
	for _, st := range Settings {
		if st.Key == key {
			return st, true
		}
	}
	return Setting{}, false
}

// Has reports whether the division uses the setting.
func (d Div) Has(st Setting) bool { return d.Vote || !st.Vote }

// Slot is the requirement key of a setting for this division (pugs_spec_role).
func (d Div) Slot(st Setting) string { return d.Key + st.suffix }

// Requirements declares every slot of the division. None blocks a command or is held by whoever runs it: they only say where
// the roles and channels are.
func (d Div) Requirements() []types.Requirement {
	var reqs []types.Requirement
	for _, st := range Settings {
		if d.Has(st) {
			reqs = append(reqs, types.Requirement{Type: st.Type, Key: d.Slot(st), Optional: true, Grants: st.Type == types.ReqRole, Label: d.Label + " " + strings.ToLower(st.Name)})
		}
	}
	return reqs
}

// Config is what a guild assigned to a division ("" = not set).
type Config struct {
	Member, Spec, Manager                 string // roles
	VoteChannel, LogChannel, AlertChannel string
}

// Load reads the division's assignments.
func Load(guildID string, d Div) (Config, error) {
	roles, _, err := helpers.CommandRequirementSlots(guildID, d.Key, types.ReqRole)
	if err != nil {
		return Config{}, err
	}
	channels, _, err := helpers.CommandRequirementSlots(guildID, d.Key, types.ReqChannel)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Member: roles[d.Slot(SetMember)].ID, Spec: roles[d.Slot(SetSpec)].ID, Manager: roles[d.Slot(SetManager)].ID,
		VoteChannel: channels[d.Slot(SetVotes)].ID, LogChannel: channels[d.Slot(SetLogs)].ID, AlertChannel: channels[d.Slot(SetRoles)].ID,
	}, nil
}

// Value is the assignment of a setting.
func (c Config) Value(st Setting) string {
	switch st.Key {
	case SetMember.Key:
		return c.Member
	case SetSpec.Key:
		return c.Spec
	case SetManager.Key:
		return c.Manager
	case SetVotes.Key:
		return c.VoteChannel
	case SetLogs.Key:
		return c.LogChannel
	}
	return c.AlertChannel
}

// IsManager says whether whoever ran the interaction may manage the division: an administrator, or a holder of its manager role.
func IsManager(s *discordgo.Session, i *discordgo.InteractionCreate, cfg Config) bool {
	return helpers.MemberIsAdministrator(s, i) || helpers.MemberHasRole(i.Member, cfg.Manager)
}

// ============================================================================
// Members
// ============================================================================

// guildMembers lists every member of the guild.
func guildMembers(s *discordgo.Session, guildID string) ([]*discordgo.Member, error) {
	// The bot loads every member into its gateway cache when a server becomes available and the member events keep it current, so
	// this answers instantly. Only a cache that is still filling up (or was never filled) falls back to paging through Discord.
	if g, err := s.State.Guild(guildID); err == nil {
		s.State.RLock() // State.Guild locks on its own, so this is taken after it returns, never inside it
		complete := g.MemberCount > 0 && len(g.Members) >= g.MemberCount
		cached := append([]*discordgo.Member(nil), g.Members...)
		s.State.RUnlock()
		if complete {
			return cached, nil
		}
	}

	var all []*discordgo.Member
	after := ""
	for {
		page, err := s.GuildMembers(guildID, after, 1000)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 1000 {
			return all, nil
		}
		after = page[len(page)-1].User.ID
	}
}

// holders lists the members (not bots) that hold the role.
func holders(members []*discordgo.Member, roleID string) []*discordgo.Member {
	var out []*discordgo.Member
	for _, m := range members {
		if m.User != nil && !m.User.Bot && helpers.MemberHasRole(m, roleID) {
			out = append(out, m)
		}
	}
	return out
}

// nameOf is how the server shows the member: their nickname, else their display name.
func nameOf(m *discordgo.Member) string {
	switch {
	case m.Nick != "":
		return m.Nick
	case m.User.GlobalName != "":
		return m.User.GlobalName
	}
	return m.User.Username
}

// mentions joins names (not <@id> mentions: Discord shows a mention as a raw ID for any user the viewer's client has not loaded,
// which is most of a long list) to fit limit characters, saying how many did not fit.
func mentions(ids []string, limit int) string {
	if len(ids) == 0 {
		return "*None*"
	}
	var b strings.Builder
	for n, id := range ids {
		m := id
		if b.Len()+len(m)+20 > limit {
			fmt.Fprintf(&b, " … +%d more", len(ids)-n)
			break
		}
		if n > 0 {
			b.WriteString(", ")
		}
		b.WriteString(m)
	}
	return b.String()
}

func roleMention(id string) string {
	if id == "" {
		return "`Not configured`"
	}
	return "<@&" + id + ">"
}

// ============================================================================
// Embeds
// ============================================================================

func errorEmbed(title, text string) *discordgo.MessageEmbed {
	return helpers.Preset("error", types.PresetOptions{Title: title, Description: text, Thumbnail: helpers.ThumbFlag})
}

func unexpectedEmbed() *discordgo.MessageEmbed {
	return errorEmbed("Unexpected Error", "Something went wrong while handling this division request. Please try again later or contact an administrator.")
}

func notConfiguredEmbed(d Div, st Setting) *discordgo.MessageEmbed {
	return errorEmbed("Not Configured", fmt.Sprintf("The %s **%s** is not set. An administrator can set it with `/%s set-role` or `/%s set-channel`.", d.Label, strings.ToLower(st.Name), d.Key, d.Key))
}

// ============================================================================
// Command
// ============================================================================

// NewCommand builds the /pugs, /pups or /premium command.
func NewCommand(d Div) *types.Command {
	user := func(desc string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{Name: "user", Description: desc, Type: discordgo.ApplicationCommandOptionUser, Required: true}
	}
	reason := &discordgo.ApplicationCommandOption{Name: "reason", Description: "Reason", Type: discordgo.ApplicationCommandOptionString, MaxLength: 512}
	sub := func(name, desc string, opts ...*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{Name: name, Description: desc, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts}
	}
	subs := []*discordgo.ApplicationCommandOption{
		sub("add", "Add a player to "+d.Label, user("The player to add"), reason),
		sub("remove", "Remove a player from "+d.Label, user("The player to remove"), reason),
		sub("add-spec", "Give a player the "+d.Label+" spec role", user("The player"), reason),
		sub("remove-spec", "Take the "+d.Label+" spec role from a player", user("The player"), reason),
		sub("list", "List the "+d.Label+" members"),
		sub("specs", "List the "+d.Label+" specs"),
	}
	if d.Vote {
		subs = append(subs, sub("vote", "Start a vote to add a player to "+d.Label, user("The player to vote on"), reason))
	}
	// The administrator's side: where the roles and channels are, and the managers of every division.
	setting := func(kind types.RequirementType) *discordgo.ApplicationCommandOption {
		o := &discordgo.ApplicationCommandOption{Name: "setting", Description: "What to set", Type: discordgo.ApplicationCommandOptionString, Required: true}
		for _, st := range Settings {
			if d.Has(st) && (kind == "" || st.Type == kind) {
				o.Choices = append(o.Choices, &discordgo.ApplicationCommandOptionChoice{Name: st.Name, Value: st.Key})
			}
		}
		return o
	}
	subs = append(subs,
		sub("managers", "Show the managers of every division"),
		sub("settings", "Show the "+d.Label+" roles and channels"),
		sub("set-role", "Set the "+d.Label+" member, spec or manager role (administrators)", setting(types.ReqRole),
			&discordgo.ApplicationCommandOption{Name: "role", Description: "The role", Type: discordgo.ApplicationCommandOptionRole, Required: true}),
		sub("set-channel", "Set a "+d.Label+" alerts or log channel (administrators)", setting(types.ReqChannel),
			&discordgo.ApplicationCommandOption{Name: "channel", Description: "The channel", Type: discordgo.ApplicationCommandOptionChannel, Required: true,
				ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews}}),
		sub("clear", "Clear a "+d.Label+" role or channel (administrators)", setting("")),
	)
	return &types.Command{
		Name:         d.Key,
		Description:  "Manages the " + d.Label + " division: its members, specs and managers.",
		Cooldown:     3 * time.Second,
		Requirements: d.Requirements(),
		Data:         &discordgo.ApplicationCommand{Options: subs},
		Execute:      func(s *discordgo.Session, i *discordgo.InteractionCreate) error { return execute(s, i, d) },
	}
}

func execute(s *discordgo.Session, i *discordgo.InteractionCreate, d Div) error {
	reply := func(e *discordgo.MessageEmbed) error { return helpers.EditInteractionEmbed(s, i, e) }
	sub, opts := types.SelectedSubcommand(i)
	cfg, err := Load(i.GuildID, d)
	if err != nil {
		return reply(unexpectedEmbed())
	}

	if sub == "list" || sub == "specs" || sub == "managers" {
		e, rows, err := Roster(s, i.GuildID, d, cfg, sub, 0)
		if err != nil {
			return reply(unexpectedEmbed())
		}
		return helpers.EditInteractionComponents(s, i, e, rows)
	}
	switch sub {
	case "settings":
		return reply(settingsEmbed(d, cfg))
	case "set-role", "set-channel", "clear":
		return reply(configure(s, i, d, sub, opts))
	case "vote", "add", "remove", "add-spec", "remove-spec":
	default:
		return reply(errorEmbed("Invalid Subcommand", "Use `/"+d.Key+" list`, `specs`, `managers`, `add`, `remove`, `add-spec`, `remove-spec` or `vote`."))
	}

	if !IsManager(s, i, cfg) {
		return reply(errorEmbed("Permission Denied", "Only "+d.Label+" managers can do this."))
	}
	target := helpers.SubOptionUser(s, opts, "user")
	if target == nil || target.Bot {
		return reply(errorEmbed("Invalid Target", "Select a real player from the command options."))
	}
	why := helpers.SubOptionString(opts, "reason")

	if sub == "vote" {
		return reply(startVote(s, i, d, cfg, target, why))
	}
	return reply(change(s, i, d, cfg, target, strings.HasPrefix(sub, "add"), strings.HasSuffix(sub, "-spec"), why))
}

// ============================================================================
// Role changes
// ============================================================================

// change adds or removes the division's member (or spec) role and posts the promotion or demotion alert. It returns the answer.
func change(s *discordgo.Session, i *discordgo.InteractionCreate, d Div, cfg Config, target *discordgo.User, add, spec bool, why string) *discordgo.MessageEmbed {
	st, roleID := SetMember, cfg.Member
	if spec {
		st, roleID = SetSpec, cfg.Spec
	}
	if roleID == "" {
		return notConfiguredEmbed(d, st)
	}
	member, err := helpers.FetchGuildMember(s, i.GuildID, target.ID)
	if err != nil {
		return errorEmbed("Player Not Found", "<@"+target.ID+"> is not in this server.")
	}
	has := helpers.MemberHasRole(member, roleID)
	if add && has {
		return errorEmbed("Already Added", fmt.Sprintf("<@%s> already has the %s %s.", target.ID, d.Label, strings.ToLower(st.Name)))
	}
	if !add && !has {
		return errorEmbed("Not Added", fmt.Sprintf("<@%s> does not have the %s %s.", target.ID, d.Label, strings.ToLower(st.Name)))
	}
	role, _ := s.State.Role(i.GuildID, roleID)
	bot, _ := helpers.FetchGuildMember(s, i.GuildID, s.State.User.ID)
	if helpers.GetRoleManageabilityIssue(s, i.GuildID, role, nil, bot) != helpers.RoleHierarchyOK {
		return errorEmbed("Role Not Manageable", "I cannot manage <@&"+roleID+">. It must be below my highest role, not managed by an integration and without administrator permission.")
	}

	// A player is in one division at a time: joining one leaves the others.
	var moved []string
	if add && !spec {
		for _, other := range All {
			if other.Key == d.Key {
				continue
			}
			oc, err := Load(i.GuildID, other)
			if err == nil && helpers.MemberHasRole(member, oc.Member) && helpers.RemoveMemberRole(s, i.GuildID, target.ID, oc.Member) == nil {
				moved = append(moved, other.Label)
			}
		}
	}
	var applyErr error
	if add {
		applyErr = helpers.AddMemberRole(s, i.GuildID, target.ID, roleID)
	} else {
		applyErr = helpers.RemoveMemberRole(s, i.GuildID, target.ID, roleID)
	}
	if applyErr != nil {
		return unexpectedEmbed()
	}

	username := playerName(i.GuildID, target)
	actor := helpers.InteractionUserID(i)
	notice := roleNotice{div: d, target: target, username: username, by: actor, roleID: roleID, add: add, spec: spec, reason: why, moved: moved}
	if cfg.AlertChannel != "" {
		_, _ = helpers.SendChannelMessage(s, i.GuildID, cfg.AlertChannel, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{notice.embed(true)}})
	}
	return notice.embed(false)
}

// roleNotice is a promotion, demotion or spec change, as the alert channel and the staff member who made it see it.
type roleNotice struct {
	div      Div
	target   *discordgo.User
	username string
	by       string
	roleID   string
	add      bool
	spec     bool
	reason   string
	moved    []string // the divisions the player left to join this one
}

// embed is the notice written the way the other commands write theirs: a title, a sentence, then the details as quoted lines with the
// player's Minecraft bust beside them. The alert (alert true) leaves out the role line; the answer to the staff member shows it.
func (n roleNotice) embed(alert bool) *discordgo.MessageEmbed {
	who := fmt.Sprintf("<@%s> (**%s**)", n.target.ID, n.username)
	title, preset, sentence := n.div.Label+" Demotion", "caution", fmt.Sprintf("%s was removed from **%s**.", who, n.div.Label)
	switch {
	case n.add && !n.spec:
		title, preset, sentence = n.div.Label+" Promotion", "success", fmt.Sprintf("%s was added to **%s**.", who, n.div.Label)
	case n.add:
		title, preset, sentence = n.div.Label+" Spec Added", "success", fmt.Sprintf("%s was given the **%s** spec role.", who, n.div.Label)
	case n.spec:
		title, sentence = n.div.Label+" Spec Removed", fmt.Sprintf("%s lost the **%s** spec role.", who, n.div.Label)
	}
	lines := []string{
		sentence, "",
		fmt.Sprintf("> **Player:** %s", who),
		fmt.Sprintf("> **Division:** `%s`", n.div.Label),
	}
	if !alert {
		lines = append(lines, "> **Role:** <@&"+n.roleID+">")
	}
	if len(n.moved) > 0 {
		lines = append(lines, "> **Left:** "+strings.Join(n.moved, ", "))
	}
	if n.reason != "" {
		lines = append(lines, "> **Reason:** "+n.reason)
	}
	lines = append(lines, "> **By:** <@"+n.by+">")
	return helpers.Preset(preset, types.PresetOptions{Title: title, Description: strings.Join(lines, "\n"), Thumbnail: helpers.MinecraftBustAvatarURL(n.username)})
}

// playerName is the player's registered username, or their Discord name when they have none.
func playerName(guildID string, u *discordgo.User) string {
	if view, err := database.Must().Player.Get(guildID, u.ID); err == nil && view.Config != nil && view.Config.Username != "" {
		return view.Config.Username
	}
	return u.Username
}

// ============================================================================
// Configuration (administrators)
// ============================================================================

func mention(t types.RequirementType, id string) string {
	if t == types.ReqRole {
		return "<@&" + id + ">"
	}
	return "<#" + id + ">"
}

// settingsEmbed lists the division's roles and channels.
func settingsEmbed(d Div, cfg Config) *discordgo.MessageEmbed {
	var lines []string
	for _, st := range Settings {
		if !d.Has(st) {
			continue
		}
		value := "`Not configured`"
		if id := cfg.Value(st); id != "" {
			value = mention(st.Type, id)
		}
		lines = append(lines, fmt.Sprintf("> **%s:** %s", st.Name, value))
	}
	return helpers.Preset("info", types.PresetOptions{Title: d.Label + " Settings", Description: strings.Join(lines, "\n"), Thumbnail: helpers.ThumbGuild})
}

// configure sets or clears one role or channel of the division. The slots are the division command's requirements, so /config and
// the dashboard show the same assignments.
func configure(s *discordgo.Session, i *discordgo.InteractionCreate, d Div, sub string, opts []*discordgo.ApplicationCommandInteractionDataOption) *discordgo.MessageEmbed {
	if !helpers.MemberIsAdministrator(s, i) {
		return errorEmbed("Permission Denied", "Only administrators can change the division settings.")
	}
	st, ok := FindSetting(helpers.SubOptionString(opts, "setting"))
	if !ok || !d.Has(st) {
		return errorEmbed("Invalid Setting", "Pick a setting from the options.")
	}
	id := ""
	switch sub {
	case "set-role":
		id = helpers.SubOptionRoleID(s, opts, "role")
	case "set-channel":
		id = helpers.SubOptionChannelID(opts, "channel")
	}
	if sub != "clear" && id == "" {
		return errorEmbed("Missing Value", "You must pick a "+string(st.Type)+".")
	}
	if err := helpers.SetRequirementSlotID(helpers.InteractionUserID(i), i.GuildID, d.Key, st.Type, d.Slot(st), id); err != nil {
		return errorEmbed("Not Saved", "The setting could not be saved. If this is a new server, run `/config sync` first so the division commands are set up.")
	}
	value := "`Cleared`"
	if id != "" {
		value = mention(st.Type, id)
	}
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Division Updated",
		Description: fmt.Sprintf("> **Division:** `%s`\n> **Setting:** `%s`\n> **Value:** %s", d.Label, st.Name, value),
		Thumbnail:   helpers.ThumbGuild,
	})
}
