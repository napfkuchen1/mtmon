// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Per-interface (VLAN) view. The flow exporter reports the SNMP ifIndex of the ingress and egress interface of every
// flow. mtmon stores that index next to the rollups in tables of their own (adding a column to the primary key of
// the existing rollups is not possible without a rebuild, and old versions must still start on the same database).
// The index is stored, never the interface name: the name is resolved at read time from if_names, so a renamed
// VLAN shows up under its new name for the old traffic as well. Index 0 means the router itself.
//
//	rollup_1h_if   per hour, device, client-side interface (ifc), remote-side interface (ifr) and client
//	rollup_1h_ifd  the same per destination (for the top lists), plus a daily tier for long windows
//	if_names       ifIndex -> name/type per device, refreshed by the poller
const ifaceSchema = `
CREATE TABLE IF NOT EXISTS rollup_1h_if(
  ts INTEGER, dev TEXT, ifc INTEGER, ifr INTEGER, mac TEXT,
  b_up INTEGER, b_down INTEGER, b_int INTEGER, flows INTEGER,
  o_up INTEGER DEFAULT 0, o_down INTEGER DEFAULT 0, o_int INTEGER DEFAULT 0,
  PRIMARY KEY(ts, dev, ifc, ifr, mac)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS rollup_1h_ifd(
  ts INTEGER, dev TEXT, ifc INTEGER, ifr INTEGER, rip TEXT, rport INTEGER, proto INTEGER,
  svc TEXT, host TEXT, cc TEXT, asn INTEGER, asorg TEXT,
  b_up INTEGER, b_down INTEGER, b_int INTEGER, flows INTEGER,
  PRIMARY KEY(ts, dev, ifc, ifr, rip, rport, proto)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS rollup_1d_ifd(
  ts INTEGER, dev TEXT, ifc INTEGER, ifr INTEGER, rip TEXT, rport INTEGER, proto INTEGER,
  svc TEXT, host TEXT, cc TEXT, asn INTEGER, asorg TEXT,
  b_up INTEGER, b_down INTEGER, b_int INTEGER, flows INTEGER,
  PRIMARY KEY(ts, dev, ifc, ifr, rip, rport, proto)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS if_names(
  dev TEXT, idx INTEGER, name TEXT, type TEXT, seen INTEGER, PRIMARY KEY(dev, idx)) WITHOUT ROWID;`

func migrateIface(db *sql.DB) error {
	_, err := db.Exec(ifaceSchema)
	return err
}

