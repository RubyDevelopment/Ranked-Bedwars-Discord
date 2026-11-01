package helpers

import (
	"strings"
	"testing"

	"ranked-bedwars/src/types"
)

func TestRankVariablesGetTheirColours(t *testing.T) {
	_, _ = rankCache.get("g-rank-test", func() ([]types.Rank, error) {
		return []types.Rank{{RankName: "Diamond", Color: "#38bdf8"}, {RankName: "Gold", Color: "#facc15"}}, nil
	})
	v := withRankStyles("g-rank-test", Vars{"stats.rank": "Diamond", "lb.1.rank": "gold", "t1.p1.rank": "Gold", "t1.p1.rank_before": "Unknown", "player.name": "x"})
	for _, k := range []string{"stats.rank", "lb.1.rank", "t1.p1.rank", "t1.p1.rank_before"} {
		if !strings.HasPrefix(v[k+"_color"], "#") || !strings.HasPrefix(v[k+"_bg"], "rgba(") || !strings.HasSuffix(v[k+"_border"], "0.35)") {
			t.Fatalf("%s: %q %q %q", k, v[k+"_color"], v[k+"_bg"], v[k+"_border"])
		}
	}
	if v["stats.rank_color"] == v["lb.1.rank_color"] {
		t.Fatal("different ranks must get different colours")
	}
	if v["t1.p1.rank_before_color"] != "#9ca3af" {
		t.Fatalf("an unknown rank should be grey, got %q", v["t1.p1.rank_before_color"])
	}
	if _, has := v["player.name_color"]; has {
		t.Fatal("only rank variables get colours")
	}
}
