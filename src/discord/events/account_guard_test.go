package events

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func yes() *bool { v := true; return &v }
func no() *bool  { v := false; return &v }

// A message from a young account with a picture, in a text channel, that has written nothing before.
func suspicious() guardInput {
	return guardInput{
		Kinds:          map[string]bool{"image": true, "attachment": true},
		ChannelID:      "c1",
		ChannelType:    "text",
		Prior:          0,
		AccountAge:     2 * 24 * time.Hour,
		MemberAge:      3 * time.Hour,
		MemberAgeKnown: true,
	}
}

func TestGuardMatches(t *testing.T) {
	cases := []struct {
		name  string
		guard EventGuard
		in    func(*guardInput)
		want  bool
	}{
		{"no conditions: every message", EventGuard{}, nil, true},
		{"image in a text channel", EventGuard{ContentKinds: []string{"image"}, ChannelTypes: []string{"text"}}, nil, true},
		{"image in a voice channel's chat only", EventGuard{ContentKinds: []string{"image"}, ChannelTypes: []string{"voice"}}, nil, false},
		{"image in a voice channel's chat only (it is voice)", EventGuard{ContentKinds: []string{"image"}, ChannelTypes: []string{"voice"}}, func(in *guardInput) { in.ChannelType = "voice" }, true},
		{"text rule ignores a picture-only message", EventGuard{ContentKinds: []string{"text"}}, nil, false},
		{"any of the kinds is enough", EventGuard{ContentKinds: []string{"text", "image"}}, nil, true},
		{"a link", EventGuard{ContentKinds: []string{"link"}}, func(in *guardInput) { in.Kinds = map[string]bool{"text": true, "link": true} }, true},

		{"fewer than 5 messages: has 0", EventGuard{MaxMessages: 5}, nil, true},
		{"fewer than 5 messages: has 4", EventGuard{MaxMessages: 5}, func(in *guardInput) { in.Prior = 4 }, true},
		{"fewer than 5 messages: has 5", EventGuard{MaxMessages: 5}, func(in *guardInput) { in.Prior = 5 }, false},
		{"0 means no message limit", EventGuard{MaxMessages: 0}, func(in *guardInput) { in.Prior = 9999 }, true},

		{"account younger than 7 days", EventGuard{MaxAccountDays: 7}, nil, true},
		{"account older than 7 days", EventGuard{MaxAccountDays: 7}, func(in *guardInput) { in.AccountAge = 30 * 24 * time.Hour }, false},
		{"joined within 24 hours", EventGuard{MaxMemberHours: 24}, nil, true},
		{"joined long ago", EventGuard{MaxMemberHours: 24}, func(in *guardInput) { in.MemberAge = 100 * time.Hour }, false},
		{"join time unknown: not acted on", EventGuard{MaxMemberHours: 24}, func(in *guardInput) { in.MemberAgeKnown = false }, false},

		{"every condition together", EventGuard{ContentKinds: []string{"image"}, ChannelTypes: []string{"text"}, MaxMessages: 3, MaxAccountDays: 7, MaxMemberHours: 24}, nil, true},
		{"one failing condition spares them", EventGuard{ContentKinds: []string{"image"}, MaxMessages: 3, MaxAccountDays: 1}, nil, false},

		{"excluded channel", EventGuard{ExcludeChannelIDs: []string{"c1"}}, nil, false},
		{"another channel is not excluded", EventGuard{ExcludeChannelIDs: []string{"c9"}}, nil, true},
		{"trusted role", EventGuard{SkipRoleIDs: []string{"r1"}}, func(in *guardInput) { in.RoleIDs = []string{"r2", "r1"} }, false},
		{"other role", EventGuard{SkipRoleIDs: []string{"r1"}}, func(in *guardInput) { in.RoleIDs = []string{"r2"} }, true},

		{"moderators are skipped by default", EventGuard{}, func(in *guardInput) { in.Moderator = true }, false},
		{"moderators skipped when asked", EventGuard{SkipModerators: yes()}, func(in *guardInput) { in.Moderator = true }, false},
		{"moderators can be included", EventGuard{SkipModerators: no()}, func(in *guardInput) { in.Moderator = true }, true},

		{"registered players skipped", EventGuard{SkipRegistered: true}, func(in *guardInput) { in.Registered = func() bool { return true } }, false},
		{"unregistered players are not", EventGuard{SkipRegistered: true}, func(in *guardInput) { in.Registered = func() bool { return false } }, true},
		{"registration ignored when not asked", EventGuard{SkipRegistered: false}, func(in *guardInput) { in.Registered = func() bool { return true } }, true},
	}
	for _, tc := range cases {
		in := suspicious()
		if tc.in != nil {
			tc.in(&in)
		}
		if got := tc.guard.matches(in); got != tc.want {
			t.Errorf("%s: matches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The database is only asked once everything cheaper has already passed.
func TestGuardAsksTheDatabaseLast(t *testing.T) {
	asked := 0
	in := suspicious()
	in.Registered = func() bool { asked++; return false }
	(&EventGuard{ContentKinds: []string{"text"}, SkipRegistered: true}).matches(in) // fails on the kind
	if asked != 0 {
		t.Errorf("the database was asked %d times for a message that already failed a cheaper condition", asked)
	}
	(&EventGuard{ContentKinds: []string{"image"}, SkipRegistered: true}).matches(in)
	if asked != 1 {
		t.Errorf("asked %d times, want 1", asked)
	}
}

func TestMessageKinds(t *testing.T) {
	cases := []struct {
		name string
		msg  *discordgo.Message
		want []string
	}{
		{"nothing", &discordgo.Message{}, nil},
		{"plain text", &discordgo.Message{Content: "hello"}, []string{"text"}},
		{"only spaces is not text", &discordgo.Message{Content: "   "}, nil},
		{"a link", &discordgo.Message{Content: "free crypto https://scam.example/claim"}, []string{"link", "text"}},
		{"an invite", &discordgo.Message{Content: "discord.gg/abcdef"}, []string{"link", "text"}},
		{"an attached picture by type", &discordgo.Message{Attachments: []*discordgo.MessageAttachment{{Filename: "a", ContentType: "image/png"}}}, []string{"attachment", "image"}},
		{"an attached picture by name", &discordgo.Message{Attachments: []*discordgo.MessageAttachment{{Filename: "WIN.JPG"}}}, []string{"attachment", "image"}},
		{"an attached picture by size", &discordgo.Message{Attachments: []*discordgo.MessageAttachment{{Filename: "x", Width: 800}}}, []string{"attachment", "image"}},
		{"a file that is not a picture", &discordgo.Message{Attachments: []*discordgo.MessageAttachment{{Filename: "run.exe", ContentType: "application/octet-stream"}}}, []string{"attachment"}},
		{"an embedded image", &discordgo.Message{Embeds: []*discordgo.MessageEmbed{{Type: discordgo.EmbedTypeImage}}}, []string{"image"}},
		{"an embed without a picture", &discordgo.Message{Embeds: []*discordgo.MessageEmbed{{Type: discordgo.EmbedTypeRich, Title: "x"}}}, nil},
	}
	for _, tc := range cases {
		var got []string
		for kind := range messageKinds(tc.msg) {
			got = append(got, kind)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: kinds %v, want %v", tc.name, got, tc.want)
		}
	}
	if len(messageKinds(nil)) != 0 {
		t.Error("a nil message holds nothing")
	}
}

func TestChannelTypeName(t *testing.T) {
	forum := &discordgo.Channel{Type: discordgo.ChannelTypeGuildForum}
	text := &discordgo.Channel{Type: discordgo.ChannelTypeGuildText}
	cases := []struct {
		ch, parent *discordgo.Channel
		want       string
	}{
		{&discordgo.Channel{Type: discordgo.ChannelTypeGuildText}, nil, "text"},
		{&discordgo.Channel{Type: discordgo.ChannelTypeGuildVoice}, nil, "voice"},
		{&discordgo.Channel{Type: discordgo.ChannelTypeGuildStageVoice}, nil, "stage"},
		{&discordgo.Channel{Type: discordgo.ChannelTypeGuildNews}, nil, "announcement"},
		{forum, nil, "forum"},
		{&discordgo.Channel{Type: discordgo.ChannelTypeGuildPublicThread}, forum, "forum"},
		{&discordgo.Channel{Type: discordgo.ChannelTypeGuildPublicThread}, text, "thread"},
		{&discordgo.Channel{Type: discordgo.ChannelTypeGuildPrivateThread}, nil, "thread"},
	}
	for _, tc := range cases {
		if got := channelTypeName(tc.ch, tc.parent); got != tc.want {
			t.Errorf("type %d: %q, want %q", tc.ch.Type, got, tc.want)
		}
	}
}

// Counts survive a restart, and "before this message" really is before it.
func TestMessageCounterPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "counts.json")
	c := &messageCounter{path: path}
	for want := 0; want < 3; want++ {
		if got := c.bump("g", "u"); got != want {
			t.Fatalf("bump %d returned %d, want the number sent before it (%d)", want+1, got, want)
		}
	}
	if got := c.bump("g", "other"); got != 0 {
		t.Errorf("another member starts at 0, got %d", got)
	}
	if got := c.bump("other-guild", "u"); got != 0 {
		t.Errorf("the same member in another server starts at 0, got %d", got)
	}
	if err := c.flush(); err != nil {
		t.Fatal(err)
	}

	restarted := &messageCounter{path: path}
	if got := restarted.bump("g", "u"); got != 3 {
		t.Errorf("after a restart the member had %d messages, want 3", got)
	}
}

func TestMessageCounterFlushIsQuietWhenNothingChanged(t *testing.T) {
	c := &messageCounter{path: filepath.Join(t.TempDir(), "never", "counts.json")}
	if err := c.flush(); err != nil {
		t.Fatal(err)
	}
}

func TestRecentMessages(t *testing.T) {
	r := &recentLog{by: map[string][]recentMsg{}}
	now := time.Now()
	r.add("g", "u", recentMsg{"c1", "m1", now.Add(-2 * time.Hour)})
	r.add("g", "u", recentMsg{"c2", "m2", now.Add(-30 * time.Minute)})
	r.add("g", "u", recentMsg{"c1", "m3", now.Add(-5 * time.Minute)})
	r.add("g", "u", recentMsg{"c1", "m4", now.Add(-1 * time.Minute)})
	r.add("g", "someone-else", recentMsg{"c1", "x1", now})

	ids := func(list []recentMsg) []string {
		var out []string
		for _, m := range list {
			out = append(out, m.MessageID)
		}
		return out
	}
	if got := ids(r.latest("g", "u", time.Hour, 10, "", now)); !slices.Equal(got, []string{"m4", "m3", "m2"}) {
		t.Errorf("last hour, every channel: %v (newest first, nothing older than an hour)", got)
	}
	if got := ids(r.latest("g", "u", time.Hour, 2, "", now)); !slices.Equal(got, []string{"m4", "m3"}) {
		t.Errorf("limit 2: %v", got)
	}
	if got := ids(r.latest("g", "u", time.Hour, 10, "c1", now)); !slices.Equal(got, []string{"m4", "m3"}) {
		t.Errorf("only channel c1: %v", got)
	}
	if got := r.latest("other-guild", "u", time.Hour, 10, "", now); len(got) != 0 {
		t.Errorf("another server's messages are never mixed in: %v", got)
	}
	r.forget("g", "u", r.latest("g", "u", time.Hour, 10, "", now))
	if got := ids(r.latest("g", "u", 24*time.Hour, 10, "", now)); !slices.Equal(got, []string{"m1"}) {
		t.Errorf("after deleting the recent ones only the old one is left: %v", got)
	}
}

func TestRecentMessagesKeepAtMostAHundred(t *testing.T) {
	r := &recentLog{by: map[string][]recentMsg{}}
	now := time.Now()
	for i := 0; i < 150; i++ {
		r.add("g", "u", recentMsg{"c", "m", now})
	}
	if n := len(r.by["g|u"]); n != recentKeep {
		t.Errorf("%d messages kept, want %d", n, recentKeep)
	}
}

func TestPlanDeletes(t *testing.T) {
	now := time.Now()
	young := now.Add(-time.Hour)
	old := now.Add(-20 * 24 * time.Hour)
	items := []recentMsg{
		{"c1", "a", young}, {"c1", "b", young}, {"c1", "c", young}, // three in one channel: one bulk delete
		{"c2", "d", young},                                          // alone in its channel: deleted one by one
		{"c3", "e", old}, {"c3", "f", old},                          // too old for a bulk delete
	}
	bulk, single := planDeletes(items, now)
	if got := bulk["c1"]; len(got) != 1 || len(got[0]) != 3 {
		t.Errorf("c1 should be one bulk delete of 3, got %v", got)
	}
	if _, ok := bulk["c2"]; ok {
		t.Error("a single message cannot be bulk deleted")
	}
	if _, ok := bulk["c3"]; ok {
		t.Error("messages older than two weeks cannot be bulk deleted")
	}
	singles := map[string]bool{}
	for _, m := range single {
		singles[m.MessageID] = true
	}
	for _, id := range []string{"d", "e", "f"} {
		if !singles[id] {
			t.Errorf("message %s should be deleted one by one", id)
		}
	}
	if len(singles) != 3 {
		t.Errorf("%d singles, want 3: %v", len(singles), singles)
	}
}

func TestPlanDeletesSplitsAtAHundred(t *testing.T) {
	now := time.Now()
	var items []recentMsg
	for i := 0; i < 101; i++ {
		items = append(items, recentMsg{"c", string(rune('a' + i%26)), now})
	}
	bulk, single := planDeletes(items, now)
	if len(bulk["c"]) != 1 || len(bulk["c"][0]) != 100 || len(single) != 1 {
		t.Errorf("101 messages should be a bulk delete of 100 and one single, got %d chunks and %d singles", len(bulk["c"]), len(single))
	}
}
