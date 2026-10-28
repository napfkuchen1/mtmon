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