// IfName is one interface of a device as the router lists it.
type IfName struct {
	Idx  uint32 `json:"idx"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// IfLabel is what the UI shows for an interface index: the current name, "" for the router itself (index 0, the UI
// translates it) and "if<N>" for an index the device does not (yet) list. A name is never guessed.
func IfLabel(names map[uint32]IfName, idx uint32) string {
	if idx == 0 {
		return ""
	}
	if n, ok := names[idx]; ok && n.Name != "" {
		return n.Name
	}
	return "if" + strconv.FormatUint(uint64(idx), 10)
}

// SetIfNames replaces the interface list of a device (the poller calls it with the full /interface list).
// An empty list is ignored: a failed or empty poll must not erase the names.
func (s *Store) SetIfNames(dev string, list []IfName) error {
	if dev == "" || len(list) == 0 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM if_names WHERE dev=?`, dev); err != nil {
		tx.Rollback()
		return err
	}
	now := time.Now().Unix()
	known := map[uint32]bool{0: true}
	for _, n := range list {
		if n.Idx == 0 {
			continue
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO if_names(dev,idx,name,type,seen) VALUES(?,?,?,?,?)`, dev, n.Idx, n.Name, n.Type, now); err != nil {
			tx.Rollback()
			return err
		}
		known[n.Idx] = true
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.ifMu.Lock()
	if s.ifKnown == nil {
		s.ifKnown = map[string]map[uint32]bool{}
	}
	s.ifKnown[dev] = known
	s.ifMu.Unlock()
	return nil
}

// IfNames returns the interface list of a device, keyed by index.
func (s *Store) IfNames(dev string) map[uint32]IfName {
	out := map[uint32]IfName{}
	rows, err := s.DB.Query(`SELECT idx, name, coalesce(type,'') FROM if_names WHERE dev=?`, dev)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n IfName
		if rows.Scan(&n.Idx, &n.Name, &n.Type) == nil {
			out[n.Idx] = n
		}
	}
	return out
}

// IfKnown reports whether the device lists this interface index (index 0, the router itself, always counts).
func (s *Store) IfKnown(dev string, idx uint32) bool {
	if idx == 0 {
		return true
	}
	s.ifMu.Lock()
	k, ok := s.ifKnown[dev]
	s.ifMu.Unlock()
	if !ok {
		k = map[uint32]bool{0: true}
		for i := range s.IfNames(dev) {
			k[i] = true
		}
		s.ifMu.Lock()
		if s.ifKnown == nil {
			s.ifKnown = map[string]map[uint32]bool{}
		}
		s.ifKnown[dev] = k
		s.ifMu.Unlock()
	}
	return k[idx]
}

// IfaceSel selects one interface of one device in the API ("dev:idx", split at the last colon).
type IfaceSel struct {
	Dev string
	Idx uint32
}

// ParseIface parses a ?iface= value. Empty or malformed input is not an error for the callers: ok=false means
// "no filter requested or unusable", and the lists then simply are empty or unfiltered as the caller decides.
func ParseIface(v string) (sel IfaceSel, ok bool) {
	i := strings.LastIndexByte(v, ':')
	if i <= 0 || i == len(v)-1 {
		return sel, false
	}
	n, err := strconv.ParseUint(v[i+1:], 10, 32)
	if err != nil {
		return sel, false
	}
	return IfaceSel{Dev: v[:i], Idx: uint32(n)}, true
}

// String is the token ParseIface reads back.
func (i IfaceSel) String() string { return i.Dev + ":" + strconv.FormatUint(uint64(i.Idx), 10) }

// ifKey is the aggregation key of the interface tables; unused parts stay zero.
type ifKey struct {
	ts       int64
	dev      string
	ifc, ifr uint32
	mac      string
	rip      string
	rport    uint16
	proto    uint8
}

func ifAgg(m map[ifKey]*aggVal, k ifKey, r FlowRow) {
	v := m[k]
	if v == nil {
		v = &aggVal{}
		m[k] = v
	}
	v.add(r)
}

// writeIface adds the interface aggregates of one flush. Rows without an exporter interface (older flow sources,
// or both indexes 0) are skipped: there is nothing to attribute.
func writeIface(tx *sql.Tx, rows []FlowRow) error {
	hi := map[ifKey]*aggVal{}
	hd := map[ifKey]*aggVal{}
	dd := map[ifKey]*aggVal{}
	for _, r := range rows {
		if r.IfC == 0 && r.IfR == 0 {
			continue
		}
		dev := r.Dev
		if dev == "" {
			dev = r.Exporter
		}
		key := r.MAC
		if key == "" {
			key = r.CIP
		}
		h := r.TS / 3600 * 3600
		ifAgg(hi, ifKey{ts: h, dev: dev, ifc: r.IfC, ifr: r.IfR, mac: key}, r)
		ifAgg(hd, ifKey{ts: h, dev: dev, ifc: r.IfC, ifr: r.IfR, rip: r.RIP, rport: r.RPort, proto: r.Proto}, r)
		ifAgg(dd, ifKey{ts: r.TS / 86400 * 86400, dev: dev, ifc: r.IfC, ifr: r.IfR, rip: r.RIP, rport: r.RPort, proto: r.Proto}, r)
	}
	if len(hi) == 0 {
		return nil
	}
	st, err := tx.Prepare(`INSERT INTO rollup_1h_if(ts,dev,ifc,ifr,mac,b_up,b_down,b_int,flows,o_up,o_down,o_int) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)` +
		upsertTail + `, o_up=o_up+excluded.o_up, o_down=o_down+excluded.o_down, o_int=o_int+excluded.o_int`)
	if err != nil {
		return err
	}
	defer st.Close()
	for k, v := range hi {
		if _, err := st.Exec(k.ts, k.dev, k.ifc, k.ifr, k.mac, v.up, v.down, v.internal, v.flows, v.ownUp, v.ownDown, v.ownInt); err != nil {
			return err
		}
	}
	for _, t := range []struct {
		table string
		m     map[ifKey]*aggVal
	}{{"rollup_1h_ifd", hd}, {"rollup_1d_ifd", dd}} {
		sd, err := tx.Prepare(`INSERT INTO ` + t.table + `(ts,dev,ifc,ifr,rip,rport,proto,svc,host,cc,asn,asorg,b_up,b_down,b_int,flows)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)` + upsertTail + `, host=CASE WHEN excluded.host<>'' THEN excluded.host ELSE host END`)
		if err != nil {
			return err
		}
		for k, v := range t.m {
			if _, err := sd.Exec(k.ts, k.dev, k.ifc, k.ifr, k.rip, k.rport, k.proto, v.svc, v.host, v.cc, v.asn, v.org, v.up, v.down, v.internal, v.flows); err != nil {
				sd.Close()
				return err
			}
		}
		sd.Close()
	}
	return nil
}

// ifaceCond matches the traffic of one interface: clients on it (ifc) or, for a WAN, traffic that left through it
// (ifr, only the internet directions so a routed LAN destination does not count as WAN).
const ifaceCond = `r.dev=? AND (r.ifc=? OR (r.ifr=? AND r.b_up+r.b_down>0))`

// IfaceRow is the traffic of one interface of one device.
type IfaceRow struct {
	Dev      string `json:"dev"`
	Idx      uint32 `json:"idx"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	Internal int64  `json:"internal"`
	Flows    int64  `json:"flows"`
	Clients  int64  `json:"clients"`
}

