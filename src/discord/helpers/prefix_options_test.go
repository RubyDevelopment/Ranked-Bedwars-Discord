package helpers

import (
	"strings"
	"testing"
)

func TestPrefixOptionsAreOrderIndependent(t *testing.T) {
	opts := PunishmentOptions("user", "reason", false, false, true)
	want := map[string]string{"user": "699202571289624576", "duration": "3d", "reason": "Inappropriate language(1st)"}
	for _, args := range []string{
		"699202571289624576 3d Inappropriate language(1st)",
		"699202571289624576 Inappropriate language(1st) 3d",
		"3d 699202571289624576 Inappropriate language(1st)",
		"Inappropriate language(1st) 3d 699202571289624576",
	} {
		got := map[string]string{}
		for _, o := range buildPrefixLeafOptions("", opts, strings.Fields(args)) {
			got[o.Name] = o.Value.(string)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%q: %s = %q, want %q", args, k, got[k], v)
			}
		}
	}
}
