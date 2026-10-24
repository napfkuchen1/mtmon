package alert

import (
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
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

// A planned update: the device is offline for a while and its neighbour's link flaps. That is one info line,
// not a critical offline, a warning link-down and the matching recoveries.
func TestUpdateRebootIsOneInfo(t *testing.T) {
	e, got := newEngine(t)
	e.rebootGrace = time.Hour // nothing fires by timer
	e.DeviceState("rb", true, "", 5, 40)
	e.DeviceBoot("rb", "7.24.2", 5000)
	e.DeviceState("rb", false, "i/o timeout", 0, 0)
	e.InterfaceState("sw", "ether2", false)
	e.DeviceState("rb", true, "", 5, 40)
	e.InterfaceState("sw", "ether2", true)
	e.DeviceBoot("rb", "7.24.4", 20)
	if kinds(*got) != "device_updated/rb" || (*got)[0].Severity != "info" || !strings.Contains((*got)[0].Msg, "7.24.2 → 7.24.4") {
		t.Fatalf("update: %+v", *got)
	}
}

// A link that stays down is still reported, once the grace is over.
func TestHeldLinkDownStillFires(t *testing.T) {
	e, _ := newEngine(t)
	e.rebootGrace = 20 * time.Millisecond
	fired := make(chan store.Alert, 1)
	e.Notify = func(a store.Alert) { fired <- a }
	e.InterfaceState("sw", "ether3", false)
	select {
	case a := <-fired:
		if a.Kind != "iface_down" || a.Subject != "sw/ether3" {
			t.Fatalf("held link: %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("held link-down never fired")
	}
}

// A flap with no device reboot nearby is reported late but complete.
func TestHeldLinkFlapWithoutRebootReported(t *testing.T) {
	e, got := newEngine(t)
	e.rebootGrace = time.Hour
	e.InterfaceState("sw", "ether3", false)
	e.InterfaceState("sw", "ether3", true)
	if kinds(*got) != "iface_down/sw/ether3,iface_up/sw/ether3" {
		t.Fatalf("flap: %+v", *got)
	}
}

// A spike of the same client at the same time of day on three earlier days is a schedule (speed test), not an incident.
func TestRecurringSpikeIsInfo(t *testing.T) {
	e, got := newEngine(t)
	now := time.Now()
	e.St.DB.Exec(`INSERT INTO clients(mac,first_seen,last_seen) VALUES('m',?,?)`, now.Unix(), now.Unix()) // new client: would be a warning
	for d := 1; d <= 3; d++ {
		e.St.AddAlert(store.Alert{TS: now.AddDate(0, 0, -d).Add(4 * time.Minute).Unix(), Kind: "traffic_spike", Subject: "m", Severity: "warning", Msg: "x"})
	}
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 10_000})
	e.Checks()
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 2 << 30})
	e.Checks()
	if len(*got) != 1 || (*got)[0].Severity != "info" || !strings.Contains((*got)[0].Msg, "recurring") {
		t.Fatalf("recurring spike: %+v", *got)
	}
}

func TestWatchedClientOfflineAndBack(t *testing.T) {
	e, got := newEngine(t)
	now := time.Now()
	gone := now.Add(-30 * time.Minute).Unix()
	e.St.DB.Exec(`INSERT INTO clients(mac,hostname,first_seen,last_seen,online,device,watch) VALUES('AA:00:00:00:00:01','intercom',?,?,0,'ap',1)`, gone-86400, gone)
	e.St.DB.Exec(`INSERT INTO clients(mac,hostname,first_seen,last_seen,online,watch) VALUES('AA:00:00:00:00:02','tv',?,?,0,0)`, gone-86400, gone) // not watched
	e.watchChecks()
	e.watchChecks() // reported once
	if kinds(*got) != "client_offline/AA:00:00:00:00:01" || (*got)[0].Severity != "warning" || !strings.Contains((*got)[0].Msg, "intercom") {
		t.Fatalf("offline: %+v", *got)
	}
	e.St.DB.Exec(`UPDATE clients SET online=1, last_seen=? WHERE mac='AA:00:00:00:00:01'`, now.Unix())
	e.mu.Lock()
	delete(e.last, "client_online|AA:00:00:00:00:01")
	e.mu.Unlock()
	e.watchChecks()
	if len(*got) != 2 || (*got)[1].Kind != "client_online" || (*got)[1].Severity != "info" || !strings.Contains((*got)[1].Msg, "back online after") {
		t.Fatalf("back: %+v", *got)
	}
}

