// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func fp(v float64) *float64 { return &v }

func poeHW(status string, fault bool) store.HWDevice {
	return store.HWDevice{Caps: store.HWCaps{PoE: store.HWOn},
		PoE: []store.HWPoe{{Iface: "ether2", Status: status, Fault: fault}}}
}

func openHW(t *testing.T, e *Engine, kind string) []store.Alert {
	t.Helper()
	as, _ := e.St.Alerts(100)
	var out []store.Alert
	for _, a := range as {
		if a.Kind == kind && !a.Acked {
			out = append(out, a)
		}
	}
	return out
}

// A PoE port in short-circuit or overload raises once (not on every poll) and resolves when the port is fine.
func TestPoeFaultRaisesOnceAndResolves(t *testing.T) {
	e, got := newEngine(t)
	e.Hardware("sw", poeHW("powered-on", false))
	if len(*got) != 0 {
		t.Fatalf("healthy port raised %s", kinds(*got))
	}
	e.Hardware("sw", poeHW("short-circuit", true))
	e.Hardware("sw", poeHW("short-circuit", true))
	if kinds(*got) != "poe_fault/sw/ether2" {
		t.Fatalf("raised %q, want one poe_fault", kinds(*got))
	}
	a := (*got)[0]
	if a.Severity != "warning" || a.MsgKey != "PoE short circuit on {d} port {i}" || !strings.Contains(a.Msg, "sw") || !strings.Contains(a.Msg, "ether2") {
		t.Fatalf("alert = %+v", a)
	}
	if n := len(openHW(t, e, "poe_fault")); n != 1 {
		t.Fatalf("%d open alerts, want 1", n)
	}
	e.Hardware("sw", poeHW("powered-on", false))
	if n := len(openHW(t, e, "poe_fault")); n != 0 {
		t.Fatalf("alert still open after recovery: %d", n)
	}
	e.Hardware("sw", poeHW("overload", true)) // a new fault is a new alert
	if len(*got) != 2 || (*got)[1].MsgKey != "PoE overload on {d} port {i}" {
		t.Fatalf("raised %q", kinds(*got))
	}
}

func sfpHW(rx *float64) store.HWDevice {
	return store.HWDevice{Caps: store.HWCaps{SFP: store.HWOn}, SFP: []store.HWSfp{{Iface: "sfp1", Present: true, RxDBm: rx}}}
}

// SFP receive power below the configured limit warns; it resolves only 0.5 dB above the limit; without a limit
// and without a reading nothing is raised.
func TestSfpRxWarning(t *testing.T) {
	e, got := newEngine(t)
	e.Hardware("sw", sfpHW(fp(-30)))
	if len(*got) != 0 {
		t.Fatalf("no limit set, yet %s", kinds(*got))
	}
	if err := e.St.SetSfpWarnDBm(fp(-20)); err != nil {
		t.Fatal(err)
	}
	e.Hardware("sw", sfpHW(nil)) // module without DOM: no reading, no alert
	e.Hardware("sw", sfpHW(fp(-19.9)))
	if len(*got) != 0 {
		t.Fatalf("above the limit raised %s", kinds(*got))
	}
	e.Hardware("sw", sfpHW(fp(-21)))
	e.Hardware("sw", sfpHW(fp(-22)))
	if kinds(*got) != "sfp_rx_low/sw/sfp1" || (*got)[0].Params["p"] != "-21.0" || (*got)[0].Params["l"] != "-20.0" {
		t.Fatalf("raised %q %+v", kinds(*got), (*got)[0].Params)
	}
	e.Hardware("sw", sfpHW(fp(-19.8))) // above the limit, inside the 0.5 dB hysteresis: stays open
	if n := len(openHW(t, e, "sfp_rx_low")); n != 1 {
		t.Fatalf("resolved too early: %d open", n)
	}
	e.Hardware("sw", sfpHW(fp(-19.4)))
	if n := len(openHW(t, e, "sfp_rx_low")); n != 0 {
		t.Fatalf("still open after recovery: %d", n)
	}
}

// An alert whose feature was switched off is resolved; a transient gap keeps it open.
func TestHardwareAlertResolvedWhenFeatureGone(t *testing.T) {
	e, _ := newEngine(t)
	e.Hardware("sw", poeHW("short-circuit", true))
	e.Hardware("sw", store.HWDevice{Caps: store.HWCaps{PoE: store.HWOn}}) // port not read this time
	if n := len(openHW(t, e, "poe_fault")); n != 1 {
		t.Fatalf("transient gap closed the alert: %d open", n)
	}
	e.Hardware("sw", store.HWDevice{Caps: store.HWCaps{PoE: store.HWOff}})
	if n := len(openHW(t, e, "poe_fault")); n != 0 {
		t.Fatalf("alert open although the feature is off: %d", n)
	}
}
