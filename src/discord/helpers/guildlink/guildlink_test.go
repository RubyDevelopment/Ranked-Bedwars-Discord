package guildlink

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func link(t *testing.T) {
	t.Helper()
	Set("staff", "main")
	t.Cleanup(func() { Set("staff", "") })
}

// A command, button or form used in the secondary server reaches the handlers as if it were used in the main guild.
func TestInteractionLooksLikeTheMainGuild(t *testing.T) {
	link(t)
	in := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{ID: "1", GuildID: "staff", ChannelID: "chan",
		Member: &discordgo.Member{User: &discordgo.User{ID: "u"}, Permissions: discordgo.PermissionAdministrator, Roles: []string{"staff-server-admin"}}}}
	out, keep := Translate(nil, in)
	got := out.(*discordgo.InteractionCreate)
	if !keep || got.GuildID != "main" || got.ChannelID != "chan" {
		t.Fatalf("guild %q channel %q keep %v", got.GuildID, got.ChannelID, keep)
	}
	// The member is the person as a member of the main guild: the secondary server's roles and administrator right do not carry over.
	if got.Member.GuildID != "main" || got.Member.Permissions != 0 || len(got.Member.Roles) != 0 || got.Member.User.ID != "u" {
		t.Fatalf("member %+v", got.Member)
	}
	if in.GuildID != "staff" || in.Member.Permissions == 0 {
		t.Fatal("the original event must stay as Discord sent it (other handlers share it)")
	}
	if VenueOf("1") != "staff" {
		t.Fatalf("origin %q", VenueOf("1"))
	}
}

func TestPrefixMessageLooksLikeTheMainGuild(t *testing.T) {
	link(t)
	in := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "9", GuildID: "staff", ChannelID: "chan", Author: &discordgo.User{ID: "u"}}}
	out, keep := Translate(nil, in)
	got := out.(*discordgo.MessageCreate)
	if !keep || got.GuildID != "main" || got.Member == nil || got.Member.GuildID != "main" {
		t.Fatalf("%+v keep %v", got.Message, keep)
	}
	if VenueOf("prefix:9") != "staff" {
		t.Fatal("origin of a prefix command is not remembered")
	}
}

// Everything else a secondary server does is not the community's business; the main guild's own events pass untouched.
func TestOtherEventsOfASecondaryServerAreDropped(t *testing.T) {
	link(t)
	for name, ev := range map[string]any{
		"member joined": &discordgo.GuildMemberAdd{Member: &discordgo.Member{GuildID: "staff"}},
		"voice":         &discordgo.VoiceStateUpdate{VoiceState: &discordgo.VoiceState{GuildID: "staff"}},
		"guild create":  &discordgo.GuildCreate{Guild: &discordgo.Guild{ID: "staff"}},
		"message gone":  &discordgo.MessageDelete{Message: &discordgo.Message{GuildID: "staff"}},
	} {
		if _, keep := Translate(nil, ev); keep {
			t.Errorf("%s of a secondary server is handled", name)
		}
	}
	for name, ev := range map[string]any{
		"main member":  &discordgo.GuildMemberAdd{Member: &discordgo.Member{GuildID: "main"}},
		"main message": &discordgo.MessageCreate{Message: &discordgo.Message{GuildID: "main"}},
		"ready":        &discordgo.Ready{},
		"dm":           &discordgo.MessageCreate{Message: &discordgo.Message{}},
	} {
		out, keep := Translate(nil, ev)
		if !keep || out != ev {
			t.Errorf("%s was changed or dropped", name)
		}
	}
}

// A wrapped handler keeps its type (so discordgo still accepts it), is not called for dropped events and gets the rewritten event.
func TestWrapKeepsTheHandlerType(t *testing.T) {
	link(t)
	var seen string
	var handler func(*discordgo.Session, *discordgo.InteractionCreate) = func(_ *discordgo.Session, i *discordgo.InteractionCreate) { seen = i.GuildID }
	wrapped, ok := Wrap(handler).(func(*discordgo.Session, *discordgo.InteractionCreate))
	if !ok {
		t.Fatal("the wrapper changed the handler's type")
	}
	wrapped(nil, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{ID: "2", GuildID: "staff"}})
	if seen != "main" {
		t.Fatalf("handler saw %q", seen)
	}
	called := false
	voice, _ := Wrap(func(*discordgo.Session, *discordgo.VoiceStateUpdate) { called = true }).(func(*discordgo.Session, *discordgo.VoiceStateUpdate))
	voice(nil, &discordgo.VoiceStateUpdate{VoiceState: &discordgo.VoiceState{GuildID: "staff"}})
	if called {
		t.Fatal("a dropped event reached its handler")
	}
}
