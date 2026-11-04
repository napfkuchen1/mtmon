// SPDX-License-Identifier: AGPL-3.0-only

package flow

import (
	"encoding/binary"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// Export-packet loss (#294). NetFlow v9 numbers its export packets, IPFIX counts the data records sent before the
// packet. A jump in the sequence number is a loss somewhere between the router and the decoder: router CPU, a
// full UDP buffer, a bridge. Without this, a lossy export looks healthy and all totals are just too low.

// maxSeqGap is the largest jump treated as loss; anything bigger (or backwards) is a restart of the exporter.
const maxSeqGap = 1 << 20

// seqBuckets minutes of history are kept per exporter and domain (the suggestion looks at one hour).
const seqBuckets = 60

type seqKey struct {
	exp    netip.Addr
	domain uint32
}

type seqBucket struct {
	min            int64
	expected, lost uint64
}

type seqState struct {
	init             bool
	nextPkt, nextRec uint32 // expected next sequence number (v9: by packets and by records; IPFIX: nextRec)
	expected, lost   uint64
	resets           uint64
	buckets          [seqBuckets]seqBucket
}

// seqTracker is the sequence accounting of a collector. The zero value is ready to use.
type seqTracker struct {
	mu  sync.Mutex
	st  map[seqKey]*seqState
	now func() time.Time // test clock; nil = time.Now
}

// LossStat is the sequence accounting of one exporter and observation domain.
type LossStat struct {
	Exporter           netip.Addr
	Domain             uint32
	Expected           uint64 // units seen or lost since start (v9 packets, IPFIX records)
	Lost               uint64
	Resets             uint64 // exporter restarts (sequence went back or jumped far)
	Expected1h, Lost1h uint64
}

// Pct1h is the loss over the last hour in percent (0 when nothing was expected).
func (l LossStat) Pct1h() float64 {
	if l.Expected1h == 0 {
		return 0
	}
	return 100 * float64(l.Lost1h) / float64(l.Expected1h)
}

// seqHeader reads version, sequence number, observation domain and (v9) the record count from an export header.
func seqHeader(b []byte) (ver int, seq, domain uint32, count int, ok bool) {
	if len(b) < 4 {
		return
	}
	switch binary.BigEndian.Uint16(b) {
	case 9:
		if len(b) < 20 {
			return
		}
		return 9, binary.BigEndian.Uint32(b[12:]), binary.BigEndian.Uint32(b[16:]), int(binary.BigEndian.Uint16(b[2:])), true
	case 10:
		if len(b) < 16 {
			return
		}
		return 10, binary.BigEndian.Uint32(b[8:]), binary.BigEndian.Uint32(b[12:]), 0, true
	}
	return
}

// observe accounts one decoded packet. nrec is the number of data records the decoder returned; resync is true when
// the packet carried data the decoder could not read (template not known yet): the record count is unknown then, so
// the sequence is re-learned from the next packet instead of booking a loss that is none.
func (t *seqTracker) observe(exp netip.Addr, b []byte, nrec int, resync bool) {
	ver, seq, dom, cnt, ok := seqHeader(b)
	if !ok {
		return
	}
	now := time.Now
	if t.now != nil {
		now = t.now
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.st == nil {
		t.st = map[seqKey]*seqState{}
	}
	k := seqKey{exp, dom}
	s := t.st[k]
	if s == nil {
		if len(t.st) >= 1000 { // spoofed sources must not grow this without limit
			return
		}
		s = &seqState{}
		t.st[k] = s
	}
	var unit uint64 = 1 // delivered units in this packet
	var lost uint64
	switch ver {
	case 10:
		unit = uint64(nrec)
		if resync {
			s.init = false
			return
		}
		if s.init {
			lost = s.gap(int32(seq - s.nextRec))
		}
		s.nextRec = seq + uint32(nrec)
	default: // 9: counts packets (some exporters count records instead: accept either)
		if s.init {
			dp, dr := int32(seq-s.nextPkt), int32(seq-s.nextRec)
			switch {
			case dp == 0 || dr == 0:
			case dp > 0 && dp <= maxSeqGap && (dr <= 0 || dp < dr):
				lost = uint64(dp)
			case dr > 0 && dr <= maxSeqGap:
				lost = uint64(dr)
			default:
				s.resets++
			}
		}
		s.nextPkt, s.nextRec = seq+1, seq+uint32(cnt)
	}
	s.init = true
	if unit+lost == 0 {
		return
	}
	s.expected += unit + lost
	s.lost += lost
	m := now().Unix() / 60
	bk := &s.buckets[m%seqBuckets]
	if bk.min != m {
		*bk = seqBucket{min: m}
	}
	bk.expected += unit + lost
	bk.lost += lost
}

// gap books the difference between the announced and the expected sequence number: forward and small = loss,
// anything else = the exporter restarted.
func (s *seqState) gap(d int32) uint64 {
	switch {
	case d == 0:
		return 0
	case d > 0 && d <= maxSeqGap:
		return uint64(d)
	}
	s.resets++
	return 0
}

// stats returns the accounting of all exporters, sorted by address and domain.
func (t *seqTracker) stats() []LossStat {
	now := time.Now
	if t.now != nil {
		now = t.now
	}
	cut := now().Unix()/60 - seqBuckets
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]LossStat, 0, len(t.st))
	for k, s := range t.st {
		ls := LossStat{Exporter: k.exp, Domain: k.domain, Expected: s.expected, Lost: s.lost, Resets: s.resets}
		for _, b := range s.buckets {
			if b.min > cut {
				ls.Expected1h += b.expected
				ls.Lost1h += b.lost
			}
		}
		out = append(out, ls)
	}
	sort.Slice(out, func(i, j int) bool {
		if c := out[i].Exporter.Compare(out[j].Exporter); c != 0 {
			return c < 0
		}
		return out[i].Domain < out[j].Domain
	})
	return out
}
