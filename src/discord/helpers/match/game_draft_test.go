package match

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"

	"ranked-bedwars/src/discord/helpers/partymgr"
)

// Every team size and party layout: parties get the captains, a lone party gives up the first pick, nobody is split.
func TestDraftParties(t *testing.T) {
	for _, tc := range []struct {
		perTeam int
		sizes   []int
	}{
		{2, []int{2}}, {2, []int{2, 2}}, {3, []int{2}}, {3, []int{3}}, {3, []int{2, 2}}, {3, []int{3, 3}},
		{4, []int{2}}, {4, []int{3}}, {4, []int{4}}, {4, []int{2, 3}}, {4, []int{4, 4}}, {4, []int{2, 2, 3}}, {4, []int{2, 2, 2, 2}},
	} {
		for _, pickType := range []string{"one_per_person", "alternating"} {
			for seed := range uint64(200) {
				r := rand.New(rand.NewPCG(seed, 1))
				var players []string
				ratings := map[string]int{}
				for i := range tc.perTeam * 2 {
					players = append(players, strconv.Itoa(i))
					ratings[players[i]] = r.IntN(2000) // party members may be the best or the worst in the lobby
				}
				var parties []partymgr.PartyGroup
				next := 0
				for _, n := range tc.sizes {
					members := players[next : next+n]
					parties = append(parties, partymgr.PartyGroup{Leader: members[n-1], Members: members})
					next += n
				}
				d, err := NewDraft(players, 2, tc.perTeam, ratings, parties, DraftConfig{PickingType: pickType, AlternatingMinPlayers: 8, AutoAssignParties: true}, r)
				if err != nil {
					t.Fatalf("%v: %v", tc, err)
				}
				withParty := 0
				for _, team := range d.Teams {
					if i := slices.IndexFunc(parties, func(p partymgr.PartyGroup) bool { return slices.Contains(p.Members, team[0]) }); i >= 0 {
						withParty++
						if team[0] != parties[i].Leader {
							t.Fatalf("%v seed %d: captain %s is not the party leader", tc, seed, team[0])
						}
					}
				}
				if want := min(2, len(tc.sizes)); withParty != want {
					t.Fatalf("%v seed %d: %d party captains, want %d", tc, seed, withParty, want)
				}
				if len(tc.sizes) == 1 && len(d.Teams[0]) != 1 {
					t.Fatalf("%v seed %d: the party's captain picks first: %v", tc, seed, d.Teams)
				}
				if len(tc.sizes) > 1 && d.teamElo(0) > d.teamElo(1) {
					t.Fatalf("%v seed %d: two parties, want the weaker team first: %v", tc, seed, d.Teams)
				}
				for turn := d.Turn(); turn >= 0; turn = d.Turn() {
					if _, _, err := d.AutoPick(); err != nil {
						t.Fatalf("%v seed %d: %v", tc, seed, err)
					}
				}
				for _, p := range parties {
					team := slices.IndexFunc(d.Teams, func(tm []string) bool { return slices.Contains(tm, p.Members[0]) })
					for _, id := range p.Members {
						if !slices.Contains(d.Teams[team], id) {
							t.Fatalf("%v seed %d: party split: %v", tc, seed, d.Teams)
						}
					}
				}
				if len(d.Teams[0]) != tc.perTeam || len(d.Teams[1]) != tc.perTeam {
					t.Fatalf("%v seed %d: uneven teams %v", tc, seed, d.Teams)
				}
			}
		}
	}
}
