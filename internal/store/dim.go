package store

import (
	"database/sql"
	"strconv"
)

// rollup_1d_dim: daily totals per service, country, AS, port and protocol (a few hundred rows a day). The
// per-destination daily table has tens of thousands of rows a day on a busy network; lists that only need one of
// these dimensions over weeks read this table instead.
const dimSchema = `
CREATE TABLE IF NOT EXISTS rollup_1d_dim(
  ts INTEGER, dim TEXT, k TEXT, label TEXT, b_up INTEGER, b_down INTEGER, b_int INTEGER, flows INTEGER,
  PRIMARY KEY(ts, dim, k)) WITHOUT ROWID;`

// dimOf maps a TopBy list to its dimension in rollup_1d_dim.
var dimOf = map[string]string{"services": "svc", "country": "cc", "asn": "asn", "ports": "port", "protocols": "proto"}

// dimExprs derive (dim, key, label) from a rollup_1d row; shared by the backfill so both agree.
const dimBackfill = `INSERT OR IGNORE INTO rollup_1d_dim(ts,dim,k,label,b_up,b_down,b_int,flows)
SELECT ts,'svc',svc,svc,sum(b_up),sum(b_down),sum(b_int),sum(flows) FROM rollup_1d WHERE svc<>'' GROUP BY ts,svc
UNION ALL SELECT ts,'cc',cc,cc,sum(b_up),sum(b_down),sum(b_int),sum(flows) FROM rollup_1d WHERE cc<>'' GROUP BY ts,cc
UNION ALL SELECT ts,'asn',cast(asn as text),coalesce(max(asorg),''),sum(b_up),sum(b_down),sum(b_int),sum(flows) FROM rollup_1d WHERE asn>0 GROUP BY ts,asn
UNION ALL SELECT ts,'port',proto||'/'||rport,coalesce(max(svc),''),sum(b_up),sum(b_down),sum(b_int),sum(flows) FROM rollup_1d GROUP BY ts,proto,rport
UNION ALL SELECT ts,'proto',cast(proto as text),'',sum(b_up),sum(b_down),sum(b_int),sum(flows) FROM rollup_1d GROUP BY ts,proto`

// migrateDim creates the table and, when it is new, fills it from the daily destination rollups.
func migrateDim(db *sql.DB) error {
	var have int
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='rollup_1d_dim'`).Scan(&have)
	if _, err := db.Exec(dimSchema); err != nil {
		return err
	}
	if have > 0 {
		return nil
	}
	_, err := db.Exec(dimBackfill)
	return err
}

type dimKey struct {
	ts       int64
	dim, key string
}

// writeDims adds the daily per-destination aggregates of one flush to rollup_1d_dim.
func writeDims(tx *sql.Tx, d1 map[aggKey]*aggVal) error {
	m := map[dimKey]*aggVal{}
	label := map[dimKey]string{}
	add := func(ts int64, dim, key, lab string, v *aggVal) {
		k := dimKey{ts, dim, key}
		a := m[k]
		if a == nil {
			a = &aggVal{}
			m[k] = a
		}
		a.up += v.up
		a.down += v.down
		a.internal += v.internal
		a.flows += v.flows
		if lab != "" {
			label[k] = lab
		}
	}
	for k, v := range d1 {
		if v.svc != "" {
			add(k.ts, "svc", v.svc, v.svc, v)
		}
		if v.cc != "" {
			add(k.ts, "cc", v.cc, v.cc, v)
		}
		if v.asn > 0 {
			add(k.ts, "asn", strconv.FormatUint(uint64(v.asn), 10), v.org, v)
		}
		add(k.ts, "port", strconv.Itoa(int(k.proto))+"/"+strconv.Itoa(int(k.rport)), v.svc, v)
		add(k.ts, "proto", strconv.Itoa(int(k.proto)), "", v)
	}
	st, err := tx.Prepare(`INSERT INTO rollup_1d_dim(ts,dim,k,label,b_up,b_down,b_int,flows) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT DO UPDATE SET b_up=b_up+excluded.b_up, b_down=b_down+excluded.b_down, b_int=b_int+excluded.b_int,
		flows=flows+excluded.flows, label=CASE WHEN excluded.label<>'' THEN excluded.label ELSE label END`)
	if err != nil {
		return err
	}
	defer st.Close()
	for k, v := range m {
		if _, err := st.Exec(k.ts, k.dim, k.key, label[k], v.up, v.down, v.internal, v.flows); err != nil {
			return err
		}
	}
	return nil
}

// topByDim answers TopBy for services/country/asn/ports/protocols over day-aligned windows of 8 days and more.
func (s *Store) topByDim(what string, since int64, limit int) ([]TopRow, error) {
	rows, err := s.DB.Query(`SELECT k, CASE WHEN ?1='asn' THEN coalesce(nullif(max(label),''),'AS'||k) WHEN ?1='port' THEN k ELSE k END,
		CASE WHEN ?1='port' THEN coalesce(max(label),'') ELSE '' END, sum(b_up), sum(b_down), sum(b_int), sum(flows)
		FROM rollup_1d_dim WHERE dim=?1 AND ts>=?2 GROUP BY k ORDER BY sum(b_up)+sum(b_down)+sum(b_int) DESC LIMIT ?3`,
		dimOf[what], since/86400*86400, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TopRow{}
	for rows.Next() {
		var t TopRow
		if err := rows.Scan(&t.Key, &t.Label, &t.Sub, &t.Up, &t.Down, &t.Internal, &t.Flows); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// dimSums returns per-service totals over day-aligned windows (Services page, 8 days and more).
func (s *Store) svcSumsDim(since int64) (map[string]ServiceRow, error) {
	rows, err := s.DB.Query(`SELECT k, sum(b_up), sum(b_down), sum(b_int), sum(flows) FROM rollup_1d_dim WHERE dim='svc' AND ts>=? GROUP BY k`, since/86400*86400)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ServiceRow{}
	for rows.Next() {
		var r ServiceRow
		if err := rows.Scan(&r.Name, &r.Up, &r.Down, &r.Int, &r.Flows); err != nil {
			return nil, err
		}
		out[r.Name] = r
	}
	return out, rows.Err()
}
