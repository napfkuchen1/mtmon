// SPDX-License-Identifier: AGPL-3.0-only

package poller

import "testing"

func TestScrubRowDropsSecretsVolatileAndDynamic(t *testing.T) {
	if _, ok := ScrubRow(Row{".id": "*1", "dynamic": "true"}); ok {
		t.Error("dynamic row kept")
	}
	r, ok := ScrubRow(Row{".id": "*2", "name": "wg0", "private-key": "xyz", "bytes": "5", "last-handshake": "1s", "preshared-key": "k", "disabled": "false"})
	if !ok || r["name"] != "wg0" || r["disabled"] != "false" {
		t.Fatalf("row: %v %v", r, ok)
	}
	for _, k := range []string{"private-key", "bytes", "last-handshake", "preshared-key"} {
		if _, has := r[k]; has {
			t.Errorf("%s survived", k)
		}
	}
}

func TestScrubRowInDropsRuntimeFieldsOfRealSections(t *testing.T) {
	cases := []struct {
		sec  string
		a, b Row
	}{
		{"/interface/bridge/port", Row{".id": "*1", "interface": "ether1", "pvid": "1", "debug-info": "a-b", "tx-bpdu": "5", "last-topology-change": "1s", "role": "root-port", "status": "in-bridge", "edge-port": "no"},
			Row{".id": "*1", "interface": "ether1", "pvid": "1", "debug-info": "c", "tx-bpdu": "9", "last-topology-change": "9s", "role": "designated-port", "status": "inactive", "edge-port": "yes"}},
		{"/system/ntp/client", Row{"enabled": "true", "freq-drift": "1", "system-offset": "2", "status": "synchronized", "synced-server": "x"},
			Row{"enabled": "true", "freq-drift": "3", "system-offset": "4", "status": "started", "synced-server": "y"}},
		{"/ip/pool", Row{".id": "*1", "name": "dhcp", "ranges": "10.0.0.2-10.0.0.9", "used": "3", "available": "5"},
			Row{".id": "*1", "name": "dhcp", "ranges": "10.0.0.2-10.0.0.9", "used": "4", "available": "4"}},
		{"/system/scheduler", Row{".id": "*1", "name": "pppoe", "interval": "1d", "next-run": "a", "run-count": "1"},
			Row{".id": "*1", "name": "pppoe", "interval": "1d", "next-run": "b", "run-count": "2"}},
	}
	for _, c := range cases {
		a, _ := ScrubRowIn(c.sec, c.a)
		b, _ := ScrubRowIn(c.sec, c.b)
		if len(a) != len(b) {
			t.Fatalf("%s: %v vs %v", c.sec, a, b)
		}
		for k, v := range a {
			if b[k] != v {
				t.Errorf("%s: %s differs: %q vs %q", c.sec, k, v, b[k])
			}
		}
	}
	// a real setting with the same name elsewhere stays
	if r, _ := ScrubRowIn("/ip/firewall/filter", Row{"status": "x", "used": "1"}); r["status"] != "x" || r["used"] != "1" {
		t.Errorf("setting dropped outside its section: %v", r)
	}
	if r, _ := ScrubRowIn("/interface/bridge/port", Row{"pvid": "10", "edge": "yes"}); r["pvid"] != "10" || r["edge"] != "yes" {
		t.Errorf("setting dropped: %v", r)
	}
}

// the four real /system/history entries of #237 (oldest first)
var realHistory = []Row{
	{".id": "*215", "action": "device changed", "by": "admin", "time": "2026-10-30 03:10:05", "trace": "scheduler:PPPoE_Reconnect/action:895"},
	{".id": "*217", "action": "changed script settings", "by": "", "time": "2026-10-30 07:27:23", "trace": "netwatch:type: icmp, host: 203.0.113.2/script:dns-heal/action:899"},
	{".id": "*228", "action": "address list entry removed", "by": "claude", "time": "2026-10-30 17:49:34", "trace": "ssh-cmd:claude@192.168.0.200/action:916"},
	{".id": "*229", "action": "address list entry removed", "by": "claude", "time": "2026-10-30 17:49:34", "trace": "ssh-cmd:claude@192.168.0.200/action:916"},
}

func TestChangeSourceFromTraceNotBy(t *testing.T) {
	want := []string{"scheduler PPPoE_Reconnect", "script", "claude via SSH from 192.168.0.200", "claude via SSH from 192.168.0.200"}
	for i, r := range realHistory {
		if got, _ := ChangeSource(r); got != want[i] {
			t.Errorf("entry %s: %q, want %q", r[".id"], got, want[i])
		}
	}
	if l, _ := ChangeSource(Row{"by": "bob", "trace": "api:bob@10.0.0.5/x"}); l != "bob via API/REST from 10.0.0.5" {
		t.Errorf("api: %q", l)
	}
	if l, _ := ChangeSource(Row{"by": "bob", "trace": "winbox:bob@10.0.0.5/x"}); l != "bob via WinBox/WebFig from 10.0.0.5" {
		t.Errorf("winbox: %q", l)
	}
	if l, _ := ChangeSource(Row{"by": "bob"}); l != "bob" {
		t.Errorf("no trace: %q", l)
	}
}