// IfaceMatrixRow is internal (routed) traffic from one interface to another.
type IfaceMatrixRow struct {
	Dev   string `json:"dev"`
	From  uint32 `json:"from"`
	To    uint32 `json:"to"`
	Bytes int64  `json:"bytes"`
}

// IfaceUsage returns the traffic per client-side interface (lan) and per WAN-side interface (wan), and the first
// hour for which interface data exists (0 = none yet): older traffic has no interface attached.
func (s *Store) IfaceUsage(since int64, own bool) (lan, wan []IfaceRow, first int64, err error) {
	lan, wan = []IfaceRow{}, []IfaceRow{}
	up, down, in := `b_up`, `b_down`, `b_int`
	if !own {
		up, down, in = `(b_up-o_up)`, `(b_down-o_down)`, `(b_int-o_int)`
	}
	from := since / 3600 * 3600
	scan := func(q string, dst *[]IfaceRow) error {
		rows, err := s.DB.Query(q, from)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r IfaceRow
			if err := rows.Scan(&r.Dev, &r.Idx, &r.Up, &r.Down, &r.Internal, &r.Flows, &r.Clients); err != nil {
				return err
			}
			if r.Up+r.Down+r.Internal > 0 {
				*dst = append(*dst, r)
			}
		}
		return rows.Err()
	}
	if err = scan(`SELECT dev, ifc, sum(`+up+`), sum(`+down+`), sum(`+in+`), sum(flows), count(distinct mac)
		FROM rollup_1h_if INDEXED BY sqlite_autoindex_rollup_1h_if_1 WHERE ts>=? GROUP BY dev, ifc`, &lan); err != nil {
		return
	}
	if err = scan(`SELECT dev, ifr, sum(`+up+`), sum(`+down+`), 0, sum(flows), count(distinct mac)
		FROM rollup_1h_if INDEXED BY sqlite_autoindex_rollup_1h_if_1 WHERE ts>=? AND ifr<>0 AND b_up+b_down>0 GROUP BY dev, ifr`, &wan); err != nil {
		return
	}
	sortIf := func(l []IfaceRow) {
		sort.Slice(l, func(i, j int) bool { return l[i].Up+l[i].Down+l[i].Internal > l[j].Up+l[j].Down+l[j].Internal })
	}
	sortIf(lan)
	sortIf(wan)
	err = s.DB.QueryRow(`SELECT coalesce(min(ts),0) FROM rollup_1h_if`).Scan(&first)
	return
}

