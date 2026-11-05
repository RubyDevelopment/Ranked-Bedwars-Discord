package guildlink_test

import (
	"testing"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/guildlink"
	"ranked-bedwars/src/types"
)

// `=strike <player> testing`, typed in a linked staff server: the command is the main guild's (its prefix, its aliases, its settings), and the
// answer goes back to the staff server's channel as a reply to the message.
func TestPrefixCommandInAStaffServer(t *testing.T) {
	guildlink.Set("staff", "main")
	t.Cleanup(func() { guildlink.Set("staff", "") })

	strike := &types.Command{Name: "strike", Data: &discordgo.ApplicationCommand{
		Name: "strike",
		Options: []*discordgo.ApplicationCommandOption{
			{Name: "user", Type: discordgo.ApplicationCommandOptionUser, Required: true},
			{Name: "reason", Type: discordgo.ApplicationCommandOptionString, Required: true},
		},
	}}

	// What Discord sends for the message in the staff server.
	sent := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "900", GuildID: "staff", ChannelID: "staff-chan", Content: "=strike 1070401748663009352 testing",
		Author: &discordgo.User{ID: "mod"}, Member: &discordgo.Member{Roles: []string{"staff-server-role"}},
	}}
	out, keep := guildlink.Translate(nil, sent)
	if !keep {
		t.Fatal("the message was dropped")
	}
	m := out.(*discordgo.MessageCreate)
	if m.GuildID != "main" {
		t.Fatalf("handlers see guild %q, want the main guild", m.GuildID)
	}

	// The main guild's settings (its prefix and its aliases for the command) decide how the message is read.
	settings := types.GuildSettings{Prefix: "=", PrefixEnabled: true, SlashEnabled: true, Commands: types.GuildCommandsMap{"strike": {Aliases: []string{"warnuser"}}}}
	registry := map[string]*types.Command{"strike": strike}
	for _, trigger := range []string{"strike", "warnuser"} {
		route, cmd, problem := helpers.ResolvePrefixRoute(registry, settings, trigger, []string{"1070401748663009352", "testing"})
		if problem != "" || cmd != strike || route == nil {
			t.Fatalf("%q did not resolve to /strike: %q", trigger, problem)
		}
		i := helpers.BuildPrefixInteraction(nil, m.Message, cmd, route)
		if i.GuildID != "main" || i.Member == nil || i.Member.GuildID != "main" || len(i.Member.Roles) != 0 {
			t.Fatalf("interaction is for guild %q, member %+v", i.GuildID, i.Member)
		}
		if got := helpers.OptionString(i, "reason"); got != "testing" {
			t.Fatalf("reason %q", got)
		}
		// The reply is posted where the message was written, referring to it in that server.
		if guildlink.VenueOf(i.ID) != "staff" || guildlink.ReplyGuild(i.ID, i.GuildID) != "staff" || i.ChannelID != "staff-chan" {
			t.Fatalf("reply goes to %q channel %q", guildlink.ReplyGuild(i.ID, i.GuildID), i.ChannelID)
		}
		// Channel limits name the main guild's channels, so the staff server's channel is not held to them.
		if helpers.ChannelForAccess(i.ID, i.ChannelID) != "" {
			t.Fatal("a staff server channel is checked against the main guild's channel limits")
		}
	}

	// A message in the main guild itself is untouched.
	home := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "901", GuildID: "main", ChannelID: "main-chan", Author: &discordgo.User{ID: "mod"}}}
	if out, _ := guildlink.Translate(nil, home); out != home {
		t.Fatal("a main guild message was rewritten")
	}
	if guildlink.ReplyGuild("prefix:901:strike", "main") != "main" {
		t.Fatal("a reply in the main guild refers to another server")
	}
}
