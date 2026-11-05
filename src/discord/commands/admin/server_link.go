package admin

import (
	"crypto/rand"
	"fmt"
	"log"
	"strings"
	"time"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/guildlink"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

func init() {
	helpers.Register(ServerLinkCommand)
}

// ServerLinkCommand connects a secondary server (a staff server, say) to a main guild, so the bot works there as part of it.
//
//	main guild:       /link create          makes a one-time code
//	secondary server: /link join <code>     claims it
//	main guild:       /link list, /link remove
//	either:           /link leave           (run in the secondary server) ends the link
//
// The secondary server gets no configuration of its own: everything done there uses the main guild's settings, roles, channels and data
// (see package guildlink).
var ServerLinkCommand = &types.Command{
	Name:           "link",
	Description:    "Connects a secondary server (such as a staff server) to this guild.",
	SkipGuildCheck: true,
	Ephemeral:      true,
	Cooldown:       3 * time.Second,
	Data: &discordgo.ApplicationCommand{
		DefaultMemberPermissions: &adminPermissions,
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "create", Description: "In the main server: make a one-time code to link a secondary server", Type: discordgo.ApplicationCommandOptionSubCommand},
			{
				Name: "join", Description: "In the secondary server: link it to a main server with its code", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{{Name: "code", Description: "The code from /link create", Type: discordgo.ApplicationCommandOptionString, Required: true}},
			},
			{Name: "list", Description: "List the secondary servers linked to this guild", Type: discordgo.ApplicationCommandOptionSubCommand},
			{
				Name: "remove", Description: "Unlink a secondary server (its server ID) or cancel an unused code", Type: discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{{Name: "server", Description: "The secondary server's ID, or the unused code", Type: discordgo.ApplicationCommandOptionString, Required: true}},
			},
			{Name: "leave", Description: "In the secondary server: unlink it from its main server", Type: discordgo.ApplicationCommandOptionSubCommand},
		},
	},
	Execute: executeServerLinkCommand,
}

