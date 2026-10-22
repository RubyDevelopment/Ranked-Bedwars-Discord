package helpers

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestFillLayout(t *testing.T) {
	DefineLayout("test.row", "row", "{name}{note}\n{missing}\n> {tier}", "name", "note", "tier")
	got := FillLayout("", "test.row", map[string]string{"name": "Bot1", "note": " · disabled"})
	if want := "Bot1 · disabled"; got != want { // "> {tier}" has only an empty variable, so the line goes
		t.Errorf("got %q, want %q", got, want)
	}
	if got := FillLayout("", "test.row", map[string]string{"name": "A", "tier": "gold"}); got != "A\n> gold" {
		t.Errorf("got %q", got)
	}
}

func TestLayoutsOf(t *testing.T) {
	DefineLayout("testcmd.b", "b", "B")
	DefineRepeatedLayout("testcmd.a", "a", "A")
	got := layoutsOf("testcmd")
	if len(got) != 2 || got[0].Key != "testcmd.a" || !got[0].Repeats || got[1].Key != "testcmd.b" {
		t.Errorf("got %+v", got)
	}
}

func TestInteractionVarsOptions(t *testing.T) {
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "1", GuildID: "g", ChannelID: "c",
		Member: &discordgo.Member{User: &discordgo.User{ID: "42", Username: "wiggels"}, Nick: "Wig", Roles: []string{"r1"}},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "ban",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "player", Type: discordgo.ApplicationCommandOptionUser, Value: "7"},
				{Name: "reason", Type: discordgo.ApplicationCommandOptionString, Value: "cheating"},
			},
			Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Users: map[string]*discordgo.User{"7": {ID: "7", Username: "steve"}}},
		},
	}}
	v := InteractionVars(nil, i)
	for k, want := range map[string]string{
		"command.name": "ban", "option.reason": "cheating", "option.player.id": "7", "option.player.mention": "<@7>",
		"option.player.name": "steve", "user.id": "42", "member.nick": "Wig", "member.roles": "<@&r1>", "channel.mention": "<#c>",
	} {
		if v[k] != want {
			t.Errorf("%s = %q, want %q", k, v[k], want)
		}
	}
}

func TestTeamVarsPlayerLine(t *testing.T) {
	g := Game{Teams: []string{"Red"}, Players: []GamePlayer{{UserID: "1", Username: "Ann", Team: "Red", EloBefore: 1500, Captain: true}}}
	d := embedData{Game: g}
	if got := d.teamVars("plain", embedTemplate{})[0]["teamPlayers"]; got != "- <@1>" {
		t.Errorf("default line = %q", got)
	}
	line := "{captain} {name} ({elo})"
	if got := d.teamVars("plain", embedTemplate{PlayerLine: &line})[0]["teamPlayers"]; got != "👑 Ann (1500)" {
		t.Errorf("custom line = %q", got)
	}
}
