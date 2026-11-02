// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"
)

// FlowRow is an enriched, client-attributed flow ready for storage.
type FlowRow struct {
	TS       int64
	Exporter string
	MAC      string // may be empty when IP is unknown
	CIP      string
	RIP      string
	CPort    uint16
	RPort    uint16
	Proto    uint8
	Dir      string // u = upload (client→remote), d = download, i = internal
	Bytes    uint64
	Pkts     uint64
	Flags    uint8
	Host     string
	CC       string
	ASN      uint32
	ASOrg    string
	Svc      string
	tries    uint8 // failed flushes this row was part of (requeue)
}

const (
	maxQueue = 200000
	// One flow record never carries more than this: 2^44 bytes (~17.6 TB) would be ~78 Gbit/s for a whole 30 min
	// active timeout. Larger values come from a broken or spoofed exporter and would overflow the 64-bit sums.
	maxRowBytes = 1 << 44
	maxRowPkts  = 1 << 40
	// A batch that keeps failing is dropped after this many attempts instead of blocking all later writes.
	maxTries = 3
)

// Enqueue adds rows to the write queue (non-blocking; drops when overloaded). Implausible rows are counted and
// discarded here, so they can never make a flush fail.
func (s *Store) Enqueue(rows ...FlowRow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ok := rows[:0:0]
	for _, r := range rows {
		if r.Bytes > maxRowBytes || r.Pkts > maxRowPkts {
			s.Implausible++
			continue
		}
		ok = append(ok, r)
	}
	if len(s.flowQ)+len(ok) > maxQueue {
		s.Dropped += uint64(len(ok))
		return
	}
	s.flowQ = append(s.flowQ, ok...)
}

// QueueLen is exported for self-monitoring.
func (s *Store) QueueLen() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.flowQ) }

// Writer flushes the queue every second until ctx is done (flushes once more on exit).
func (s *Store) Writer(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Flush()
			s.FlushFw()
			return
		case <-t.C:
			s.Flush()
			s.FlushFw()
		}
	}
}

type aggKey struct {
	ts    int64
	mac   string
	rip   string
	rport uint16
	proto uint8
}

type aggVal struct {
	up, down, internal uint64
	ownUp, ownDown     uint64 // part of up/down that is mtmon's own traffic
	ownInt             uint64 // part of internal that is mtmon's own traffic
	flows              int64
	svc, host, cc, org string
	asn                uint32
}

func (v *aggVal) add(r FlowRow) {
	switch r.Dir {
	case "u":
		v.up += r.Bytes
		if IsOwnSvc(r.Svc) {
			v.ownUp += r.Bytes
		}
	case "d":
		v.down += r.Bytes
		if IsOwnSvc(r.Svc) {
			v.ownDown += r.Bytes
		}
	default:
		v.internal += r.Bytes
		if IsOwnSvc(r.Svc) {
			v.ownInt += r.Bytes
		}
	}
	v.flows++
	if v.svc == "" {
		v.svc = r.Svc
	}
	if r.Host != "" {
		v.host = r.Host
	}
	if v.cc == "" {
		v.cc, v.asn, v.org = r.CC, r.ASN, r.ASOrg
	}
}

func agg(m map[aggKey]*aggVal, k aggKey, r FlowRow) {
	v := m[k]
	if v == nil {
		v = &aggVal{}
		m[k] = v
	}
	v.add(r)
}

const upsertTail = ` ON CONFLICT DO UPDATE SET b_up=b_up+excluded.b_up, b_down=b_down+excluded.b_down,
  b_int=b_int+excluded.b_int, flows=flows+excluded.flows`

