package helpers

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestTheLastPlayerOfTheDraftIsPlacedWithoutAPick(t *testing.T) {
	players := []string{"a", "b", "c", "d", "e", "f"}
	d, err := NewDraft(players, 2, 3, map[string]int{"a": 600, "b": 500, "c": 400, "d": 300, "e": 200, "f": 100}, nil,
		DraftConfig{TopEloChance: 1, PickingType: "one_per_person"}, rand.New(rand.NewPCG(1, 2)))
	if err != nil {
		t.Fatal(err)
	}
	// Three picks leave one player in the pool and one team with room.
	for range 3 {
		turn := d.Turn()
		if turn < 0 {
			t.Fatal("the draft ended before the picks")
		}
		if _, err := d.Pick(d.Captains()[turn], d.Pool()[0]); err != nil {
			t.Fatal(err)
		}
	}
	if turn := d.Turn(); turn != -1 {
		t.Fatalf("one player is left and only one team has room: the draft is over, got turn %d", turn)
	}
	for i, team := range d.Teams {
		if len(team) != 3 {
			t.Errorf("team %d has %d players, want 3", i, len(team))
		}
	}
	if len(d.filled) != 1 || !slices.Contains(players, d.filled[0]) {
		t.Errorf("the placed player is recorded: %v", d.filled)
	}
}

// pickers runs a whole alternating draft and returns which team made each pick, by weakness rank (0 = the team that picks first).
func pickers(t *testing.T, players []string, perTeam int) []int {
	t.Helper()
	ratings := map[string]int{}
	for i, id := range players {
		ratings[id] = 1000 - i*10
	}
	d, err := NewDraft(players, 2, perTeam, ratings, nil, DraftConfig{TopEloChance: 1, PickingType: "alternating"}, rand.New(rand.NewPCG(3, 4)))
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for {
		turn := d.Turn()
		if turn < 0 {
			return out
		}
		out = append(out, slices.Index(d.order, turn))
		if _, err := d.Pick(d.Captains()[turn], d.Pool()[0]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAlternatingDraftPicksOneTwoTwoOne(t *testing.T) {
	eight := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	if got, want := pickers(t, eight, 4), []int{0, 1, 1, 0, 0}; !slices.Equal(got, want) { // the sixth and last player goes to the second team without a pick
		t.Errorf("4v4: picks by team %v, want %v (first team 1, second 2, first 2, then the last player is placed)", got, want)
	}
	six := eight[:6]
	if got, want := pickers(t, six, 3), []int{0, 1, 1}; !slices.Equal(got, want) { // the last player goes to the first team without a pick
		t.Errorf("3v3: picks by team %v, want %v", got, want)
	}
}

func TestTheCaptainWhoPicksFirstIsAlwaysTeamA(t *testing.T) {
	players := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	ratings := map[string]int{"a": 900, "b": 800, "c": 700, "d": 600, "e": 500, "f": 400, "g": 300, "h": 200}
	for seed := uint64(0); seed < 30; seed++ { // the captains are drawn at random: whichever way they land, the first picker is Team A
		d, err := NewDraft(players, 2, 4, ratings, nil, DraftConfig{TopEloChance: 1, PickingType: "alternating"}, rand.New(rand.NewPCG(seed, 7)))
		if err != nil {
			t.Fatal(err)
		}
		if turn := d.Turn(); turn != 0 {
			t.Fatalf("seed %d: team %d picks first, want Team A (index 0)", seed, turn)
		}
		if d.teamElo(0) > d.teamElo(1) {
			t.Fatalf("seed %d: Team A (%d) is stronger than Team B (%d), so it should not pick first", seed, d.teamElo(0), d.teamElo(1))
		}
	}
}
