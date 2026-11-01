// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"testing"
	"time"
)

// mtmon's own traffic (speed test, monitoring of the routers) stays in the data but is left out of the top lists, the
// totals and the series unless it is asked for – in every rollup tier the lists read from.
func TestOwnTrafficLeftOutOfTopListsAndTotals(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	const user, host = "AA:00:00:00:00:01", "AA:00:00:00:00:02"
	st.Enqueue(
		FlowRow{TS: now, MAC: user, CIP: "192.168.1.5", RIP: "203.0.113.9", RPort: 443, Proto: 6, Dir: "d", Bytes: 1000, Svc: "HTTPS", CC: "DE"},
		FlowRow{TS: now, MAC: host, CIP: "192.168.1.9", RIP: "198.51.100.7", RPort: 443, Proto: 6, Dir: "d", Bytes: 50000, Svc: SpeedtestSvc, CC: "US"},
		FlowRow{TS: now, MAC: host, CIP: "192.168.1.9", RIP: "192.168.1.1", RPort: 8729, Proto: 6, Dir: "u", Bytes: 7000, Svc: MonitorSvc},
	)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, since := range []int64{now - 600, now - 5*3600, now - 3*86400, now - 20*86400} {
		for _, what := range []string{"hosts", "services", "country", "ports", "protocols", "asn"} {
			rows, err := st.TopBy(what, since, 10, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if r.Key == "198.51.100.7" || r.Key == "US" || r.Key == SpeedtestSvc || r.Key == MonitorSvc || r.Key == "192.168.1.1" || r.Key == "6/8729" {
					t.Errorf("%s since %d: own traffic in the list: %+v", what, now-since, r)
				}
			}
			all, _ := st.TopByOwn(what, since, 10, "", true)
			if what == "country" && len(all) != 2 {
				t.Errorf("country with own traffic since %d: %+v", now-since, all)
			}
			if what == "services" && len(all) != 3 {
				t.Errorf("services with own traffic since %d: %+v", now-since, all)
			}
		}
		cl, _ := st.TopBy("clients", since, 10, "")
		if len(cl) != 1 || cl[0].Key != user {
			t.Errorf("clients since %d: %+v", now-since, cl)
		}
		cl, _ = st.TopByOwn("clients", since, 10, "", true)
		if len(cl) != 2 || cl[0].Key != host {
			t.Errorf("clients with own traffic since %d: %+v", now-since, cl)
		}
		if up, down, _ := st.Totals(since); up != 0 || down != 1000 {
			t.Errorf("totals since %d: %d/%d", now-since, up, down)
		}
		if up, down, _ := st.TotalsOwn(since, true); up != 7000 || down != 51000 {
			t.Errorf("totals with own traffic since %d: %d/%d", now-since, up, down)
		}
	}
	// a single client's page is never filtered
	if rows, _ := st.TopBy("hosts", now-600, 10, host); len(rows) != 2 {
		t.Errorf("host page: %+v", rows)
	}
}
