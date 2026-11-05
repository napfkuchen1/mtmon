// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"testing"
	"time"
)

func TestFwSummaryTopListsFromHourlyAggregates(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	st.SaveFwRules([]FwRule{{Device: "gw", Prefix: "drop", Chain: "input", Action: "drop"}, {Device: "gw", Prefix: "acc", Chain: "forward", Action: "accept"}})
	var es []FwEvent
	for i := 0; i < 5; i++ {
		es = append(es, FwEvent{TS: now - 60, Device: "gw", Prefix: "drop", Src: "198.51.100.7", Dst: "203.0.113.2", DPort: 22})
	}
	es = append(es, FwEvent{TS: now - 60, Device: "gw", Prefix: "drop", Src: "198.51.100.8", Dst: "203.0.113.2", DPort: 23},
		FwEvent{TS: now - 60, Device: "gw", Prefix: "acc", Src: "198.51.100.9", Dst: "203.0.113.2", DPort: 443}, // allowed: not a blocked source
		FwEvent{TS: now - 10*86400, Device: "gw", Prefix: "drop", Src: "198.51.100.10", Dst: "203.0.113.2", DPort: 22})
	st.EnqueueFw(es...)
	if err := st.FlushFw(); err != nil {
		t.Fatal(err)
	}
	check := func(st *Store) {
		t.Helper()
		sum, err := st.FwSummary(now - 86400)
		if err != nil {
			t.Fatal(err)
		}
		if len(sum.TopSrc) != 2 || sum.TopSrc[0].Key != "198.51.100.7" || sum.TopSrc[0].Hits != 5 || sum.TopSrc[1].Key != "198.51.100.8" {
			t.Errorf("top sources: %+v", sum.TopSrc)
		}
		if len(sum.TopDst) != 2 || sum.TopDst[0].Port != 22 || sum.TopDst[0].Hits != 5 {
			t.Errorf("top destinations: %+v", sum.TopDst)
		}
	}
	check(st)
	// a database from before the aggregates existed is filled from its events on open
	st.DB.Exec(`DROP TABLE fw_src_hourly`)
	st.DB.Exec(`DROP TABLE fw_dst_hourly`)
	st.Close()
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	check(st)
}

func TestPurgeBadFwAddrs(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	st.EnqueueFw(
		FwEvent{TS: now, Device: "gw", Prefix: "drop", Src: "2001:db8::7", Dst: "203.0.113.2", DPort: 22},
		FwEvent{TS: now, Device: "gw", Prefix: "drop", Src: "[2001:db8::8", Dst: "203.0.113.2", DPort: 22})
	if err := st.FlushFw(); err != nil {
		t.Fatal(err)
	}
	purgeBadFwAddrs(st.DB)
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM fw_events`).Scan(&n)
	if n != 1 {
		t.Errorf("events after purge: %d, want 1", n)
	}
}

// Rows of rules mtmon took back leave "Rules by hits"; a rule still on the router and the owner's own prefixes stay (#343).
func TestFwSummaryDropsOrphanedMtmPrefixes(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	st.SaveFwRules([]FwRule{{Device: "gw", Prefix: "MTM-1", Chain: "input", Action: "drop", Managed: true}})
	st.AddManifest("gw", ManifestEntry{Seq: 1, Descr: "Logging on rule", Path: "/ip/firewall/filter", RID: "*2"})   // apply failed, undo refused: no label
	st.AddManifest("gw", ManifestEntry{Seq: 2, Descr: "Logging on rule", Path: "/ipv6/firewall/filter", RID: "*3"}) // the IPv6 counterpart
	st.AddManifest("gw", ManifestEntry{Seq: 3, Descr: "Rule: log new connections (MTM-NEW)", Path: "/ip/firewall/filter", RID: "*9"})
	st.AddManifest("gw", ManifestEntry{Seq: 4, Descr: "Logging on rule", Path: "/ip/firewall/filter", RID: "*33"})
	es, _ := st.Manifest("gw")
	st.SetManifestState(es[0].ID, "revert-failed")
	st.SetManifestState(es[3].ID, "reverted") // taken back: MTM-33 is gone
	var ev []FwEvent
	for _, p := range []string{"MTM-1", "MTM-2", "MTM6-3", "MTM-NEW", "MTM-33", "MTM6-33", "my-prefix"} {
		ev = append(ev, FwEvent{TS: now - 60, Device: "gw", Prefix: p, Src: "198.51.100.7", Dst: "203.0.113.2", DPort: 22})
	}
	ev = append(ev, FwEvent{TS: now - 60, Device: "other", Prefix: "MTM-1", Src: "198.51.100.7", Dst: "203.0.113.2", DPort: 22}) // ids are per router
	st.EnqueueFw(ev...)
	if err := st.FlushFw(); err != nil {
		t.Fatal(err)
	}
	sum, err := st.FwSummary(now - 3600)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range sum.Rules {
		got[r.Device+"/"+r.Prefix] = true
	}
	for _, p := range []string{"gw/MTM-1", "gw/MTM-2", "gw/MTM6-3", "gw/MTM-NEW", "gw/my-prefix"} {
		if !got[p] {
			t.Errorf("%s dropped: %v", p, got)
		}
	}
	for _, p := range []string{"gw/MTM-33", "gw/MTM6-33", "other/MTM-1"} {
		if got[p] {
			t.Errorf("%s still listed: %v", p, got)
		}
	}
	if sum.Blocked+sum.Allowed+sum.Logged != 8 {
		t.Errorf("past hits leave the totals: %+v", sum)
	}
}
