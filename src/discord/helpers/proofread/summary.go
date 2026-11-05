package proofread

import (
	"context"
	"fmt"
	"strings"
)

// Check downloads the screenshot, reads its chat and analyses it. It returns ErrNoOCR when this machine cannot read images.
func Check(ctx context.Context, imageURL string, profile Profile, roster []Member, claimed string) (Result, error) {
	if !Available() {
		return Result{}, ErrNoOCR
	}
	data, err := Download(ctx, imageURL)
	if err != nil {
		return Result{}, err
	}
	lines, err := ReadLines(ctx, data, func(lines []string) bool { return Analyze(lines, profile, roster, claimed).RulesHit >= 2 })
	if err != nil {
		return Result{}, err
	}
	return Analyze(lines, profile, roster, claimed), nil
}

// Summary is the auto-check as the lines staff read under a submitted result.
func (r Result) Summary(playerCount int) string {
	var b strings.Builder
	switch {
	case r.Winner != "":
		fmt.Fprintf(&b, "**Winner in chat:** %s (%s)\n", r.Winner, r.WinnerBy)
	default:
		b.WriteString("**Winner in chat:** not clear from the screenshot\n")
	}
	if r.Mismatch != "" {
		fmt.Fprintf(&b, "⚠️ **Mismatch:** %s\n", r.Mismatch)
	}
	if len(r.BedBreaks) > 0 {
		parts := make([]string, 0, 3)
		for _, c := range r.BedBreaks[:min(3, len(r.BedBreaks))] {
			parts = append(parts, fmt.Sprintf("%s (%s) ×%d", c.Name, c.Team, c.N))
		}
		fmt.Fprintf(&b, "**Beds broken:** %s\n", strings.Join(parts, ", "))
	}
	if r.MVP.Name != "" {
		fmt.Fprintf(&b, "**MVP:** %s (%s) · %s\n", r.MVP.Name, r.MVP.Team, r.MVPBy)
	}
	if len(r.Seen) > 0 {
		parts := make([]string, len(r.Seen))
		for i, m := range r.Seen {
			parts[i] = fmt.Sprintf("%s (%s)", m.Name, m.Team)
		}
		fmt.Fprintf(&b, "**Players confirmed (%d of %d):** %s", len(r.Seen), playerCount, strings.Join(parts, ", "))
	}
	return strings.TrimSpace(b.String())
}
