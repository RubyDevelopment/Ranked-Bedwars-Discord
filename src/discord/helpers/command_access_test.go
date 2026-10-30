package helpers

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestStaffCommandAllowedRolesReplaceTheStaffGate(t *testing.T) {
	gate := roleGate{staff: true, staffID: "staff"}
	member := func(roles ...string) *discordgo.Member { return &discordgo.Member{Roles: roles} }
	cases := []struct {
		name            string
		allowed, denied []string
		roles           []string
		want            bool
	}{
		{"staff role, no allowed list", nil, nil, []string{"staff"}, true},
		{"no staff role, no allowed list", nil, nil, []string{"other"}, false},
		{"allowed role without the staff role", []string{"mod"}, nil, []string{"mod"}, true},
		{"staff role is not enough once a list is set", []string{"mod"}, nil, []string{"staff"}, false},
		{"denied wins over allowed", []string{"mod"}, []string{"banned"}, []string{"mod", "banned"}, false},
	}
	for _, c := range cases {
		if got := checkRoleAccess(c.allowed, c.denied, "guild", member(c.roles...), false, gate).Allowed; got != c.want {
			t.Errorf("%s: allowed = %v, want %v", c.name, got, c.want)
		}
	}
}
