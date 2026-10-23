package helpers

import (
	"testing"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

func TestCommandAccessRecognizesEveryoneRole(t *testing.T) {
	const guildID = "guild-id"
	member := &discordgo.Member{Roles: []string{"member-role"}}

	tests := []struct {
		name    string
		allowed []string
		denied  []string
		want    bool
	}{
		{name: "everyone allowed role", allowed: []string{guildID}, want: true},
		{name: "other allowed role", allowed: []string{"staff-role"}, want: false},
		{name: "everyone denied role", denied: []string{guildID}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := types.GuildSettings{
				SlashEnabled:  true,
				PrefixEnabled: true,
				Commands: types.GuildCommandsMap{
					"test": {
						AllowedRoles: test.allowed,
						DeniedRoles:  test.denied,
					},
				},
			}

			slash := CheckSlashAccess(settings, guildID, "test", "", "", member, false)
			if slash.Allowed != test.want {
				t.Errorf("CheckSlashAccess().Allowed = %t, want %t", slash.Allowed, test.want)
			}

			prefix := CheckPrefixAccess(settings, guildID, "test", "", "", member, false)
			if prefix.Allowed != test.want {
				t.Errorf("CheckPrefixAccess().Allowed = %t, want %t", prefix.Allowed, test.want)
			}
		})
	}
}
