package helpers

import (
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func noFlow(t *testing.T, labels map[string]string) {
	old := flowLoader
	flowLoader = func(string) guildFlow { return guildFlow{ButtonLabels: labels} }
	flowMu.Lock()
	flowCache = map[string]struct {
		at   time.Time
		flow guildFlow
	}{}
	flowMu.Unlock()
	t.Cleanup(func() { flowLoader = old })
}

func TestHostButtonsOnlyUnderTheHostedGamesPartyAndTeamEmbeds(t *testing.T) {
	noFlow(t, nil)
	g := Game{Bot: "Host"}
	for _, key := range []string{"partyInvitation", "teamSelection", "matchJoined"} {
		if hostButtons(g, key, embedTemplate{}) == nil {
			t.Errorf("%s should carry the host buttons", key)
		}
	}
	if hostButtons(g, "introduction", embedTemplate{}) != nil {
		t.Error("other embeds carry no host buttons")
	}
	if hostButtons(Game{}, "teamSelection", embedTemplate{}) != nil {
		t.Error("a game nobody hosts by bot has no host to press buttons on")
	}
	off := false
	if hostButtons(g, "teamSelection", embedTemplate{Buttons: &off}) != nil {
		t.Error("the guild can switch the buttons off per embed")
	}
}

func TestHostButtonIDsNameTheBotAndParseBack(t *testing.T) {
	noFlow(t, map[string]string{"pg_kick": "Remove"})
	row := hostButtons(Game{Bot: "Host_01"}, "partyInvitation", embedTemplate{})[0].(*discordgo.ActionsRow)
	want := []string{"party_warp", "party_invite", "game_warp", "pg_kick"}
	if len(row.Components) != len(want) {
		t.Fatalf("got %d buttons, want %d", len(row.Components), len(want))
	}
	if row.Components[3].(*discordgo.Button).Label != "Remove" || row.Components[0].(*discordgo.Button).Label != "Party Warp" {
		t.Error("labels come from the profile, with the default for a blank one")
	}
	for i, c := range row.Components {
		m := hostButtonID.FindStringSubmatch(c.(*discordgo.Button).CustomID)
		if m == nil || m[1] != want[i] || m[2] != "Host_01" {
			t.Errorf("button %d has id %q", i, c.(*discordgo.Button).CustomID)
		}
	}
}
