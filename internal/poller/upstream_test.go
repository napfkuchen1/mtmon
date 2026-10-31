// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"strings"
	"testing"
)

func vend(mac string) string {
	if strings.HasPrefix(mac, "3C:A6:2F") {
		return "AVM Audiovisuelles Marketing und Computersysteme GmbH"
	}
	return ""
}

// Typical DSL/fibre setup behind a FRITZ!Box: PPPoE dialled over ether1, DS-Lite tunnel on top, an address in the
// modem's network on the same port.
func TestUpstreamFritzBoxPPPoEDSLite(t *testing.T) {
	s := &devSnap{role: "router",
		wan:   map[string]bool{"ether1": true, "pppoe-out1": true, "ipipv6-tunnel1": true},
		ifs:   []Row{{"name": "ether1", "type": "ether"}, {"name": "pppoe-out1", "type": "pppoe-out"}, {"name": "ipipv6-tunnel1", "type": "ipipv6"}, {"name": "bridge", "type": "bridge"}},
		pppoe: []Row{{"name": "pppoe-out1", "interface": "ether1"}},
		addrs: []Row{{"address": "192.168.178.2/24", "interface": "ether1"}, {"address": "192.168.88.1/24", "interface": "bridge"}},
		arp: []Row{
			{"mac-address": "3c:a6:2f:11:22:33", "address": "192.168.178.1", "interface": "ether1", "status": "reachable"},
			{"mac-address": "02:00:00:00:00:07", "address": "192.168.88.7", "interface": "bridge", "status": "reachable"},
			{"mac-address": "02:00:00:00:00:09", "address": "192.168.178.50", "interface": "ether1", "status": "failed"},
		}}
	got := upstreamsFor("gw", s, vend)
	if len(got) != 1 {
		t.Fatalf("one upstream expected: %+v", got)
	}
	u := got[0]
	if u.Port != "ether1" || u.IP != "192.168.178.1" || u.MAC != "3C:A6:2F:11:22:33" || u.Label != "Modem / router (AVM)" || u.NoIP {
		t.Errorf("%+v", u)
	}
	want := map[string]bool{"PPPoE": true, "DS-Lite": true, "transfer net 192.168.178.0/24": true}
	for _, m := range u.Modes {
		delete(want, m)
	}
	if len(want) != 0 || len(u.Modes) != 3 {
		t.Errorf("modes: %v", u.Modes)
	}
}

func TestUpstreamOntWithoutIPAndDoubleNAT(t *testing.T) {
	// fibre ONT: PPPoE over ether1, nothing answers ARP
	ont := &devSnap{wan: map[string]bool{"ether1": true, "pppoe-out1": true},
		ifs: []Row{{"name": "ether1", "type": "ether"}, {"name": "pppoe-out1", "type": "pppoe-out"}}, pppoe: []Row{{"interface": "ether1"}}}
	if got := upstreamsFor("gw", ont, nil); len(got) != 1 || !got[0].NoIP || got[0].Label != "ONT / modem (no IP)" || got[0].Modes[0] != "PPPoE" {
		t.Errorf("ont: %+v", got)
	}
	// router behind a router: default route via a private gateway on the WAN port, no PPPoE
	dn := &devSnap{wan: map[string]bool{"ether1": true},
		ifs:    []Row{{"name": "ether1", "type": "ether"}},
		routes: []Row{{"dst-address": "0.0.0.0/0", "active": "true", "immediate-gw": "192.168.1.1%ether1"}},
		arp:    []Row{{"mac-address": "aa:bb:cc:00:00:01", "address": "192.168.1.1", "interface": "ether1", "status": "reachable"}}}
	got := upstreamsFor("gw", dn, nil)
	if len(got) != 1 || len(got[0].Modes) != 1 || got[0].Modes[0] != "double NAT" || got[0].Label != "Modem / router" {
		t.Errorf("double nat: %+v", got)
	}
	// a public next hop is no double NAT
	dn.routes = []Row{{"dst-address": "0.0.0.0/0", "active": "true", "immediate-gw": "203.0.113.1%ether1"}}
	if got := upstreamsFor("gw", dn, nil); len(got[0].Modes) != 0 {
		t.Errorf("public gateway: %+v", got)
	}
}

// An ARP entry that aged to "stale" still proves the modem is there; the banner must not flip to "ONT / modem (no IP)".
func TestUpstreamStaleARPKeepsModem(t *testing.T) {
	s := &devSnap{role: "router", wan: map[string]bool{"ether1": true, "pppoe-out1": true},
		ifs:   []Row{{"name": "ether1", "type": "ether"}, {"name": "pppoe-out1", "type": "pppoe-out"}},
		pppoe: []Row{{"interface": "ether1"}},
		arp:   []Row{{"mac-address": "3c:a6:2f:11:22:33", "address": "192.168.178.1", "interface": "ether1", "status": "stale"}}}
	got := upstreamsFor("gw", s, vend)
	if len(got) != 1 || got[0].NoIP || got[0].IP != "192.168.178.1" {
		t.Errorf("stale ARP: %+v", got)
	}
}

func TestUpstreamNothingWithoutWAN(t *testing.T) {
	if got := upstreamsFor("ap", &devSnap{role: "ap", arp: []Row{{"mac-address": "aa:bb:cc:00:00:01", "interface": "bridge", "status": "reachable"}}}, nil); got != nil {
		t.Errorf("an AP has no upstream: %+v", got)
	}
	if got := upstreamsFor("gw", nil, nil); got != nil {
		t.Errorf("nil snapshot: %+v", got)
	}
}
