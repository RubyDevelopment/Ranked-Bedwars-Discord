package screenshare

import (
	"testing"

	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/types"
)

// The ticket's welcome fields come from {ss_*} placeholders: empty ones must be present (so no raw {…} shows) and image evidence
// must go to the image, not to a link field.
func TestTicketPlaceholders(t *testing.T) {
	if placeholderVars(&types.Ticket{}, &types.InteractivePanel{Ticket: &types.TicketPanelConfig{}}) != nil {
		t.Error("other tickets must not get screenshare placeholders")
	}
	panel := &types.InteractivePanel{Ticket: &types.TicketPanelConfig{Command: Command}}
	ticket := &types.Ticket{ThreadID: "chan-1", OwnerID: "u1", Status: types.TicketStatusOpen}
	if v := placeholderVars(ticket, panel); v["ss_reason"] != "" || v["ss_player"] != "<@u1>" {
		t.Errorf("without a request got %v", v)
	}
	Requests.guilds["g"] = &guildState{reqs: map[int]*types.ScreenshareRequest{1: {
		ID: 1, Kind: types.ScreenshareKindTicket, ChannelID: "chan-1", TargetID: "u1", TargetName: "Steve", RequesterID: "u2",
		Reason: "aim", EvidenceURL: "https://cdn/x.png", EvidenceImage: true, ExpiresAt: 99,
	}}}
	defer delete(Requests.guilds, "g")
	v := placeholderVars(ticket, panel)
	if v["ss_image"] != "https://cdn/x.png" || v["ss_evidence"] != "" || v["ss_reporter"] != "<@u2>" || v["ss_player"] != "<@u1> (`Steve`)" {
		t.Errorf("with a request got %v", v)
	}
	ticket.Status, ticket.ClaimedBy = types.TicketStatusClaimed, "u3"
	if v := placeholderVars(ticket, panel); v["ss_claimed"] != "<@u3>" {
		t.Errorf("claimed ticket got %v", v)
	}
}

func TestEvidenceAndTimestamps(t *testing.T) {
	for link, want := range map[string]string{"https://medal.tv/clips/abc": "https://medal.tv/clips/abc", "<https://youtu.be/x>": "https://youtu.be/x", "not a link": "", "ftp://a.b/c": "", "https://localhost": ""} {
		if got := ParseLink(link); got != want {
			t.Errorf("ParseLink(%q) = %q, want %q", link, got, want)
		}
	}
	if _, problem := RestrictedEvidence(&discordgo.MessageAttachment{URL: "https://cdn/x.png", Filename: "x.png", ContentType: "image/png"}, ""); problem == "" {
		t.Error("an image must not pass as video evidence")
	}
	if u, problem := RestrictedEvidence(&discordgo.MessageAttachment{URL: "https://cdn/x.mp4", Filename: "x.mp4"}, ""); problem != "" || u == "" {
		t.Errorf("a video upload should pass, got %q %q", u, problem)
	}
	if _, problem := RestrictedEvidence(nil, ""); problem == "" {
		t.Error("restricted players need evidence")
	}
	if v, p := NormalizeTimestamps("0:42 - 1:15,  3:05"); p != "" || v != "0:42–1:15, 3:05" {
		t.Errorf("timestamps = %q %q", v, p)
	}
	if _, p := NormalizeTimestamps("later"); p == "" {
		t.Error("timestamps without a time must be refused")
	}
}
