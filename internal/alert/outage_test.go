// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// gateway -> ap -> lab: the AP loses power and takes the router behind it down. One alert names the AP.
func TestOutageNamesUpstreamDevice(t *testing.T) {
	e, got := newEngine(t)
	e.St.SaveNeighbors("gw", []store.Neighbor{{Iface: "ether2", MAC: "aa:00:00:00:00:02", Ident: "ap"}})
	e.St.SaveNeighbors("ap", []store.Neighbor{{Iface: "ether1", MAC: "aa:00:00:00:00:01", Ident: "gw"}, {Iface: "ether2", MAC: "aa:00:00:00:00:03", Ident: "lab"}})
	e.St.SaveNeighbors("lab", []store.Neighbor{{Iface: "ether1", MAC: "aa:00:00:00:00:02", Ident: "ap"}})
	e.Depths = func() map[string]int { return map[string]int{"gw": 0, "ap": 1, "lab": 2} }
	e.offGroupDelay = time.Hour
	e.DeviceState("lab", false, "i/o timeout", 0, 0)
	e.DeviceState("ap", false, "i/o timeout", 0, 0)
	e.flushOffline()
	if kinds(*got) != "devices_unreachable/ap" {
		t.Fatalf("alerts: %+v", *got)
	}
	if m := (*got)[0].Msg; !strings.Contains(m, "ap is unreachable") || !strings.Contains(m, "lab unreachable too") || strings.Contains(m, "shared cause") {
		t.Errorf("msg: %s", m)
	}
	// unrelated islands keep the generic text
	e2, got2 := newEngine(t)
	e2.Depths = func() map[string]int { return map[string]int{"gw": 0, "ap": 1, "lab": 1} }
	e2.offGroupDelay = time.Hour
	e2.DeviceState("lab", false, "i/o timeout", 0, 0)
	e2.DeviceState("ap", false, "i/o timeout", 0, 0)
	e2.flushOffline()
	if kinds(*got2) != "devices_unreachable/group" {
		t.Fatalf("islands: %+v", *got2)
	}
}
