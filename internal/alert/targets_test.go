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

func TestTargetRemovedResetsCounterAndResolves(t *testing.T) {
	e, got := newEngine(t)
	k := "dns:gone.example"
	for i := 0; i < 3; i++ {
		e.TargetResult(k, false, "NXDOMAIN")
	}
	if len(*got) != 1 {
		t.Fatalf("warning expected: %+v", *got)
	}
	e.TargetRemoved(k)
	open := 0
	al, _ := e.St.Alerts(10)
	for _, a := range al {
		if a.Kind == "target_down" && !a.Acked {
			open++
		}
	}
	if open != 0 {
		t.Fatalf("target_down still open after removal")
	}
	// re-adding the name starts counting from zero: two failures stay quiet
	e.TargetResult(k, false, "NXDOMAIN")
	e.TargetResult(k, false, "NXDOMAIN")
	if len(*got) != 1 {
		t.Fatalf("counter was not reset: %+v", *got)
	}
}
