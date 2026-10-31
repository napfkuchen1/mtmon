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
