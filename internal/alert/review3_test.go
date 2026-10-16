package alert

import (
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// Two or more devices failing in the same window are one alert, not one critical alert per device.
func TestOfflineDevicesAreGrouped(t *testing.T) {
	e, got := newEngine(t)
	e.offGroupDelay = time.Hour // collect, flush by hand
	e.DeviceState("gw", false, "dial tcp: i/o timeout", 0, 0)
	e.DeviceState("ap1", false, "context deadline exceeded", 0, 0)
	e.DeviceState("ap1", false, "context deadline exceeded", 0, 0) // repeated poll: still one entry
	e.flushOffline()
	if kinds(*got) != "devices_unreachable/group" || (*got)[0].Severity != "warning" || !strings.Contains((*got)[0].Msg, "2 devices: ap1, gw") {
		t.Fatalf("grouped alert: %+v", *got)
	}
	// a single device keeps the critical alert and names the cause
	e2, got2 := newEngine(t)
	e2.DeviceState("sw", false, "connection refused", 0, 0)
	if kinds(*got2) != "device_offline/sw" || (*got2)[0].Severity != "critical" || !strings.Contains((*got2)[0].Msg, "refused") {
		t.Fatalf("single: %+v", *got2)
	}
}

// A device that answers again before the grouping window ends raises neither an offline nor an online alert.
func TestOfflineBlipBeforeFlushIsSilent(t *testing.T) {
	e, got := newEngine(t)
	e.DeviceState("gw", true, "", 5, 40)
	e.offGroupDelay = time.Hour
	e.DeviceState("gw", false, "timeout", 0, 0)
	e.DeviceState("gw", true, "", 5, 40)
	e.flushOffline()
	if len(*got) != 0 {
		t.Fatalf("blip alerted: %s", kinds(*got))
	}
}

func TestReachReason(t *testing.T) {
	for in, want := range map[string]string{"dial tcp: i/o timeout": "timeout", "connection refused": "refused",
		"authentication failed (check x)": "auth", "boom": "error"} {
		if got := reachReason(in); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}

// A drop at the same time on three earlier days is a schedule (modem reconnect), not an outage.
func TestRecurringWANDropIsInfo(t *testing.T) {
	e, got := newEngine(t)
	now := time.Now()
	for d := 1; d <= 3; d++ {
		e.St.AddAlert(store.Alert{TS: now.AddDate(0, 0, -d).Add(2 * time.Minute).Unix(), Kind: "wan_down", Subject: "gw", Severity: "warning", Msg: "x"})
	}
	e.InterfaceState("gw", "pppoe-out1", false)
	if kinds(*got) != "wan_down/gw" || (*got)[0].Severity != "info" || !strings.Contains((*got)[0].Msg, "recurring") {
		t.Fatalf("recurring: %+v", *got)
	}
}

func TestWANDropAtOtherTimeStaysWarning(t *testing.T) {
	e, got := newEngine(t)
	now := time.Now()
	for d := 1; d <= 3; d++ {
		e.St.AddAlert(store.Alert{TS: now.AddDate(0, 0, -d).Add(-3 * time.Hour).Unix(), Kind: "wan_down", Subject: "gw", Severity: "warning", Msg: "x"})
	}
	e.InterfaceState("gw", "pppoe-out1", false)
	if (*got)[0].Severity != "warning" {
		t.Fatalf("outage at an unusual time must stay a warning: %+v", *got)
	}
}

func spikeEngine(t *testing.T, firstSeen int64) (*Engine, *[]store.Alert) {
	e, got := newEngine(t)
	e.St.DB.Exec(`INSERT INTO clients(mac,first_seen,last_seen) VALUES('m',?,?)`, firstSeen, time.Now().Unix())
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 10_000})
	e.Checks()
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 2 << 30})
	e.Checks()
	return e, got
}

func TestSpikeOfKnownDominantClientIsInfoAndOncePerDay(t *testing.T) {
	e, got := spikeEngine(t, time.Now().Add(-72*time.Hour).Unix())
	if len(*got) != 1 || (*got)[0].Severity != "info" || !strings.Contains((*got)[0].Msg, "known client") {
		t.Fatalf("known client: %+v", *got)
	}
	e.mu.Lock()
	delete(e.last, "traffic_spike|m") // even without the cooldown the day limit holds
	e.mu.Unlock()
	e.Checks()
	if len(*got) != 1 {
		t.Fatalf("second spike of the same client on the same day alerted: %s", kinds(*got))
	}
}

func TestSpikeOfNewClientStaysWarning(t *testing.T) {
	_, got := spikeEngine(t, time.Now().Add(-time.Hour).Unix())
	if len(*got) != 1 || (*got)[0].Severity != "warning" {
		t.Fatalf("new client: %+v", *got)
	}
}

// 30 Mbit/s over the 5x baseline is not worth an alert until it lasts.
func TestSmallSpikeNeedsDuration(t *testing.T) {
	e, got := newEngine(t)
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 1000})
	e.Checks()
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 100 << 20}) // ~28 Mbit/s over the window
	for i := 0; i < 4; i++ {
		e.Checks()
	}
	if len(*got) != 0 {
		t.Fatalf("short small spike alerted: %s", kinds(*got))
	}
}
