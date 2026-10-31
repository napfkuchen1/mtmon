// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestParseRouterDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"1d": 24 * time.Hour, "1w2d": 9 * 24 * time.Hour, "30m": 30 * time.Minute, "1d00:00:00": 24 * time.Hour,
		"00:30:00": 30 * time.Minute, "12h": 12 * time.Hour, "": 0, "0s": 0,
	} {
		if got := parseRouterDuration(in); got != want {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}

func schedulerSnap(e *Engine, row map[string]string) {
	e.St.SaveConfigSnap("gw", time.Now().Unix(), store.ConfigSnap{"/system/scheduler": {row}})
}

// A nightly scheduler run that cycles the PPPoE client explains the drop right after it.
func TestPlannedReconnectIsInfo(t *testing.T) {
	e, got := newEngine(t)
	now := time.Now()
	run := now.Add(-time.Minute)
	schedulerSnap(e, map[string]string{"name": "pppoe-nightly", "interval": "1d", "start-date": "jan/01/2024",
		"start-time": run.Format("15:04:05"), "on-event": "/interface pppoe-client disable pppoe-out1; :delay 5s; /interface pppoe-client enable pppoe-out1"})
	e.InterfaceState("gw", "pppoe-out1", false)
	if kinds(*got) != "wan_down/gw" || (*got)[0].Severity != "info" || !strings.Contains((*got)[0].Msg, "pppoe-nightly") {
		t.Fatalf("planned: %+v", *got)
	}
}

func TestUnplannedWANDropStaysWarningWithScheduler(t *testing.T) {
	e, got := newEngine(t)
	run := time.Now().Add(-5 * time.Hour)
	schedulerSnap(e, map[string]string{"name": "pppoe-nightly", "interval": "1d", "start-date": "jan/01/2024",
		"start-time": run.Format("15:04:05"), "on-event": "/interface pppoe-client disable pppoe-out1; /interface pppoe-client enable pppoe-out1"})
	e.InterfaceState("gw", "pppoe-out1", false)
	if (*got)[0].Severity != "warning" {
		t.Fatalf("drop at another time must stay a warning: %+v", *got)
	}
}

func TestSchedulerNotTouchingWANDoesNotExplain(t *testing.T) {
	e, got := newEngine(t)
	run := time.Now().Add(-time.Minute)
	schedulerSnap(e, map[string]string{"name": "backup", "interval": "1d", "start-time": run.Format("15:04:05"),
		"on-event": "/system backup save name=nightly"})
	e.InterfaceState("gw", "pppoe-out1", false)
	if (*got)[0].Severity != "warning" {
		t.Fatalf("unrelated scheduler must not downgrade: %+v", *got)
	}
}

// A traffic spike caused by the router itself names the router, not its WAN-port MAC (#122).
func TestWhoNamesManagedDevice(t *testing.T) {
	e, _ := newEngine(t)
	e.SelfName = func(mac string) (string, bool) { return "gw", mac == "04:F4:1C:29:A5:DC" }
	if got := e.who("04:F4:1C:29:A5:DC"); got != "gw" {
		t.Fatalf("who = %q", got)
	}
	if got := e.who("AA:BB:CC:00:00:01"); got != "AA:BB:CC:00:00:01" {
		t.Fatalf("unknown MAC stays the MAC: %q", got)
	}
}

// #188: PPPoE_Reconnect at 03:10 daily explains the drop at 03:10:05; the 5-minute watchdog never explains anything.
func TestPlannedRunWindowAndInterval(t *testing.T) {
	e, _ := newEngine(t)
	loc := time.Local
	at := func(h, m, s int) time.Time { return time.Date(2026, 10, 30, h, m, s, 0, loc) }
	schedulerSnap(e, map[string]string{"name": "PPPoE_Reconnect", "interval": "1d", "start-date": "oct/01/2026",
		"start-time": "03:10:00", "on-event": "/interface pppoe-client disable pppoe-out1; /interface pppoe-client enable pppoe-out1"})
	if got := e.plannedReconnect("gw", "pppoe-out1", at(3, 10, 5)); got != "PPPoE_Reconnect" {
		t.Fatalf("03:10:05: %q", got)
	}
	if got := e.plannedReconnect("gw", "pppoe-out1", at(3, 20, 0)); got != "" {
		t.Fatalf("10 min later must not match: %q", got)
	}
	schedulerSnap(e, map[string]string{"name": "WG_Check_Reload", "interval": "5m", "start-time": "00:00:00",
		"on-event": "/interface wireguard disable wg0; /interface wireguard enable wg0"})
	if got := e.plannedIface("gw", "wg0", at(3, 10, 5)); got != "" {
		t.Fatalf("5 min interval is never planned: %q", got)
	}
	schedulerSnap(e, map[string]string{"name": "boot", "interval": "1d", "start-time": "startup", "on-event": "/interface disable ether5"})
	if got := e.plannedIface("gw", "ether5", at(3, 10, 5)); got != "" {
		t.Fatalf("start-time=startup is not a clock time: %q", got)
	}
}

func TestPlannedIfaceAndConfigAreInfo(t *testing.T) {
	e, got := newEngine(t)
	run := time.Now().Add(-time.Minute)
	schedulerSnap(e, map[string]string{"name": "port-cycle", "interval": "1d", "start-time": run.Format("15:04:05"),
		"on-event": "/interface ethernet disable ether5; :delay 3s; /interface ethernet enable ether5"})
	e.ConfigChanged("gw", 1, nil)
	e.InterfaceState("gw", "ether5", false)
	for _, a := range *got {
		if a.Severity != "info" || !strings.Contains(a.Msg, "port-cycle") {
			t.Errorf("planned, expected info: %+v", a)
		}
	}
	if len(*got) < 1 {
		t.Fatal("no alerts raised")
	}
}

func TestInfoKindDowngradesWarning(t *testing.T) {
	e, got := newEngine(t)
	e.Raise("dhcp_problem", "gw", "warning", "DHCP trouble", 0)
	if err := e.St.SetInfoKind("dhcp_problem", true); err != nil {
		t.Fatal(err)
	}
	e.Raise("dhcp_problem", "gw2", "warning", "DHCP trouble", 0)
	if len(*got) != 2 || (*got)[0].Severity != "warning" || (*got)[1].Severity != "info" || !strings.Contains((*got)[1].Msg, "set to info") {
		t.Fatalf("%+v", *got)
	}
	e.St.SetInfoKind("dhcp_problem", false)
	if e.St.IsInfoKind("dhcp_problem") {
		t.Fatal("not restored")
	}
}
