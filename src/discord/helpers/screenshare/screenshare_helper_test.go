package screenshare

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

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