func TestWatchedClientInMaintenanceWindowWaits(t *testing.T) {
	e, got := newEngine(t)
	gone := time.Now().Add(-30 * time.Minute).Unix()
	e.St.DB.Exec(`INSERT INTO clients(mac,hostname,first_seen,last_seen,online,device,watch) VALUES('AA:00:00:00:00:01','intercom',?,?,0,'ap',1)`, gone-86400, gone)
	e.St.SetMaintenance("ap", time.Now().Add(time.Hour).Unix())
	e.watchChecks()
	if len(*got) != 0 {
		t.Fatalf("alert during the maintenance window of its AP: %+v", *got)
	}
}

// A reboot of the device behind a port: the link-down/up of that port folds into the device's own alerts, the offline
// alert is closed (as info, with the reboot noted) when the device answers again.
func TestRebootFoldsLinkAlertsAndResolvesOffline(t *testing.T) {
	e, got := newEngine(t)
	e.Cfg.Devices = []config.Device{{Name: "hex", Addr: "10.0.0.1", Role: "router"}, {Name: "ap", Addr: "10.0.0.2", Role: "ap"}}
	e.St.SaveNeighbors("hex", []store.Neighbor{{Device: "hex", Iface: "ether2", MAC: "AA:00:00:00:00:A1", Ident: "ap"}})
	e.DeviceState("ap", true, "", 1, 40)
	e.DeviceBoot("ap", "7.19", 100000)
	e.DeviceState("ap", false, "i/o timeout", 0, 0)
	e.InterfaceState("hex", "ether2", false)
	e.InterfaceState("hex", "ether2", true)
	if kinds(*got) != "device_offline/ap" {
		t.Fatalf("link alerts must fold into the device outage: %s", kinds(*got))
	}
	if e.St.OpenAlerts() != 1 {
		t.Fatalf("open: %d", e.St.OpenAlerts())
	}
	e.DeviceState("ap", true, "", 1, 40)
	e.DeviceBoot("ap", "7.19", 40) // uptime went backwards
	if kinds(*got) != "device_offline/ap,device_online/ap" {
		t.Fatalf("no extra reboot line expected: %s", kinds(*got))
	}
	as, _ := e.St.Alerts(10)
	for _, a := range as {
		if a.Kind == "device_offline" && (!a.Acked || a.Severity != "info" || !strings.Contains(a.Msg, "resolved: back after") || !strings.Contains(a.Msg, "rebooted (uptime now 40s)")) {
			t.Errorf("offline alert after return: %+v", a)
		}
	}
	// a link that is down while the device behind it answers is a real link problem
	e.InterfaceState("hex", "ether2", false)
	if !strings.Contains(kinds(*got), "iface_down/hex/ether2") {
		t.Errorf("real link-down lost: %s", kinds(*got))
	}
}

func TestNewClientAlertModes(t *testing.T) {
	e, got := newEngine(t)
	e.St.DB.Exec(`INSERT INTO clients(mac,vendor,first_seen,last_seen) VALUES('DC:A6:32:00:00:01','Raspberry Pi',1,1),('3C:22:FB:00:00:02','',1,1),('2A:00:00:00:00:03','Apple',1,1)`)
	e.NewClient("DC:A6:32:00:00:01", "nas", "192.168.88.5") // known manufacturer, real MAC
	if len(*got) != 0 {
		t.Fatalf("default mode must not alert for a known device: %s", kinds(*got))
	}
	e.NewClient("3C:22:FB:00:00:02", "x", "192.168.88.6")      // no vendor
	e.NewClient("2A:00:00:00:00:03", "iphone", "192.168.88.7") // randomised MAC
	if kinds(*got) != "new_client/3C:22:FB:00:00:02,new_client/2A:00:00:00:00:03" {
		t.Fatalf("unknown devices: %s", kinds(*got))
	}
	e.St.SetMeta("alert_new_client", "all")
	e.NewClient("DC:A6:32:00:00:01", "nas", "192.168.88.5")
	if len(*got) != 3 {
		t.Fatalf("mode all: %s", kinds(*got))
	}
	e.St.SetMeta("alert_new_client", "off")
	e.NewClient("3C:22:FB:00:00:02", "x", "192.168.88.6")
	if len(*got) != 3 {
		t.Fatalf("mode off: %s", kinds(*got))
	}
}
