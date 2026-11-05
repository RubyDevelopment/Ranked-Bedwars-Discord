package match

import "testing"

func TestSubmitFlowDefaultsAndUnknownModes(t *testing.T) {
	if got := (SubmitFlow{}).normalized(); got.Mode != SubmitVerify || got.MinPlayersPercent != 50 {
		t.Errorf("empty -> %+v", got)
	}
	if got := (SubmitFlow{Mode: "yolo", MinPlayersPercent: 400}).normalized(); got.Mode != SubmitVerify || got.MinPlayersPercent != 50 {
		t.Errorf("unknown -> %+v", got)
	}
	if got := (SubmitFlow{Mode: SubmitAuto, MinPlayersPercent: 75}).normalized(); got.Mode != SubmitAuto || got.MinPlayersPercent != 75 {
		t.Errorf("auto -> %+v", got)
	}
	if DefaultFlows().Submit.Mode != SubmitVerify {
		t.Error("default mode")
	}
}
