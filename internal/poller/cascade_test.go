// SPDX-License-Identifier: AGPL-3.0-only

package poller

import "testing"

func TestCascadedRouterIsNoInternetEdge(t *testing.T) {
	m := &Manager{snaps: map[string]*devSnap{
		"gw": {role: "router", wan: map[string]bool{"ether1": true, "pppoe-out1": true},
			addrs:  []Row{{"address": "192.168.88.1/24"}},
			routes: []Row{{"dst-address": "0.0.0.0/0", "gateway": "pppoe-out1", "active": "true"}}},
		"lab": {role: "router", wan: map[string]bool{"ether1": true},
			addrs:  []Row{{"address": "192.168.88.50/24"}},
			routes: []Row{{"dst-address": "0.0.0.0/0", "gateway": "192.168.88.1", "active": "true"}}},
	}}
	if m.cascaded("gw") || !m.cascaded("lab") {
		t.Fatalf("gw=%v lab=%v, want false/true", m.cascaded("gw"), m.cascaded("lab"))
	}
	if got := m.WANSummary(); got != "gw: ether1, pppoe-out1" {
		t.Errorf("WANSummary = %q", got)
	}
}
