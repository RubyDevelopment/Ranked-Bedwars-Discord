package staff

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"30m": 30 * time.Minute, "2h": 2 * time.Hour, "3d": 72 * time.Hour, "1w": 168 * time.Hour,
		"1d12h": 36 * time.Hour, "1d 12h": 36 * time.Hour, " 90M ": 90 * time.Minute, "2H30m": 150 * time.Minute,
	} {
		if got, ok := parseDuration(in); !ok || got != want {
			t.Errorf("parseDuration(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "soon", "5", "5x", "2 days", "h", "1d,2h", "99999999999999999999m"} {
		if _, ok := parseDuration(in); ok {
			t.Errorf("parseDuration(%q) should be refused", in)
		}
	}
}