func executeServerLinkCommand(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !helpers.MemberIsAdministrator(s, i) {
		return reply(s, i, "error", "Permission Denied", "Only administrators can link servers.")
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
	venue := guildlink.VenueOf(i.ID) // set when the command was used in a secondary server

	switch sub {
	case "create":
		return linkCreate(s, i, venue)
	case "join":
		return linkJoin(s, i, venue, arg("code"))
	case "list":
		return linkList(s, i, venue)
	case "remove":
		return linkRemove(s, i, venue, arg("server"))
	case "leave":
		return linkLeave(s, i, venue)
	}
	return reply(s, i, "error", "Invalid Subcommand", "Use `/link create`, `/link join`, `/link list`, `/link remove` or `/link leave`.")
}

func reply(s *discordgo.Session, i *discordgo.InteractionCreate, kind, title, text string) error {
	return helpers.EditInteractionEmbed(s, i, helpers.Preset(kind, types.PresetOptions{Title: title, Description: text, Thumbnail: helpers.ThumbFlag}))
}

// registered says whether the guild is a main guild with data of its own (a secondary server never is).
func registered(guildID string) bool {
	svc := database.Get()
	if svc == nil {
		return false
	}
	_, err := svc.Guild.Get(guildID)
	return err == nil
}

func linkCreate(s *discordgo.Session, i *discordgo.InteractionCreate, venue string) error {
	if venue != "" {
		return reply(s, i, "error", "Run This In The Main Server", "This server is already linked to a main server. Make the code there with `/link create`.")
	}
	if !registered(i.GuildID) {
		return reply(s, i, "error", "Server Not Set Up", "Set this server up first with `/setup init`, then make a link code.")
	}
	code := newLinkCode()
	if _, err := database.CreateGuildLink(i.GuildID, code, helpers.InteractionUserID(i)); err != nil {
		return reply(s, i, "error", "Could Not Make A Code", fmt.Sprintf("The link code could not be saved: %v", err))
	}
	return reply(s, i, "success", "Link Code Ready",
		fmt.Sprintf("Add the bot to your secondary server and run:\n\n`/link join code:%s`\n\nThe code works once and expires in **30 minutes**. The secondary server will use this server's settings, roles, channels and data: commands used there are logged and answered in the same way, and nothing has to be set up again.", code))
}

func linkJoin(s *discordgo.Session, i *discordgo.InteractionCreate, venue, code string) error {
	if venue != "" {
		return reply(s, i, "error", "Already Linked", "This server is already linked to a main server. Use `/link leave` first to link it somewhere else.")
	}
	// The bot registers a server as soon as it joins, so a secondary server usually has an empty record by now: that one may be linked
	// (and is removed below). A server with players or games of its own is a real main server.
	placeholder := false
	if registered(i.GuildID) {
		hasData, err := database.GuildHasData(i.GuildID)
		if err != nil || hasData {
			return reply(s, i, "error", "This Is A Main Server", "This server is set up with Ranked Bedwars itself and has players or games, so it cannot be linked as a secondary server.")
		}
		placeholder = true
	}
	if code == "" {
		return reply(s, i, "error", "Code Needed", "Give the code made by `/link create` in the main server.")
	}
	name := i.GuildID
	if g, err := s.State.Guild(i.GuildID); err == nil && g != nil {
		name = g.Name
	}
	link, err := database.ClaimLink(code, i.GuildID, name)
	if err != nil {
		msg := err.Error()
		if api, ok := err.(*database.APIStatusError); ok {
			msg = api.Message
		}
		return reply(s, i, "error", "Could Not Link", msg+".")
	}
	guildlink.Set(i.GuildID, link.HomeGuildID)
	if placeholder {
		if err := database.DeleteGuild(i.GuildID); err != nil {
			log.Printf("link: could not remove the empty record of secondary server %s: %v", i.GuildID, err)
		}
	}
	mainName := link.HomeGuildID
	if g, err := s.State.Guild(link.HomeGuildID); err == nil && g != nil {
		mainName = g.Name
	}
	return reply(s, i, "success", "Server Linked",
		fmt.Sprintf("This server is now linked to **%s**. Every command works here as it does there, with that server's settings, roles, channels and data. Only members who are also in **%s** (and hold its staff role where a command needs it) can use them. Custom commands appear here within a minute.", mainName, mainName))
}

func linkList(s *discordgo.Session, i *discordgo.InteractionCreate, venue string) error {
	if venue != "" {
		return reply(s, i, "error", "Run This In The Main Server", "List the linked servers from the main server.")
	}
	rows, err := database.GuildLinks(i.GuildID)
	if err != nil {
		return reply(s, i, "error", "Could Not Load Links", fmt.Sprintf("%v", err))
	}
	var lines []string
	for _, r := range rows {
		switch {
		case r.VenueGuildID != nil:
			name := r.VenueName
			if name == "" {
				name = "Secondary server"
			}
			lines = append(lines, fmt.Sprintf("**%s** · `%s`", name, *r.VenueGuildID))
		case r.Code != nil && (r.CodeExpiresAt == nil || r.CodeExpiresAt.After(time.Now())):
			lines = append(lines, fmt.Sprintf("Waiting for a server · code `%s`", *r.Code))
		}
	}
	if len(lines) == 0 {
		return reply(s, i, "info", "No Linked Servers", "Make a code with `/link create` and use it in a secondary server.")
	}
	return reply(s, i, "info", "Linked Servers", strings.Join(lines, "\n"))
}

func linkRemove(s *discordgo.Session, i *discordgo.InteractionCreate, venue, which string) error {
	if venue != "" {
		return reply(s, i, "error", "Run This In The Main Server", "Remove linked servers from the main server, or use `/link leave` here.")
	}
	rows, err := database.GuildLinks(i.GuildID)
	if err != nil {
		return reply(s, i, "error", "Could Not Load Links", fmt.Sprintf("%v", err))
	}
	for _, r := range rows {
		isServer := r.VenueGuildID != nil && *r.VenueGuildID == which
		isCode := r.Code != nil && strings.EqualFold(*r.Code, which)
		if !isServer && !isCode {
			continue
		}
		if err := database.DeleteGuildLink(i.GuildID, r.ID); err != nil {
			return reply(s, i, "error", "Could Not Remove", fmt.Sprintf("%v", err))
		}
		if isServer {
			guildlink.Set(which, "")
		}
		return reply(s, i, "success", "Link Removed", "That link is gone. The bot no longer answers there.")
	}
	return reply(s, i, "error", "Not Found", "No linked server or unused code matches that. See `/link list`.")
}

func linkLeave(s *discordgo.Session, i *discordgo.InteractionCreate, venue string) error {
	if venue == "" {
		return reply(s, i, "error", "Not A Secondary Server", "This server is not linked to another one.")
	}
	rows, err := database.GuildLinks(i.GuildID) // i.GuildID is the main guild here
	if err != nil {
		return reply(s, i, "error", "Could Not Load Links", fmt.Sprintf("%v", err))
	}
	for _, r := range rows {
		if r.VenueGuildID != nil && *r.VenueGuildID == venue {
			if err := database.DeleteGuildLink(i.GuildID, r.ID); err != nil {
				return reply(s, i, "error", "Could Not Unlink", fmt.Sprintf("%v", err))
			}
			guildlink.Set(venue, "")
			return reply(s, i, "success", "Server Unlinked", "This server is no longer linked. Commands stop working here.")
		}
	}
	return reply(s, i, "error", "Not Found", "The link could not be found.")
}

// newLinkCode is a short code that is easy to read out and type (no 0/O or 1/I).
func newLinkCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for n := range b {
		b[n] = alphabet[int(b[n])%len(alphabet)]
	}
	return string(b)
}
