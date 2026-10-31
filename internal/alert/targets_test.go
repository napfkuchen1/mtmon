// SPDX-License-Identifier: AGPL-3.0-only

package alert

import "testing"

func TestTargetDownAfterThreeAndRecovery(t *testing.T) {
	e, got := newEngine(t)
	k := "http4:https://nas.example/"
	e.TargetResult(k, false, "timeout")
	e.TargetResult(k, false, "timeout")
	if len(*got) != 0 {
		t.Fatalf("two failures must stay quiet: %v", *got)
	}
	e.TargetResult(k, false, "timeout")
	e.TargetResult(k, false, "timeout")
	if len(*got) != 1 || (*got)[0].Kind != "target_down" || (*got)[0].Severity != "warning" {
		t.Fatalf("one warning expected: %+v", *got)
	}
	e.TargetResult(k, true, "")
	if len(*got) != 2 || (*got)[1].Kind != "target_up" || (*got)[1].Severity != "info" {
		t.Fatalf("recovery info expected: %+v", *got)
	}
	e.TargetResult(k, true, "")
	e.TargetResult(k, false, "x")
	if len(*got) != 2 {
		t.Fatalf("a single failure after recovery must stay quiet: %+v", *got)
	}
}
