package helpers

import "testing"

func TestFillVarsBraces(t *testing.T) {
	vars := map[string]string{"user.name": "Wiggels"}
	for in, want := range map[string]string{
		"hi {user.name}":         "hi Wiggels",
		"hi {{user.name}}":       "hi Wiggels",
		"{nope} {{ user.name }}": "{nope} Wiggels",
	} {
		if got := FillVars(in, vars); got != want {
			t.Errorf("FillVars(%q) = %q, want %q", in, got, want)
		}
	}
	// banner text: a known name fills, an unknown {name} stays, an unknown {{name}} is blanked as before
	if got := (Vars{"a.b": "1"}).fill("{a.b}|{{a.b}}|{x}|{{x}}"); got != "1|1|{x}|" {
		t.Errorf("Vars.fill = %q", got)
	}
}
