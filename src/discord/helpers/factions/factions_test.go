package factions

import "testing"

func TestParseConfig(t *testing.T) {
	// Nothing saved: the defaults, no factions.
	st, list := ParseConfig(nil)
	if st != DefaultSettings() || len(list) != 0 {
		t.Fatalf("empty config gave %+v %v", st, list)
	}

	panels := []byte(`{
	  "faction_settings": {"allow_leave": true, "win_points": 5},
	  "factions": [
	    {"id":"a","name":"Red","emoji":"🔴","color":"#ff0000","role_id":"111"},
	    {"id":"b","name":"No role yet","role_id":""},
	    {"id":"c","name":"","role_id":"333"}
	  ]}`)
	st, list = ParseConfig(panels)
	if !st.AllowLeave || st.WinPoints != 5 {
		t.Errorf("saved settings lost: %+v", st)
	}
	if !st.Enabled || !st.RequireRegistered || st.GamePoints != 1 {
		t.Errorf("settings that were not saved lost their defaults: %+v", st)
	}
	if len(list) != 1 || list[0].Name != "Red" {
		t.Fatalf("only a faction with a name and a role can be joined, got %+v", list)
	}
	if list[0].ColorInt() != 0xff0000 || (Faction{}).ColorInt() != 0x99AAB5 {
		t.Errorf("colours: %x %x", list[0].ColorInt(), (Faction{}).ColorInt())
	}
	if list[0].Label() != "🔴 Red" {
		t.Errorf("label %q", list[0].Label())
	}
}

func TestFindAndOf(t *testing.T) {
	list := []Faction{{ID: "a", Name: "Red", RoleID: "r1"}, {ID: "b", Name: "Blue", RoleID: "r2"}}
	if f := Find(list, " blue "); f == nil || f.ID != "b" {
		t.Errorf("Find by name: %+v", f)
	}
	if Find(list, "green") != nil {
		t.Error("found a faction that is not there")
	}
	if f := Of([]string{"x", "r2"}, list); f == nil || f.ID != "b" {
		t.Errorf("Of: %+v", f)
	}
	if Of([]string{"x"}, list) != nil {
		t.Error("a member with no faction role is in one")
	}
}

// Points are wins and games of the members who hold the role, only those who played count as active, and ties break on wins then name.
func TestTally(t *testing.T) {
	list := []Faction{{ID: "red", Name: "Red"}, {ID: "blue", Name: "Blue"}, {ID: "green", Name: "Green"}}
	members := map[string][]string{"red": {"a", "b", "idle"}, "blue": {"c"}, "green": {"d"}}
	stats := map[string]MemberStats{
		"a": {Elo: 1600, Wins: 10, Games: 15}, "b": {Elo: 1400, Wins: 2, Games: 5},
		"idle": {}, // registered but never played this season
		"c":    {Elo: 1000, Wins: 4, Games: 24},
		"d":    {Elo: 900, Wins: 4, Games: 24},
		"zzz":  {Elo: 3000, Wins: 99, Games: 99}, // not in any faction
	}
	got := Tally(list, members, stats, Settings{WinPoints: 3, GamePoints: 1})
	if got[0].Faction.ID != "red" || got[0].Points != 12*3+20 || got[0].Members != 3 || got[0].Active != 2 || got[0].AvgElo != 1500 {
		t.Fatalf("red: %+v", got[0])
	}
	// Blue and Green have the same points and wins: the name decides.
	if got[1].Faction.Name != "Blue" || got[2].Faction.Name != "Green" || got[1].Points != got[2].Points {
		t.Fatalf("tie order: %+v %+v", got[1], got[2])
	}
	for i, s := range got {
		if s.Position != i+1 {
			t.Errorf("position %d is %d", i+1, s.Position)
		}
	}
}
