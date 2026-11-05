// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"testing"
	"time"
)

func ifaceStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().Unix()
	row := func(mac, dir string, ifc, ifr uint32, b uint64) FlowRow {
		return FlowRow{TS: now, Exporter: "10.0.0.1", Dev: "r1", MAC: mac, CIP: "10.0.0.5", RIP: "93.184.216.34", RPort: 443, CPort: 40000,
			Proto: 6, Dir: dir, Bytes: b, IfC: ifc, IfR: ifr, Svc: "https"}
	}
	st.Enqueue(
		row("aa:00:00:00:00:01", "u", 10, 1, 1000),
		row("aa:00:00:00:00:01", "d", 10, 1, 5000),
		row("aa:00:00:00:00:02", "u", 2, 1, 300),
		row("aa:00:00:00:00:01", "i", 10, 2, 700), // VLAN 10 -> bridge: routed between two interfaces
		row("aa:00:00:00:00:01", "i", 10, 0, 50),  // to the router itself: not part of the matrix
		row("aa:00:00:00:00:02", "i", 2, 2, 40),   // stays inside one interface: not part of the matrix
		row("aa:00:00:00:00:03", "u", 0, 0, 9),    // no interface information: nothing to attribute
	)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	return st
}

// Different ingress interfaces end up in different rollup rows, and the filter returns only that interface.
func TestRollupPerInterface(t *testing.T) {
	st := ifaceStore(t)
	lan, wan, first, err := st.IfaceUsage(time.Now().Add(-time.Hour).Unix(), true)
	if err != nil {
		t.Fatal(err)
	}
	if first == 0 {
		t.Error("first hour with interface data must be set")
	}
	got := map[uint32]IfaceRow{}
	for _, r := range lan {
		got[r.Idx] = r
	}
	if r := got[10]; r.Up != 1000 || r.Down != 5000 || r.Internal != 750 {
		t.Errorf("vlan 10: %+v", r)
	}
	if r := got[2]; r.Up != 300 || r.Internal != 40 {
		t.Errorf("bridge: %+v", r)
	}
	if len(wan) != 1 || wan[0].Idx != 1 || wan[0].Up != 1300 || wan[0].Down != 5000 {
		t.Errorf("wan usage: %+v", wan)
	}

	top, err := st.TopByIface("clients", time.Now().Add(-time.Hour).Unix(), 10, true, IfaceSel{"r1", 10})
	if err != nil || len(top) != 1 || top[0].Key != "aa:00:00:00:00:01" {
		t.Fatalf("clients on vlan 10: %+v %v", top, err)
	}
	hosts, err := st.TopByIface("hosts", time.Now().Add(-time.Hour).Unix(), 10, true, IfaceSel{"r1", 2})
	if err != nil || len(hosts) != 1 || hosts[0].Up != 300 {
		t.Fatalf("hosts on the bridge: %+v %v", hosts, err)
	}
	// the WAN side selects what left through it
	wanTop, _ := st.TopByIface("clients", time.Now().Add(-time.Hour).Unix(), 10, true, IfaceSel{"r1", 1})
	if len(wanTop) != 2 {
		t.Errorf("clients over the WAN: %+v", wanTop)
	}
}

// An unknown interface, device or list gives an empty (never nil) list, not an error.
func TestIfaceFilterUnknownIsEmpty(t *testing.T) {
	st := ifaceStore(t)
	since := time.Now().Add(-time.Hour).Unix()
	for _, sel := range []IfaceSel{{"r1", 999}, {"nope", 10}, {"\x00", 0}} {
		for _, what := range []string{"clients", "hosts", "internal", "services"} {
			rows, err := st.TopByIface(what, since, 10, true, sel)
			if err != nil || rows == nil || len(rows) != 0 {
				t.Errorf("%v %s: %v %v", sel, what, rows, err)
			}
		}
	}
	if _, err := st.TopByIface("bogus", since, 10, true, IfaceSel{"r1", 10}); err == nil {
		t.Error("an unknown list must still be an error")
	}
}

func TestParseIface(t *testing.T) {
	if s, ok := ParseIface("core:sw:12"); !ok || s.Dev != "core:sw" || s.Idx != 12 {
		t.Errorf("last colon splits: %+v %v", s, ok)
	}
	for _, bad := range []string{"", "x", ":3", "r1:", "r1:-1", "r1:abc", "r1:99999999999"} {
		if _, ok := ParseIface(bad); ok {
			t.Errorf("%q must not parse", bad)
		}
	}
	if (IfaceSel{"r1", 7}).String() != "r1:7" {
		t.Error("token round trip")
	}
}

// Names are resolved when read: a rename shows for old traffic, an unlisted index is shown as if<N>.
func TestIfaceNamesResolvedAtReadTime(t *testing.T) {
	st := ifaceStore(t)
	if st.IfKnown("r1", 10) {
		t.Error("nothing listed yet")
	}
	if !st.IfKnown("r1", 0) {
		t.Error("index 0 (the router itself) is always known")
	}
	if err := st.SetIfNames("r1", []IfName{{1, "ether1", "ether"}, {10, "vlan10", "vlan"}}); err != nil {
		t.Fatal(err)
	}
	n := st.IfNames("r1")
	if IfLabel(n, 10) != "vlan10" || IfLabel(n, 7) != "if7" || IfLabel(n, 0) != "" {
		t.Errorf("labels: %q %q %q", IfLabel(n, 10), IfLabel(n, 7), IfLabel(n, 0))
	}
	if !st.IfKnown("r1", 10) || st.IfKnown("r1", 7) {
		t.Error("known set follows the list")
	}
	if err := st.SetIfNames("r1", []IfName{{1, "ether1", "ether"}, {10, "guest", "vlan"}}); err != nil {
		t.Fatal(err)
	}
	if IfLabel(st.IfNames("r1"), 10) != "guest" {
		t.Error("rename must apply to the stored traffic without rewriting it")
	}
	if err := st.SetIfNames("r1", nil); err != nil || IfLabel(st.IfNames("r1"), 10) != "guest" {
		t.Error("an empty list must not erase the names")
	}
}

// The matrix has only routed traffic between two different interfaces: not the router itself, not same-interface.
func TestInterVLANMatrixExcludesRouter(t *testing.T) {
	st := ifaceStore(t)
	mx, err := st.IfaceMatrix(time.Now().Add(-time.Hour).Unix(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(mx) != 1 || mx[0].From != 10 || mx[0].To != 2 || mx[0].Bytes != 700 {
		t.Fatalf("matrix: %+v", mx)
	}
}

func TestInterfaceTablesResetAndRekey(t *testing.T) {
	st := ifaceStore(t)
	if err := st.RekeyTraffic("aa:00:00:00:00:02", "aa:00:00:00:00:09"); err != nil {
		t.Fatal(err)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM rollup_1h_if WHERE mac='aa:00:00:00:00:02'`).Scan(&n)
	if n != 0 {
		t.Errorf("%d interface rows left under the old key", n)
	}
	if _, err := st.ResetData(ResetOpts{}); err != nil {
		t.Fatal(err)
	}
	st.DB.QueryRow(`SELECT (SELECT count(*) FROM rollup_1h_if)+(SELECT count(*) FROM rollup_1h_ifd)+(SELECT count(*) FROM rollup_1d_ifd)`).Scan(&n)
	if n != 0 {
		t.Errorf("reset left %d interface rows", n)
	}
}
