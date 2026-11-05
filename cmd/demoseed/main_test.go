// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// The client lists and the destination lists read different rollups; the demo has to fill both with the same bytes.
func TestSeedFillsClientAndDestinationRollups(t *testing.T) {
	dir := t.TempDir()
	seed(dir)
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sum := func(table string, since int64) (n int64) {
		t.Helper()
		if err := st.DB.QueryRow(`SELECT coalesce(sum(b_down),0) FROM `+table+` WHERE ts>=?`, since).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	since := time.Now().Add(-24*time.Hour).Unix() / 3600 * 3600
	hourly, clients := sum("rollup_1h", since), sum("rollup_1h_client", since)
	if hourly == 0 || clients != hourly {
		t.Errorf("client rollup %d bytes, hourly rollup %d bytes", clients, hourly)
	}
	// the KPIs read the minute rollup, the lists the hourly one: they have to tell the same story
	if minute := sum("rollup_1m", since); float64(hourly) < 0.6*float64(minute) || float64(hourly) > 1.6*float64(minute) {
		t.Errorf("hourly rollup %d bytes, minute rollup %d bytes for the same day", hourly, minute)
	}
	if daily := sum("rollup_1d", 0); daily == 0 {
		t.Error("daily rollup is empty")
	}
	top, err := st.TopBy("clients", since, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 5 {
		t.Errorf("top clients: %d rows", len(top))
	}
}
