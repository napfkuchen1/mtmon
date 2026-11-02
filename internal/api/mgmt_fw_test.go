// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"testing"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// A connection verdict needs logging of the same IP family: IPv4 drop logging says nothing about an IPv6 flow (#298).
func TestVerdictNeedsLoggingOfSameFamily(t *testing.T) {
	_, s := newTestServer(t)
	mac := "02:00:00:00:00:01"
	if _, err := s.St.UpsertClients([]store.Observation{{MAC: mac, IP: "192.168.88.10", Device: "rb", Present: true}}); err != nil {
		t.Fatal(err)
	}
	conns := func() []store.Conn {
		return []store.Conn{
			{TS: 1000, RIP: "93.184.216.34", RPort: 443, Proto: 6, Dir: "u", Device: "rb"},
			{TS: 1000, RIP: "2606:2800:220:1::1", RPort: 443, Proto: 6, Dir: "u", Device: "rb"},
		}
	}
	// only IPv4 rules log: IPv6 stays unknown
	s.St.SaveFwRules([]store.FwRule{{Device: "rb", Prefix: "MTM-1A", Chain: "forward", Action: "drop", Managed: true}})
	cs := conns()
	s.annotateConns(mac, cs, 0)
	if cs[0].Verdict != "allowed" || cs[1].Verdict != "" {
		t.Fatalf("IPv4 only: %+v", cs)
	}
	// IPv6 logging active: IPv6 flows get a verdict as well
	s.St.SaveFwRules([]store.FwRule{{Device: "rb", Prefix: "MTM6-2B", Chain: "forward", Action: "drop", Managed: true}})
	cs = conns()
	s.annotateConns(mac, cs, 0)
	if cs[0].Verdict != "allowed" || cs[1].Verdict != "allowed" {
		t.Fatalf("both families: %+v", cs)
	}
}

func TestIsIPv6(t *testing.T) {
	for in, want := range map[string]bool{"2606:2800::1": true, "::ffff:1.2.3.4": false, "1.2.3.4": false, "": false, "x": false} {
		if got := isIPv6(in); got != want {
			t.Errorf("isIPv6(%q)=%v", in, got)
		}
	}
}
