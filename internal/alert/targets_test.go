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

// A name that does not exist fails IPv4 and IPv6 alike: one alert for the target, and removing it closes that alert (#194).
func TestTargetNXDOMAINRaisesOneAlert(t *testing.T) {
	e, got := newEngine(t)
	k4, k6 := "http4:https://dead.example/", "http6:https://dead.example/"
	for i := 0; i < 3; i++ {
		e.TargetResult(k4, false, "NXDOMAIN")
		e.TargetResult(k6, false, "NXDOMAIN")
	}
	if len(*got) != 1 || (*got)[0].Kind != "target_down" || (*got)[0].Subject != "http:https://dead.example/" {
		t.Fatalf("one alert for the target expected: %+v", *got)
	}
	// any other reason stays per family
	for i := 0; i < 3; i++ {
		e.TargetResult("http6:https://slow.example/", false, "timeout")
	}
	if len(*got) != 2 || (*got)[1].Subject != "http6:https://slow.example/" {
		t.Fatalf("per-family alert expected: %+v", *got)
	}
	e.TargetRemoved(k4)
	e.TargetRemoved(k6)
	al, _ := e.St.Alerts(10)
	for _, a := range al {
		if a.Subject == "http:https://dead.example/" && !a.Acked {
			t.Fatalf("shared alert still open after removal: %+v", a)
		}
	}
}
