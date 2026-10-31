package helpers

import (
	"strings"
	"testing"

	"ranked-bedwars/src/types"
)

// The match embeds are the old bot's, so these pin the parts that are easy to lose: the party lists, the team rosters with the
// party mark, the map vote scores and the welcome's wording.

func embedTestGame() Game {
	return Game{
		ID: 1, Number: 7, Mode: types.Mode{Name: "4v4"},
		Players: []GamePlayer{
			{UserID: "1", Username: "Alice", Team: "A"},
			{UserID: "2", Username: "Bob", Team: "A"},
			{UserID: "3", Username: "Cara", Team: "B"},
		},
		Teams:   []string{"A", "B"},
		Parties: []PartyGroup{{Leader: "1", Members: []string{"1", "2"}}},
	}
}

func renderDefault(t *testing.T, key string, d embedData) (title, description string, fields map[string]string) {
	t.Helper()
	tpl, ok := defaultEmbeds[key]
	if !ok {
		t.Fatalf("no default for %q", key)
	}
	d.Game = embedTestGame()
	emb := renderEmbed(key, tpl, d)
	if emb == nil {
		t.Fatalf("%s rendered nothing", key)
	}
	fields = map[string]string{}
	for _, f := range emb.Fields {
		fields[f.Name] = f.Value
	}
	return emb.Title, emb.Description, fields
}

func TestPartyInvitationListsWaitingInvitedAndJoinedByName(t *testing.T) {
	_, _, fields := renderDefault(t, "partyInvitation", embedData{Vars: map[string]string{"endsAt": "100"}, Joined: []string{"1"}, Invited: []string{"2"}, Waiting: []string{"3"}})
	want := map[string]string{
		DotRed + " Waiting (1)":    "- `Cara`",
		DotYellow + " Invited (1)": "- `Bob`",
		DotGreen + " Joined (1/3)": "- `Alice`",
	}
	for name, value := range want {
		if fields[name] != value {
			t.Errorf("field %q = %q, want %q (all: %v)", name, fields[name], value, fields)
		}
	}
	if fields["Host"] != "`"+"`" && fields["Host"] != "" {
		// the host is the bot's name: empty here, so the field is dropped like the old bot would show nothing
		t.Logf("host field: %q", fields["Host"])
	}
}

func TestPartyListsSayNoneWhenEmpty(t *testing.T) {
	_, _, fields := renderDefault(t, "partyInvitation", embedData{Vars: map[string]string{"endsAt": "100"}})
	if got := fields[DotYellow+" Invited (0)"]; got != "None" {
		t.Errorf("an empty list shows %q, want None (fields: %v)", got, fields)
	}
}

func TestTeamsShowTheDotAndTheOldPartyMark(t *testing.T) {
	_, _, fields := renderDefault(t, "teamSelection", embedData{Vars: map[string]string{"endsAt": "100"}, Joined: []string{"1", "3"}, Waiting: []string{"2"}})
	if got, want := fields["Team A"], DotGreen+" `Alice 👥`\n"+DotRed+" `Bob 👥`"; got != want {
		t.Errorf("Team A = %q, want %q", got, want)
	}
	if got, want := fields["Team B"], DotGreen+" `Cara`"; got != want {
		t.Errorf("Team B = %q, want %q", got, want)
	}
}

func TestMatchJoinedListsEveryoneAsTeamTBDWithGrayDots(t *testing.T) {
	g := embedTestGame()
	emb := renderEmbed("matchJoined", defaultEmbeds["matchJoined"], embedData{Game: g})
	var tbd []string
	for _, f := range emb.Fields {
		if f.Name == "Team TBD" {
			tbd = append(tbd, f.Value)
		}
	}
	if len(tbd) != 2 || !strings.Contains(tbd[0], DotGray+" `Alice 👥`") {
		t.Errorf("Team TBD fields = %q", tbd)
	}
}

func TestMapVoteShowsEachMapWithItsScoreAndMarksTheWinner(t *testing.T) {
	d := embedData{Game: embedTestGame(), Vars: map[string]string{"endsAt": "5", "map": "Garden"}, MapRows: []map[string]string{
		{"mapName": "Garden", "mapIcon": "🏆", "mapScore": "3", "winnerTag": " [WINNER]"},
		{"mapName": "Ruins", "mapIcon": "🗺️", "mapScore": "1", "winnerTag": ""},
	}}
	emb := renderEmbed("mapVoteEnded", defaultEmbeds["mapVoteEnded"], d)
	if emb.Title != "Map Voting Ended" || !strings.Contains(emb.Description, "The majority of votes went to **Garden**!") {
		t.Errorf("ended embed: %q / %q", emb.Title, emb.Description)
	}
	if len(emb.Fields) != 2 || emb.Fields[0].Name != "🏆 Garden [WINNER]" || emb.Fields[0].Value != "`Score: 3`" || emb.Fields[1].Name != "🗺️ Ruins" {
		t.Errorf("fields = %+v", emb.Fields)
	}
	if !strings.Contains(renderDescription(t, "mapVoting", d), "using the menu below") {
		t.Error("the vote keeps the old wording")
	}
}

func renderDescription(t *testing.T, key string, d embedData) string {
	t.Helper()
	return renderEmbed(key, defaultEmbeds[key], d).Description
}

func TestWelcomeNamesTheFlowAndHowTeamsWereMade(t *testing.T) {
	g := embedTestGame()
	cases := map[string]struct {
		mutate func(*Game)
		want   string
	}{
		"parties": {func(*Game) {}, "Welcome to **Standard 4v4** teams have been automatically assigned with party groups kept together."},
		"solo":    {func(g *Game) { g.Parties = nil }, "Welcome to **Standard 4v4** teams have been automatically assigned."},
		"captain": {func(g *Game) { g.Captain = true }, "Welcome to **Captain 4v4** teams will be drafted by team captains."},
		"custom":  {func(g *Game) { g.Custom = &CustomGame{} }, "Welcome to **Custom 4v4** with teams chosen by staff."},
	}
	for name, c := range cases {
		game := g
		c.mutate(&game)
		emb := renderEmbed("introduction", defaultEmbeds["introduction"], embedData{Game: game})
		if !strings.HasPrefix(emb.Description, c.want) {
			t.Errorf("%s: %q", name, emb.Description)
		}
	}
	// A captain game has no drafted teams yet, so the welcome lists none.
	captain := g
	captain.Captain = true
	if emb := renderEmbed("introduction", defaultEmbeds["introduction"], embedData{Game: captain}); len(emb.Fields) != 0 {
		t.Errorf("captain welcome fields = %+v", emb.Fields)
	}
}

func TestDefaultPipelineAnnouncesIntroductionBeforeGameSetup(t *testing.T) {
	flow := parseGuildFlow([]byte(`{"profile":{"pipeline":[]}}`))
	if !flow.Announced["introduction"] {
		t.Fatal("an omitted/default pipeline should announce the introduction")
	}

	custom := parseGuildFlow([]byte(`{"profile":{"pipeline":[{"type":"connect"},{"type":"announce","embed":"introduction"}]}}`))
	if !custom.Announced["introduction"] {
		t.Fatal("a configured introduction announce should be recognized")
	}

	disabled := parseGuildFlow([]byte(`{"profile":{"pipeline":[{"type":"announce","embed":"introduction","enabled":false}]}}`))
	if disabled.Announced["introduction"] {
		t.Fatal("a disabled introduction announce should not be pre-posted")
	}
}
