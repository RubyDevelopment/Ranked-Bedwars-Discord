package helpers

import (
	"testing"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

func overwriteOf(out []*discordgo.PermissionOverwrite, id string) *discordgo.PermissionOverwrite {
	for _, o := range out {
		if o.ID == id {
			return o
		}
	}
	return nil
}

func TestGameChannelAccess(t *testing.T) {
	g := Game{GuildID: "guild", Players: []GamePlayer{{UserID: "p1"}}}
	s := &discordgo.Session{}

	// Voice: seen by a role but only the players can join.
	cfg := types.GameChannelsConfig{VoiceAccess: &types.ChannelAccess{View: types.ViewPlayers, ViewRoleIDs: []string{"watch"}}, ExtraRoleIDs: []string{"staff"}}
	out := voiceOverwrites(s, g, cfg)
	if o := overwriteOf(out, "guild"); o.Deny&discordgo.PermissionViewChannel == 0 {
		t.Error("@everyone should not see the channel")
	}
	if o := overwriteOf(out, "watch"); o.Allow&discordgo.PermissionViewChannel == 0 || o.Deny&discordgo.PermissionVoiceConnect == 0 {
		t.Errorf("the watching role should see but not join: %+v", o)
	}
	if o := overwriteOf(out, "p1"); o.Allow&discordgo.PermissionVoiceConnect == 0 {
		t.Error("a player should be able to join")
	}
	if o := overwriteOf(out, "staff"); o.Allow&discordgo.PermissionVoiceConnect == 0 {
		t.Error("an extra role keeps full access")
	}

	// Chat: everyone can read, only the players write.
	out = gameOverwrites(s, g, types.GameChannelsConfig{TextAccess: &types.ChannelAccess{View: types.ViewEveryone}})
	if o := overwriteOf(out, "guild"); o.Allow&discordgo.PermissionViewChannel == 0 || o.Deny&discordgo.PermissionSendMessages == 0 {
		t.Errorf("@everyone should read but not write: %+v", o)
	}

	// Viewers who can use it: nothing is denied.
	out = voiceOverwrites(s, g, types.GameChannelsConfig{VoiceAccess: &types.ChannelAccess{View: types.ViewEveryone, Use: types.UseViewers}})
	if o := overwriteOf(out, "guild"); o.Deny != 0 {
		t.Errorf("open voice should deny nothing: %+v", o)
	}

	// Defaults: the chat is players only; the older voice roles still apply.
	out = gameOverwrites(s, g, types.GameChannelsConfig{})
	if o := overwriteOf(out, "guild"); o.Deny&discordgo.PermissionViewChannel == 0 || o.Allow != 0 {
		t.Errorf("default chat should be hidden: %+v", o)
	}
	out = voiceOverwrites(s, g, types.GameChannelsConfig{VoiceVisibleRoleIDs: []string{"old"}})
	if o := overwriteOf(out, "old"); o == nil || o.Deny&discordgo.PermissionVoiceConnect == 0 {
		t.Errorf("the older voice-visible roles should still see but not join: %+v", o)
	}
}
