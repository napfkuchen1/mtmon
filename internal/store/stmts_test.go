// SPDX-License-Identifier: AGPL-3.0-only

package store

import "testing"

// TestFlushReusesStatements: flushes after the first one reuse the cached statements (no new SQL
// text is prepared) and write the same sums as a plain insert would.
func TestFlushReusesStatements(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	row := func(ts int64, b uint64) FlowRow {
		return FlowRow{TS: ts, Exporter: "e", MAC: "AA:BB:CC:DD:EE:01", CIP: "192.168.0.10", RIP: "203.0.113.9",
			CPort: 40000, RPort: 443, Proto: 6, Dir: "u", Bytes: b, Pkts: 1, Svc: "https"}
	}
	st.Enqueue(row(1700000000, 100), row(1700000001, 50))
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	n := len(st.stmts)
	if n == 0 {
		t.Fatal("no statements cached after first flush")
	}
	st.Enqueue(row(1700000002, 25))
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := len(st.stmts); got != n {
		t.Fatalf("cached statements grew from %d to %d on second flush", n, got)
	}
	var up, flows int64
	if err := st.DB.QueryRow(`SELECT SUM(b_up), SUM(flows) FROM rollup_1m`).Scan(&up, &flows); err != nil {
		t.Fatal(err)
	}
	if up != 175 || flows != 3 {
		t.Fatalf("rollup_1m sums: b_up=%d flows=%d, want 175 and 3", up, flows)
	}
	var raw int64
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM flows`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != 3 {
		t.Fatalf("flows rows = %d, want 3", raw)
	}
	if err := st.FlushFw(); err != nil {
		t.Fatal(err)
	}
}
