package helpers

import (
	"testing"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database-Go/src/models"

	"ranked-bedwars/src/types"
)

func resetPlanState(t *testing.T) {
	t.Helper()
	t.Setenv("DEV_MODE", "")
	t.Setenv("PLAN_ENFORCEMENT", "")
	t.Setenv("PLAN_GRANDFATHER_BEFORE", "")
	planMu.Lock()
	planCache = map[string]lastKnown{}
	planMu.Unlock()
}

func TestPlanDenial(t *testing.T) {
	resetPlanState(t)
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
		{"guild failed to load and never seen: fail open", GuildContext{Registered: true}, stats, false},
	}
	for i, c := range cases {
		if got := planDenial(c.gc, "g"+string(rune('a'+i)), c.cmd, now) != nil; got != c.deny {
			t.Errorf("%s: denied = %v, want %v", c.name, got, c.deny)
		}
	}

	t.Setenv("PLAN_ENFORCEMENT", "off")
	if planDenial(guild(nil, nil, true), "off", stats, now) != nil {
		t.Error("PLAN_ENFORCEMENT=off should let every server through")
	}
}

func TestPlanDenialKeepsLapsedServersLockedDuringAnOutage(t *testing.T) {
	resetPlanState(t)
	now := time.Now()
	stats := &types.Command{Name: "player_stats"}
	past := now.AddDate(0, 0, -10)

	// The server is seen lapsed, then the Database becomes unreachable (Guild nil): it must stay locked.
	lapsed := GuildContext{Registered: true, Guild: &models.GuildsModel{TierExpiresAt: &past, IsEnabled: true}}
	if planDenial(lapsed, "lapsed", stats, now) == nil {
		t.Fatal("a lapsed server should be denied")
	}
	if planDenial(GuildContext{Registered: true}, "lapsed", stats, now) == nil {
		t.Error("during an outage a server last seen lapsed must stay locked")
	}

	// A paying server stays usable through an outage.
	tier := "core"
	paid := GuildContext{Registered: true, Guild: &models.GuildsModel{Tier: &tier, IsEnabled: true}}
	if planDenial(paid, "paid", stats, now) != nil {
		t.Fatal("a paid server should be allowed")
	}
	if planDenial(GuildContext{Registered: true}, "paid", stats, now) != nil {
		t.Error("during an outage a paying server must stay usable")
	}
}

func TestPlanGrandfathering(t *testing.T) {
	resetPlanState(t)
	now := time.Now()
	stats := &types.Command{Name: "player_stats"}
	old := models.GuildsModel{IsEnabled: true}
	old.CreatedAt = now.AddDate(0, -2, 0)
	fresh := models.GuildsModel{IsEnabled: true}
	fresh.CreatedAt = now.AddDate(0, 0, -1)

	if planDenial(GuildContext{Registered: true, Guild: &old}, "old", stats, now) == nil {
		t.Error("without a grandfather date an unplanned old server is denied")
	}
	t.Setenv("PLAN_GRANDFATHER_BEFORE", now.AddDate(0, -1, 0).Format("2006-01-02"))
	if planDenial(GuildContext{Registered: true, Guild: &old}, "old2", stats, now) != nil {
		t.Error("a server older than the grandfather date should be allowed")
	}
	if planDenial(GuildContext{Registered: true, Guild: &fresh}, "fresh", stats, now) == nil {
		t.Error("a server newer than the grandfather date still needs a plan")
	}
	expired := now.AddDate(0, 0, -10)
	lapsed := models.GuildsModel{IsEnabled: true, TierExpiresAt: &expired}
	lapsed.CreatedAt = now.AddDate(0, -2, 0)
	if planDenial(GuildContext{Registered: true, Guild: &lapsed}, "lapsed2", stats, now) == nil {
		t.Error("a server that had a plan and let it lapse is not grandfathered")
	}
}
