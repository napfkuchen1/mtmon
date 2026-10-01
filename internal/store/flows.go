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
}

const maxQueue = 200000

// Enqueue adds rows to the write queue (non-blocking; drops when overloaded).
func (s *Store) Enqueue(rows ...FlowRow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.flowQ)+len(rows) > maxQueue {
		s.Dropped += uint64(len(rows))
		return
	}
	s.flowQ = append(s.flowQ, rows...)
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
	flows              int64
	svc, host, cc, org string
	asn                uint32
}

func (v *aggVal) add(r FlowRow) {
	switch r.Dir {
	case "u":
		v.up += r.Bytes
	case "d":
		v.down += r.Bytes
	default:
		v.internal += r.Bytes
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
		if r.Dir == "u" {
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
	stc, err := tx.Prepare(`INSERT INTO rollup_1h_client(ts,mac,b_up,b_down,b_int,flows) VALUES(?,?,?,?,?,?)` + upsertTail)
	if err != nil {
		return fail(err)
	}
	defer stc.Close()
	for k, v := range hc {
		if _, err := stc.Exec(k.ts, k.mac, v.up, v.down, v.internal, v.flows); err != nil {
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

func (s *Store) requeue(rows []FlowRow) {
	s.mu.Lock()
	if len(s.flowQ)+len(rows) <= maxQueue {
		s.flowQ = append(rows, s.flowQ...)
	} else {
		s.Dropped += uint64(len(rows))
	}
	s.mu.Unlock()
}

// DroppedCount returns rows discarded because the write queue was full.
func (s *Store) DroppedCount() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.Dropped }