func TestChangedByNewerThanLastSnapshotDedupNewestFirst(t *testing.T) {
	got, newest, planned := changedBy(realHistory, 5, 0x216)
	if len(got) != 2 || got[0] != "claude via SSH from 192.168.0.200" || got[1] != "script" || newest != 0x229 || planned {
		t.Fatalf("got %v %x %v", got, newest, planned)
	}
	if got, newest, _ := changedBy(realHistory, 5, 0x229); got != nil || newest != 0x229 {
		t.Fatalf("nothing newer: %v %x", got, newest)
	}
	if got, _, _ := changedBy(realHistory, 1, 0); len(got) != 1 {
		t.Fatalf("limit: %v", got)
	}
}

// existing install: no stored id, the reference comes from the stored history mark; one new entry = one source
func TestChangedByAfterMarkIDNamesOnlyTheNewEntry(t *testing.T) {
	prev := historyMark(realHistory[:3]) // the mark taken before the last change
	after := MarkHistoryID(prev)
	if after != 0x228 {
		t.Fatalf("mark id %x", after)
	}
	got, newest, _ := changedBy(realHistory, 5, after)
	if len(got) != 1 || got[0] != "claude via SSH from 192.168.0.200" || newest != 0x229 {
		t.Fatalf("got %v %x, want exactly the one new source", got, newest)
	}
	if MarkHistoryID("0") != 0 || MarkHistoryID("") != 0 {
		t.Fatal("empty mark must give 0")
	}
}

func TestChangeSourceDropsUnspecifiedAddress(t *testing.T) {
	for _, ip := range []string{"::", "0.0.0.0"} {
		if l, _ := ChangeSource(Row{"by": "bob", "trace": "winbox:bob@" + ip + "/x"}); l != "bob via WinBox/WebFig" {
			t.Errorf("%s: %q", ip, l)
		}
	}
	if l, _ := ChangeSource(Row{"trace": "api:bob@fe80::1/x"}); l != "bob via API/REST from fe80::1" {
		t.Errorf("ipv6 kept: %q", l)
	}
}

func TestChangedByPlannedWhenOnlySchedulerRan(t *testing.T) {
	got, _, planned := changedBy(realHistory[:1], 5, 0)
	if len(got) != 1 || !planned {
		t.Fatalf("scheduler-only change must be planned: %v %v", got, planned)
	}
	if _, _, planned := changedBy(realHistory[:2], 5, 0); planned {
		t.Fatal("a script run is not a planned scheduler run")
	}
}

func TestScrubDropsRuntimeFieldsOfStaticCapRows(t *testing.T) {
	w, _ := ScrubRowIn("/interface/wifi", Row{".id": "*6E", "about": "operated by CAP F4:1E:57:A0:00:02%bridge", "bound": "true",
		"channel.frequency": "2462.0", "channel.width": "20mhz", "comment": "2.4ghz", "configuration.ssid": "HomeSSID",
		"inactive": "false", "master": "true", "name": "cap-wifi1", "running": "false", "disabled": "false"})
	for _, k := range []string{"about", "bound", "channel.frequency", "channel.width", "inactive", "master", "running"} {
		if _, has := w[k]; has {
			t.Errorf("wifi runtime field %s survived", k)
		}
	}
	if w["configuration.ssid"] != "HomeSSID" || w["name"] != "cap-wifi1" || w["comment"] != "2.4ghz" || w["disabled"] != "false" {
		t.Errorf("settings lost: %v", w)
	}
	// a channel set by hand on a standalone interface is a setting
	if h, _ := ScrubRowIn("/interface/wifi", Row{"name": "wifi1", "channel.frequency": "2412"}); h["channel.frequency"] != "2412" {
		t.Errorf("manual channel dropped: %v", h)
	}
	e, _ := ScrubRowIn("/interface", Row{"name": "ether1", "mac-address": "AA:BB:CC:00:00:01", "running": "true", "l2mtu": "1592", "max-l2mtu": "4074",
		"fp-rx-byte": "5", "rx-byte": "1", "link-downs": "0", "last-link-up-time": "x", "mtu": "1500", "disabled": "false"})
	if len(e) != 3 || e["mtu"] != "1500" {
		t.Errorf("interface row: %v", e)
	}
	b, _ := ScrubRowIn("/ip/firewall/filter", Row{".id": "*1", "chain": "input", "bytes": "9", "match-count": "3", "x-packets": "1"})
	if len(b) != 2 {
		t.Errorf("counters: %v", b)
	}
}

func TestHistoryMarkChangesWithNewEntry(t *testing.T) {
	a := []Row{{".id": "*1", "time": "t1", "action": "added", "by": "admin"}}
	b := append(append([]Row{}, a...), Row{".id": "*2", "time": "t2", "action": "changed", "by": "admin"})
	if historyMark(a) == historyMark(b) || historyMark(a) != historyMark(append([]Row{}, a...)) || historyMark(nil) != "0" {
		t.Fatal("history mark must change exactly when the history does")
	}
}

func TestScrubDropsStpCountersKeepsWifiSettings(t *testing.T) {
	r, _ := ScrubRowIn("/interface/bridge/port", Row{"interface": "ether2", "rx-tc": "3", "topology-changes": "1", "discard-transitions": "2"})
	if len(r) != 1 || r["interface"] != "ether2" {
		t.Fatalf("%v", r)
	}
	found := false
	for _, s := range ConfigSections {
		found = found || s == "/interface/wifi/access-list"
	}
	if !found {
		t.Fatal("wifi access-list not in snapshot sections")
	}
}
