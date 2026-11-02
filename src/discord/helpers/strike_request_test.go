package helpers

import "testing"

func TestStrikeVotesNeededIsThreeQuartersRoundedUp(t *testing.T) {
	for voters, want := range map[int]int{0: 1, 1: 1, 2: 2, 3: 3, 4: 3, 5: 4, 7: 6, 8: 6} {
		if got := strikeVotesNeeded(voters); got != want {
			t.Errorf("%d voters: need %d agree votes, got %d", voters, want, got)
		}
	}
}

func TestSkinNameDropsTheEloAndFallsBackToTheDefaultSkin(t *testing.T) {
	for in, want := range map[string]string{
		"[0] IBRAHIMBOY575": "IBRAHIMBOY575",
		"[215] ShubDee":     "ShubDee",
		"Gqlry":             "Gqlry",
		"bad name!":         "MHF_Steve",
		"":                  "MHF_Steve",
	} {
		if got := skinName(in); got != want {
			t.Errorf("skinName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := HeadURL("[0] HikeTheOg"); got != "https://mc-heads.net/avatar/HikeTheOg" {
		t.Errorf("head url = %s", got)
	}
}