// IfaceMatrix returns routed traffic between interfaces (flows of direction i). The router itself (index 0) and
// traffic that stays inside one interface are left out: only traffic that crosses between two interfaces is a
// routing decision worth showing.
func (s *Store) IfaceMatrix(since int64, own bool) ([]IfaceMatrixRow, error) {
	in := `b_int`
	if !own {
		in = `(b_int-o_int)`
	}
	rows, err := s.DB.Query(`SELECT dev, ifc, ifr, sum(`+in+`) AS n FROM rollup_1h_if INDEXED BY sqlite_autoindex_rollup_1h_if_1
		WHERE ts>=? AND b_int>0 AND ifc<>0 AND ifr<>0 AND ifc<>ifr GROUP BY dev, ifc, ifr HAVING n>0 ORDER BY n DESC LIMIT 200`, since/3600*3600)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IfaceMatrixRow{}
	for rows.Next() {
		var r IfaceMatrixRow
		if err := rows.Scan(&r.Dev, &r.From, &r.To, &r.Bytes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TopByIface is TopByOwn restricted to the traffic of one interface (see ifaceCond). Granularity is one hour.
// "clients" and "internal" come from the per-client table, everything else from the per-destination tables.
func (s *Store) TopByIface(what string, since int64, limit int, own bool, sel IfaceSel) ([]TopRow, error) {
	key := fmt.Sprintf("topif|%s|%s|%d|%d|%t", what, sel, limit, since/3600, own)
	if olderThan(since, hybridMinAge) {
		return memoize(&s.memo, key, 2*time.Minute, sliceCopy[TopRow], func() ([]TopRow, error) { return s.topByIface(what, since, limit, own, sel) })
	}
	return s.topByIface(what, since, limit, own, sel)
}

func (s *Store) topByIface(what string, since int64, limit int, own bool, sel IfaceSel) ([]TopRow, error) {
	var q string
	args := []any{since / 3600 * 3600, sel.Dev, sel.Idx, sel.Idx}
	switch what {
	case "clients", "internal":
		up, down, in := `r.b_up`, `r.b_down`, `r.b_int`
		if !own {
			up, down, in = `(r.b_up-r.o_up)`, `(r.b_down-r.o_down)`, `(r.b_int-r.o_int)`
		}
		extra := ""
		if what == "internal" {
			extra = ` AND r.b_int>0`
		}
		q = `SELECT r.mac, coalesce(nullif(c.label,''),nullif(c.hostname,''),r.mac), coalesce(c.ip, r.mac),
			sum(` + up + `), sum(` + down + `), sum(` + in + `), sum(r.flows)
			FROM rollup_1h_if r INDEXED BY sqlite_autoindex_rollup_1h_if_1 LEFT JOIN clients c ON c.mac=r.mac
			WHERE r.ts>=? AND ` + ifaceCond + extra + ` GROUP BY r.mac HAVING sum(` + up + `)+sum(` + down + `)+sum(` + in + `)>0
			ORDER BY sum(` + up + `)+sum(` + down + `)+sum(` + in + `) DESC LIMIT ?`
	default:
		sl, grp, where, err := topSpec(what)
		if err != nil {
			return nil, err
		}
		tbl, al := "rollup_1h_ifd", int64(3600)
		if olderThan(since, 8*24*time.Hour) {
			tbl, al = "rollup_1d_ifd", 86400
		}
		args[0] = since / al * al
		if !own {
			where += ` AND coalesce(r.svc,'') NOT IN ` + ownSvcs
		}
		q = `SELECT ` + sl + `, sum(r.b_up), sum(r.b_down), sum(r.b_int), sum(r.flows)
			FROM ` + tbl + ` r INDEXED BY sqlite_autoindex_` + tbl + `_1 WHERE r.ts>=? AND ` + ifaceCond + where + ` GROUP BY ` + grp +
			` ORDER BY sum(r.b_up)+sum(r.b_down)+sum(r.b_int) DESC LIMIT ?`
	}
	args = append(args, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TopRow{}
	for rows.Next() {
		var t TopRow
		var k, l, sub *string
		if err := rows.Scan(&k, &l, &sub, &t.Up, &t.Down, &t.Internal, &t.Flows); err != nil {
			return nil, err
		}
		t.Key, t.Label, t.Sub = deref(k), deref(l), deref(sub)
		out = append(out, t)
	}
	return out, rows.Err()
}
