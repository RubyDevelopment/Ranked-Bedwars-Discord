package tickets

import (
	"strings"
	"testing"

	"ranked-bedwars/src/types"
)

func TestClosedLinesShowTheReasonOnlyWhenOneWasGiven(t *testing.T) {
	ticket := &types.Ticket{ThreadName: "322-x", ClosedBy: "42"}
	if got := strings.Join(TicketClosedLines(ticket, "Guild"), "\n"); strings.Contains(got, "Reason") {
		t.Fatalf("a reason appears without one:\n%s", got)
	}
	ticket.CloseReason = "  Resolved, @everyone thanks  "
	got := strings.Join(TicketClosedLines(ticket, "Guild"), "\n")
	if !strings.Contains(got, "- **Reason:** Resolved, @\u200beveryone thanks") {
		t.Fatalf("reason missing or able to ping:\n%s", got)
	}
}
