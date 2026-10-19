package helpers

import (
	"testing"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"

	"ranked-bedwars/src/types"
)

func TestPlanDenial(t *testing.T) {
	t.Setenv("DEV_MODE", "")
	t.Setenv("PLAN_ENFORCEMENT", "")
	now := time.Now()
	tier := "automated"
	past, soon := now.AddDate(0, 0, -10), now.AddDate(0, 0, 5)
	guild := func(tier *string, expires *time.Time, enabled bool) GuildContext {
		return GuildContext{Registered: true, Guild: &models.GuildsModel{Tier: tier, TierExpiresAt: expires, IsEnabled: enabled}}
	}
	stats, setup := &types.Command{Name: "player_stats"}, &types.Command{Name: "setup"}

	cases := []struct {
		name string
		gc   GuildContext
		cmd  *types.Command
		deny bool
	}{
		{"paid and current", guild(&tier, &soon, true), stats, false},
		{"permanent plan", guild(&tier, nil, true), stats, false},
		{"lapsed", guild(&tier, &past, true), stats, true},
		{"never had a plan", guild(nil, nil, true), stats, true},
		{"disabled by the developer", guild(&tier, &soon, false), stats, true},
		{"setup stays usable without a plan", guild(nil, nil, true), setup, false},
		{"guild failed to load: fail open", GuildContext{Registered: true}, stats, false},
	}
	for _, c := range cases {
		if got := planDenial(c.gc, c.cmd, now) != nil; got != c.deny {
			t.Errorf("%s: denied = %v, want %v", c.name, got, c.deny)
		}
	}

	t.Setenv("PLAN_ENFORCEMENT", "off")
	if planDenial(guild(nil, nil, true), stats, now) != nil {
		t.Error("PLAN_ENFORCEMENT=off should let every server through")
	}
}
