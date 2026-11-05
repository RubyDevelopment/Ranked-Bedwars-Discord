package queue

import (
	"strings"
	"testing"
	"time"
)

// Games made at 20:00 UTC every day (two a day) make 8 PM the server's busy hour: an empty queue says so, and in the quiet hours says
// when it usually picks up.
func TestEmptyQueueSaysWhenGamesUsuallyHappen(t *testing.T) {
	now := time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC) // a Thursday, 3 AM
	var times []time.Time
	for d := 1; d <= 27; d++ {
		day := now.AddDate(0, 0, -d)
		for _, minute := range []int{5, 40} {
			times = append(times, time.Date(day.Year(), day.Month(), day.Day(), 20, minute, 0, 0, time.UTC))
		}
	}
	p := buildActivityProfile(times, now)

	quiet := p.status(now)
	if !strings.Contains(quiet, "Quiet right now") || !strings.Contains(quiet, "8 PM UTC") || !strings.Contains(quiet, "~2 games/hr") {
		t.Fatalf("quiet hours read %q", quiet)
	}
	busy := p.status(time.Date(2026, 11, 5, 20, 30, 0, 0, time.UTC))
	if !strings.Contains(busy, "Usually busy now") || !strings.Contains(busy, "~2 games/hr") {
		t.Fatalf("the busy hour reads %q", busy)
	}
}

// With almost no games there is nothing reliable to say, so the channel shows no status, as before.
func TestTooFewGamesSayNothing(t *testing.T) {
	now := time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)
	p := buildActivityProfile([]time.Time{now.Add(-48 * time.Hour), now.Add(-72 * time.Hour)}, now)
	if got := p.status(now); got != "" {
		t.Fatalf("status %q", got)
	}
}

// Games older than the window and games in the future are not counted.
func TestOldGamesAreIgnored(t *testing.T) {
	now := time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)
	var times []time.Time
	for i := 0; i < 50; i++ {
		times = append(times, now.Add(-60*24*time.Hour), now.Add(48*time.Hour))
	}
	if p := buildActivityProfile(times, now); p.games != 0 {
		t.Fatalf("%d games counted", p.games)
	}
}

func TestClockAndRate(t *testing.T) {
	for in, want := range map[int]string{0: "12 AM UTC", 9: "9 AM UTC", 12: "12 PM UTC", 20: "8 PM UTC"} {
		if got := clock(time.Date(2026, 1, 1, in, 0, 0, 0, time.UTC)); got != want {
			t.Errorf("clock(%d) = %q, want %q", in, got, want)
		}
	}
	if rate(2) != "2" || rate(1.5) != "1.5" || rate(12.4) != "12" {
		t.Errorf("rate: %s %s %s", rate(2), rate(1.5), rate(12.4))
	}
}
