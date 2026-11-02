// SPDX-License-Identifier: AGPL-3.0-only

package flow

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

func ipfixHdr(seq, dom uint32) []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint16(b, 10)
	binary.BigEndian.PutUint32(b[8:], seq)
	binary.BigEndian.PutUint32(b[12:], dom)
	return b
}

func v9Hdr(seq, dom uint32, count int) []byte {
	b := make([]byte, 20)
	binary.BigEndian.PutUint16(b, 9)
	binary.BigEndian.PutUint16(b[2:], uint16(count))
	binary.BigEndian.PutUint32(b[12:], seq)
	binary.BigEndian.PutUint32(b[16:], dom)
	return b
}

var exp1 = netip.MustParseAddr("192.0.2.1")

func TestSeqIPFIXLoss(t *testing.T) {
	var tr seqTracker
	tr.observe(exp1, ipfixHdr(100, 0), 10, false) // learns 110
	tr.observe(exp1, ipfixHdr(110, 0), 10, false) // ok, next 120
	tr.observe(exp1, ipfixHdr(125, 0), 10, false) // 5 records lost
	st := tr.stats()
	if len(st) != 1 || st[0].Lost != 5 || st[0].Lost1h != 5 || st[0].Expected != 35 {
		t.Fatalf("stats %+v", st)
	}
}

func TestSeqIPFIXReset(t *testing.T) {
	var tr seqTracker
	tr.observe(exp1, ipfixHdr(5000, 0), 10, false)
	tr.observe(exp1, ipfixHdr(3, 0), 10, false) // exporter rebooted: no loss
	st := tr.stats()
	if st[0].Lost != 0 || st[0].Resets != 1 {
		t.Fatalf("reset: %+v", st)
	}
}

func TestSeqIPFIXResync(t *testing.T) {
	var tr seqTracker
	tr.observe(exp1, ipfixHdr(100, 0), 10, false)
	tr.observe(exp1, ipfixHdr(110, 0), 0, true) // template unknown: record count unknown
	tr.observe(exp1, ipfixHdr(150, 0), 10, false)
	if st := tr.stats(); st[0].Lost != 0 {
		t.Fatalf("resync booked loss: %+v", st)
	}
}

func TestSeqV9Packets(t *testing.T) {
	var tr seqTracker
	tr.observe(exp1, v9Hdr(1, 0, 20), 20, false)
	tr.observe(exp1, v9Hdr(2, 0, 20), 20, false)
	tr.observe(exp1, v9Hdr(5, 0, 20), 20, false) // 2 packets lost
	if st := tr.stats(); st[0].Lost != 2 {
		t.Fatalf("v9 packets: %+v", st)
	}
}

func TestSeqV9RecordCounting(t *testing.T) {
	var tr seqTracker
	tr.observe(exp1, v9Hdr(0, 0, 20), 20, false)
	tr.observe(exp1, v9Hdr(20, 0, 20), 20, false) // record-counting exporter: fine
	if st := tr.stats(); st[0].Lost != 0 || st[0].Resets != 0 {
		t.Fatalf("v9 records: %+v", st)
	}
}

func TestSeqWindowAndDomains(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tr := seqTracker{now: func() time.Time { return now }}
	tr.observe(exp1, ipfixHdr(0, 1), 10, false)
	tr.observe(exp1, ipfixHdr(30, 1), 10, false) // 20 lost
	tr.observe(exp1, ipfixHdr(0, 2), 10, false)
	if st := tr.stats(); len(st) != 2 || st[0].Domain != 1 || st[0].Lost1h != 20 || st[1].Lost != 0 {
		t.Fatalf("domains: %+v", st)
	}
	now = now.Add(2 * time.Hour)
	if st := tr.stats(); st[0].Lost1h != 0 || st[0].Lost != 20 {
		t.Fatalf("window: %+v", st)
	}
}