// Flush writes all queued rows in one transaction. Rollups are pre-aggregated in memory first, so a
// burst of thousands of flows for the same client/destination costs one upsert per table.
func (s *Store) Flush() error {
	s.mu.Lock()
	rows := s.flowQ
	s.flowQ = nil
	s.mu.Unlock()
	if len(rows) == 0 {
		return nil
	}
	m1, h1, d1 := map[aggKey]*aggVal{}, map[aggKey]*aggVal{}, map[aggKey]*aggVal{}
	hc := map[aggKey]*aggVal{}
	type sk struct{ mac, rip string }
	seen := map[sk]int64{}
	for _, r := range rows {
		key := r.MAC
		if key == "" {
			key = r.CIP
		}
		agg(m1, aggKey{r.TS / 60 * 60, key, r.RIP, r.RPort, r.Proto}, r)
		agg(h1, aggKey{r.TS / 3600 * 3600, key, r.RIP, r.RPort, r.Proto}, r)
		agg(hc, aggKey{ts: r.TS / 3600 * 3600, mac: key}, r)
		agg(d1, aggKey{ts: r.TS / 86400 * 86400, rip: r.RIP, rport: r.RPort, proto: r.Proto}, r)
		if r.Dir == "u" && !IsOwnSvc(r.Svc) {
			if _, ok := seen[sk{key, r.RIP}]; !ok {
				seen[sk{key, r.RIP}] = r.TS
			}
		}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		s.requeue(rows)
		return err
	}
	fail := func(err error) error { tx.Rollback(); s.requeue(rows); return err }
	ins, err := tx.Prepare(`INSERT INTO flows(ts,exporter,mac,cip,rip,cport,rport,proto,dir,bytes,pkts,flags,host,cc,asn,asorg,svc)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fail(err)
	}
	defer ins.Close()
	for _, r := range rows {
		if _, err := ins.Exec(r.TS, r.Exporter, r.MAC, r.CIP, r.RIP, r.CPort, r.RPort, r.Proto, r.Dir, r.Bytes, r.Pkts, r.Flags,
			r.Host, r.CC, r.ASN, r.ASOrg, r.Svc); err != nil {
			return fail(err)
		}
	}
	full := func(table string, m map[aggKey]*aggVal) error {
		st, err := tx.Prepare(`INSERT INTO ` + table + `(ts,mac,rip,rport,proto,svc,host,cc,asn,asorg,b_up,b_down,b_int,flows)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)` + upsertTail + `, host=CASE WHEN excluded.host<>'' THEN excluded.host ELSE host END`)
		if err != nil {
			return err
		}
		defer st.Close()
		for k, v := range m {
			if _, err := st.Exec(k.ts, k.mac, k.rip, k.rport, k.proto, v.svc, v.host, v.cc, v.asn, v.org, v.up, v.down, v.internal, v.flows); err != nil {
				return err
			}
		}
		return nil
	}
	if err := full("rollup_1m", m1); err != nil {
		return fail(err)
	}
	if err := full("rollup_1h", h1); err != nil {
		return fail(err)
	}
	stc, err := tx.Prepare(`INSERT INTO rollup_1h_client(ts,mac,b_up,b_down,b_int,flows,o_up,o_down,o_int) VALUES(?,?,?,?,?,?,?,?,?)` + upsertTail +
		`, o_up=o_up+excluded.o_up, o_down=o_down+excluded.o_down, o_int=o_int+excluded.o_int`)
	if err != nil {
		return fail(err)
	}
	defer stc.Close()
	for k, v := range hc {
		if _, err := stc.Exec(k.ts, k.mac, v.up, v.down, v.internal, v.flows, v.ownUp, v.ownDown, v.ownInt); err != nil {
			return fail(err)
		}
	}
	std, err := tx.Prepare(`INSERT INTO rollup_1d(ts,rip,rport,proto,svc,host,cc,asn,asorg,b_up,b_down,b_int,flows) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)` +
		upsertTail + `, host=CASE WHEN excluded.host<>'' THEN excluded.host ELSE host END`)
	if err != nil {
		return fail(err)
	}
	defer std.Close()
	for k, v := range d1 {
		if _, err := std.Exec(k.ts, k.rip, k.rport, k.proto, v.svc, v.host, v.cc, v.asn, v.org, v.up, v.down, v.internal, v.flows); err != nil {
			return fail(err)
		}
	}
	if err := writeDims(tx, d1); err != nil {
		return fail(err)
	}
	sd, err := tx.Prepare(`INSERT OR IGNORE INTO seen_dest(mac,rip,first) VALUES(?,?,?)`)
	if err != nil {
		return fail(err)
	}
	defer sd.Close()
	for k, ts := range seen {
		sd.Exec(k.mac, k.rip, ts)
	}
	if err := tx.Commit(); err != nil {
		s.requeue(rows)
		return err
	}
	return nil
}

// requeue puts the rows of a failed flush back at the head of the queue for the next attempt. Rows that already
// failed maxTries times are dropped (and counted): a batch that fails for good must not block every later write.
func (s *Store) requeue(rows []FlowRow) {
	keep := rows[:0]
	var gaveUp uint64
	for _, r := range rows {
		r.tries++
		if r.tries >= maxTries {
			gaveUp++
			continue
		}
		keep = append(keep, r)
	}
	s.mu.Lock()
	s.Dropped += gaveUp
	if len(s.flowQ)+len(keep) <= maxQueue {
		s.flowQ = append(keep, s.flowQ...)
	} else {
		s.Dropped += uint64(len(keep))
	}
	s.mu.Unlock()
}

// DroppedCount returns rows discarded because the write queue was full.
func (s *Store) DroppedCount() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.Dropped }
