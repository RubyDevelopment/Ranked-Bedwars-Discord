package helpers

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

// Discord refuses a message whose components share a custom id, so every page of every history length has to build a
// row of distinct ids (the jump buttons used to clamp to the same page as Back/Next at either end).
func TestHistoryPageComponentsHaveUniqueCustomIDs(t *testing.T) {
	for total := 2; total <= 30; total++ {
		for page := 1; page <= total; page++ {
			rows := BuildHistoryPageComponents(HistoryPageGames, "123", page, total, []string{"7d", "1"})
			if len(rows) != 1 {
				t.Fatalf("total %d page %d: got %d rows", total, page, len(rows))
			}
			seen := map[string]bool{}
			for _, c := range rows[0].(*discordgo.ActionsRow).Components {
				b := c.(*discordgo.Button)
				if seen[b.CustomID] {
					t.Fatalf("total %d page %d: custom id %q is used twice", total, page, b.CustomID)
				}
				seen[b.CustomID] = true
				if b.Disabled {
					continue
				}
				if _, _, _, _, ok := decodeHistoryPageCustomID(b.CustomID); !ok {
					t.Fatalf("total %d page %d: enabled button %q does not decode", total, page, b.CustomID)
				}
			}
		}
	}
}

func TestInertHistoryButtonsAreNotPageClicks(t *testing.T) {
	if _, _, _, _, ok := decodeHistoryPageCustomID(historyPagePrefix + "off-back5"); ok {
		t.Fatal("an inert button id must not decode as a page click")
	}
}
