// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"testing"
	"time"
)

// Advisory: one flow record with a byte counter of 2^63 or more must not stall flow storage.
func TestImplausibleFlowRowRefused(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	good := FlowRow{TS: now, MAC: "AA:00:00:00:00:01", CIP: "192.168.1.2", RIP: "1.1.1.1", RPort: 443, Proto: 6, Dir: "u", Bytes: 1000, Pkts: 2}
	bad := good
	bad.Bytes = 1 << 63
	badPkts := good
	badPkts.Pkts = 1 << 62
	st.Enqueue(good, bad, badPkts)
	if st.QueueLen() != 1 || st.Implausible != 2 {
		t.Fatalf("queue %d implausible %d", st.QueueLen(), st.Implausible)
	}
	if err := st.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM flows`).Scan(&n)
	if n != 1 || st.QueueLen() != 0 {
		t.Fatalf("stored %d, queue %d", n, st.QueueLen())
	}
	edge := good
	edge.Bytes = maxRowBytes
	st.Enqueue(edge)
	if st.QueueLen() != 1 {
		t.Error("the plausibility limit itself is still accepted")
	}
}

// A flush that fails for good gives up after maxTries instead of requeueing the same batch forever.
func TestFailingFlushDoesNotRequeueForever(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.Enqueue(FlowRow{TS: time.Now().Unix(), CIP: "192.168.1.2", RIP: "1.1.1.1", Dir: "u", Bytes: 1, Pkts: 1})
	st.DB.Close() // every flush fails from now on
	for i := 0; i < maxTries; i++ {
		if err := st.Flush(); err == nil {
			t.Fatal("flush on a closed database must fail")
		}
	}
	if st.QueueLen() != 0 || st.Dropped != 1 {
		t.Fatalf("after %d failed flushes: queue %d dropped %d", maxTries, st.QueueLen(), st.Dropped)
	}
}
