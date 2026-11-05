package pagination

import (
	"strconv"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func buttons(t *testing.T, page, total int) []*discordgo.Button {
	t.Helper()
	rows := BuildPageRow(page, total, func(p int) string { return "go|" + strconv.Itoa(p) }, func(slot string) string { return "off-" + slot })
	if rows == nil {
		return nil
	}
	var out []*discordgo.Button
	for _, c := range rows[0].(*discordgo.ActionsRow).Components {
		out = append(out, c.(*discordgo.Button))
	}
	return out
}

func TestPageRowLayout(t *testing.T) {
	if buttons(t, 1, 1) != nil {
		t.Fatal("one page needs no buttons")
	}
	// Fewer than 5 pages: Back, current page, Next.
	b := buttons(t, 2, 3)
	if len(b) != 3 || b[0].Label != "Back" || b[1].Label != "2/3" || b[2].Label != "Next" || b[0].CustomID != "go|1" || b[2].CustomID != "go|3" {
		t.Fatalf("3 pages: %+v", b)
	}
	// 12 pages, on page 7: Back 5, Back, 7/12, Next, Next 5 all pressable, jumping 5 pages.
	b = buttons(t, 7, 12)
	want := []string{"Back 5", "Back", "7/12", "Next", "Next 5"}
	ids := []string{"go|2", "go|6", "off-page", "go|8", "go|12"}
	for n := range want {
		if b[n].Label != want[n] || b[n].CustomID != ids[n] {
			t.Errorf("button %d is %q %q, want %q %q", n, b[n].Label, b[n].CustomID, want[n], ids[n])
		}
	}
	if b[2].Disabled != true {
		t.Error("the current page button must not be pressable")
	}
}

func TestPageRowEndsAreInertAndIDsStayUnique(t *testing.T) {
	for _, c := range [][2]int{{1, 12}, {3, 12}, {12, 12}, {10, 12}, {1, 5}, {5, 5}, {1, 2}, {2, 2}} {
		b := buttons(t, c[0], c[1])
		seen := map[string]bool{}
		for _, btn := range b {
			if seen[btn.CustomID] {
				t.Errorf("page %d of %d: two buttons share the id %q (Discord would refuse the message)", c[0], c[1], btn.CustomID)
			}
			seen[btn.CustomID] = true
		}
	}
	b := buttons(t, 1, 12)
	if !b[0].Disabled || !b[1].Disabled || b[3].Disabled || b[4].Disabled {
		t.Errorf("on the first page Back 5 and Back are inert, Next and Next 5 are not: %+v", b)
	}
	b = buttons(t, 12, 12)
	if b[0].Disabled || b[1].Disabled || !b[3].Disabled || !b[4].Disabled {
		t.Errorf("on the last page Next and Next 5 are inert: %+v", b)
	}
}
