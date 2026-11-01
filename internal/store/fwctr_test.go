// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"testing"
	"time"
)

// Counter based hits (#278): the first value is a baseline, later values add the difference, a lower value means a
// reset (reboot, rule edit) and counts from zero – never a negative delta.
func TestAddFwCountersBaselineAndReset(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	st.SaveFwRules([]FwRule{{Device: "gw", Prefix: "MTM-1", Chain: "forward", Action: "accept", Descr: "LAN to internet", Managed: true, Counter: true}})
	add := func(v int64) int64 {
		n, err := st.AddFwCounters("gw", now, map[string]int64{"MTM-1": v})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	for i, c := range []struct{ cur, want int64 }{{1000, 0}, {1040, 40}, {1040, 0}, {7, 7}, {30, 23}} {
		if got := add(c.cur); got != c.want {
			t.Fatalf("step %d: counter %d added %d, want %d", i, c.cur, got, c.want)
		}
	}
	sum, err := st.FwSummary(now - 3600)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Allowed != 70 || sum.AllowedCounted != 70 || len(sum.Rules) != 1 || !sum.Rules[0].Counter {
		t.Fatalf("summary %+v", sum)
	}
	if !st.FwCounterPrefixes("gw")["MTM-1"] || len(st.FwCounterPrefixes("other")) != 0 {
		t.Fatal("counter prefixes")
	}
	if rs, _ := st.FwRules(); len(rs) != 1 || !rs[0].Counter {
		t.Fatalf("rules %+v", rs)
	}
}
