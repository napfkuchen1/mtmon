// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"testing"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestArpPresent(t *testing.T) {
	for _, c := range []struct {
		name string
		row  Row
		want bool
	}{
		{"reachable", Row{"status": "reachable"}, true},
		{"delay", Row{"status": "delay"}, true},
		{"stale", Row{"status": "stale"}, false},
		{"static permanent", Row{"status": "permanent"}, true},
		{"dhcp permanent", Row{"status": "permanent", "dhcp": "true"}, false},
		{"dynamic permanent", Row{"status": "permanent", "dynamic": "true"}, false},
	} {
		if got := arpPresent(c.row); got != c.want {
			t.Errorf("%s: arpPresent = %v, want %v", c.name, got, c.want)
		}
	}
}

func newTestState() *mergeState {
	return &mergeState{
		byMAC: map[string]*store.Observation{}, nd6: map[string]string{}, rank: map[string]int{},
		edge: map[string]edgeLoc{}, hint: map[string]string{}, shared: map[string]edgeLoc{},
		depth: map[string]int{}, ownMAC: map[string]string{}, names: map[string]string{}, uplinks: map[string]bool{},
	}
}

func TestMergeLocationPriority(t *testing.T) {
	st := newTestState()
	set := func(prio int, dev string) {
		st.upd("AA:BB:CC:00:00:01", true, prio, func(o *store.Observation) { o.Device = dev })
	}
	set(20, "ap")
	set(1, "switch") // lower priority: ignored
	if got := st.byMAC["AA:BB:CC:00:00:01"].Device; got != "ap" {
		t.Fatalf("device = %q, want ap", got)
	}
	st.upd("00:00:00:00:00:00", false, 0, func(o *store.Observation) {})
	if len(st.byMAC) != 1 {
		t.Fatalf("zero MAC created a client: %v", st.byMAC)
	}
}

func TestResolveShared(t *testing.T) {
	st := newTestState()
	st.shared["AA:BB:CC:00:00:01"] = edgeLoc{dev: "sw", iface: "ether2"} // only seen behind a trunk: unmanaged switch
	st.shared["AA:BB:CC:00:00:02"] = edgeLoc{dev: "sw", iface: "ether2"} // has an edge port elsewhere
	st.edge["AA:BB:CC:00:00:02"] = edgeLoc{dev: "ap", iface: "ether1"}
	st.shared["AA:BB:CC:00:00:03"] = edgeLoc{dev: "sw", iface: "ether2"} // a managed device itself
	st.ownMAC["AA:BB:CC:00:00:03"] = "ap"
	st.resolveShared()
	if o := st.byMAC["AA:BB:CC:00:00:01"]; o == nil || o.Device != "sw" || !o.Present || o.Via != "bridge host on sw/ether2 (shared port)" {
		t.Fatalf("shared host not placed on the uplink port: %+v", o)
	}
	if len(st.byMAC) != 1 {
		t.Fatalf("edge-known or managed MACs must not be placed: %v", st.byMAC)
	}
}

func TestResolveWifiAP(t *testing.T) {
	roleOf := map[string]string{"router": "router", "ap1": "ap", "ap2": "ap"}
	none := func(string) (string, string, bool) { return "", "", false }
	mk := func(mac string) *mergeState {
		st := newTestState()
		st.byMAC[mac] = &store.Observation{MAC: mac, WiFi: true, Device: "router", Iface: "wlan1"}
		return st
	}
	// the AP learned the client on an edge port: it wins over the controller
	st := mk("m1")
	st.edge["m1"] = edgeLoc{dev: "ap1", iface: "wlan2", ap: true}
	st.resolveWifiAP(roleOf, none)
	if o := st.byMAC["m1"]; o.Device != "ap1" || o.Iface != "wlan2" {
		t.Fatalf("edge AP ignored: %+v", o)
	}
	// CAP identity in the registration row beats everything
	st = mk("m2")
	st.hint["m2"] = "ap2"
	st.edge["m2"] = edgeLoc{dev: "ap1", iface: "wlan2", ap: true}
	st.resolveWifiAP(roleOf, none)
	if o := st.byMAC["m2"]; o.Device != "ap2" {
		t.Fatalf("hint ignored: %+v", o)
	}
	// controller only: keep the AP the client was last seen on
	st = mk("m3")
	st.resolveWifiAP(roleOf, func(string) (string, string, bool) { return "ap1", "wlan3", true })
	if o := st.byMAC["m3"]; o.Device != "ap1" || o.Iface != "wlan3" {
		t.Fatalf("previous AP not kept: %+v", o)
	}
	// previous location is not an AP: stay on the controller
	st = mk("m4")
	st.resolveWifiAP(roleOf, func(string) (string, string, bool) { return "router", "wlan1", true })
	if o := st.byMAC["m4"]; o.Device != "router" {
		t.Fatalf("moved to a non-AP: %+v", o)
	}
}
