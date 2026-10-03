package store

import (
	"fmt"
	"strings"
	"time"
)

// Table tiers (see docs/architecture.md): 1-minute rollups answer short windows precisely, hourly
// rollups answer up to ~8 days, per-destination daily rollups answer the rest. Each step keeps the
// scanned row count (and so query latency) bounded regardless of the window.
func olderThan(since int64, d time.Duration) bool { return since < time.Now().Add(-d).Unix() }

// tableFor picks the smallest rollup table that can answer a top-list query.
func tableFor(what string, since int64, mac string) (table string, align int64) {
	if !olderThan(since, 2*time.Hour+5*time.Minute) {
		return "rollup_1m", 60
	}
	if mac == "" && what == "clients" {
		return "rollup_1h_client", 3600
	}
	if mac != "" || what == "internal" || !olderThan(since, 8*24*time.Hour) {
		return "rollup_1h", 3600
	}
	return "rollup_1d", 86400
}

// seriesTable is used for charts and totals (needs finer buckets than top lists).
func seriesTable(since int64, mac string) (table string, align int64) {
	if !olderThan(since, 36*time.Hour) {
		return "rollup_1m", 60
	}
	if mac != "" {
		return "rollup_1h", 3600
	}
	return "rollup_1h_client", 3600
}

type TopRow struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Sub      string `json:"sub"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	Internal int64  `json:"internal"`
	Flows    int64  `json:"flows"`
}

// TopBy supports: clients, hosts, asn, country, ports, services, protocols, internal.
func (s *Store) TopBy(what string, since int64, limit int, mac string) ([]TopRow, error) {
	var sel, grp, where string
	switch what {
	case "clients":
		sel = `r.mac, coalesce(nullif(c.label,''),nullif(c.hostname,''),r.mac), coalesce(c.ip, r.mac)`
		grp = `r.mac`
	case "hosts":
		sel = `r.rip, coalesce(nullif(max(r.host),''),r.rip), trim(coalesce(max(r.cc),'')||' '||coalesce(max(r.asorg),''))`
		grp = `r.rip`
	case "asn":
		sel = `cast(r.asn as text), coalesce(max(r.asorg),'AS'||r.asn), ''`
		grp = `r.asn`
		where = ` AND r.asn>0`
	case "country":
		sel = `r.cc, r.cc, ''`
		grp = `r.cc`
		where = ` AND r.cc<>''`
	case "ports":
		sel = `r.proto||'/'||r.rport, r.proto||'/'||r.rport, coalesce(max(r.svc),'')`
		grp = `r.proto, r.rport`
	case "services":
		sel = `r.svc, r.svc, ''`
		grp = `r.svc`
		where = ` AND r.svc<>''`
	case "protocols":
		sel = `cast(r.proto as text), cast(r.proto as text), ''`
		grp = `r.proto`
	case "internal":
		sel = `r.mac, coalesce(nullif(c.label,''),nullif(c.hostname,''),r.mac), r.rip`
		grp = `r.mac, r.rip`
		where = ` AND r.b_int>0`
	default:
		return nil, fmt.Errorf("unknown top list %q", what)
	}
	tbl, al := tableFor(what, since, mac)
	idx := ""
	if mac == "" {
		// Force the ts range scan on the primary key. Without this SQLite picks the (mac,ts) index for
		// GROUP BY mac and scans the whole table (measured: 1.7 s → 0.12 s on 2.3 M rows).
		idx = "INDEXED BY sqlite_autoindex_" + tbl + "_1"
	}
	join := ""
	if strings.Contains(sel, "c.") { // only clients/internal need names from the clients table
		join = " LEFT JOIN clients c ON c.mac=r.mac"
	}
	q := `SELECT ` + sel + `, sum(r.b_up), sum(r.b_down), sum(r.b_int), sum(r.flows)
		FROM ` + tbl + ` r ` + idx + join + ` WHERE r.ts>=?` + where
	args := []any{since / al * al}
	if mac != "" {
		q += ` AND r.mac=?`
		args = append(args, NormMAC(mac))
	}
	q += ` GROUP BY ` + grp + ` ORDER BY sum(r.b_up)+sum(r.b_down)+sum(r.b_int) DESC LIMIT ?`
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

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type Point struct {
	TS   int64 `json:"ts"`
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// Series returns bucketed traffic. bucket in seconds (multiple of 60).
func (s *Store) Series(since int64, bucket int64, mac string) ([]Point, error) {
	if bucket < 60 {
		bucket = 60
	}
	tbl, al := seriesTable(since, mac)
	if al == 3600 && bucket < 3600 {
		bucket = 3600
	}
	q := `SELECT (ts/?)*? AS t, sum(b_up), sum(b_down) FROM ` + tbl + ` WHERE ts>=?`
	args := []any{bucket, bucket, since / al * al}
	if mac != "" {
		q += ` AND mac=?`
		args = append(args, NormMAC(mac))
	}
	q += ` GROUP BY t ORDER BY t`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Point{}
	for rows.Next() {
		var p Point
		rows.Scan(&p.TS, &p.Up, &p.Down)
		out = append(out, p)
	}
	return out, rows.Err()
}

type Conn struct {
	TS    int64  `json:"ts"`
	RIP   string `json:"rip"`
	RPort int    `json:"rport"`
	CPort int    `json:"cport"`
	Proto int    `json:"proto"`
	Dir   string `json:"dir"`
	Bytes int64  `json:"bytes"`
	Pkts  int64  `json:"pkts"`
	Host  string `json:"host"`
	CC    string `json:"cc"`
	ASN   int    `json:"asn"`
	ASOrg string `json:"asorg"`
	Svc   string `json:"svc"`
	CIP   string `json:"cip"`
	// Exporter is the router that reported the flow; Verdict/Rule come from firewall logs (set by the API).
	Exporter string `json:"exporter"`
	Device   string `json:"device,omitempty"`
	Verdict  string `json:"verdict,omitempty"`
	Rule     string `json:"rule,omitempty"`
}

// Connections returns raw flows for one client (timeline view).
// ConnFilter narrows a client's connection list (drill-down from the Top lists). Zero value = no filter.
type ConnFilter struct {
	Svc   string // detected service name
	RIP   string // remote address
	Proto int    // with RPort: protocol number (0 = any)
	RPort int    // remote port (0 = any)
	CC    string // remote country code
}

func (s *Store) Connections(mac string, since int64, limit int, afterTS int64) ([]Conn, error) {
	return s.ConnectionsFiltered(mac, since, limit, afterTS, ConnFilter{})
}

func (s *Store) ConnectionsFiltered(mac string, since int64, limit int, afterTS int64, f ConnFilter) ([]Conn, error) {
	key := NormMAC(mac)
	q := `SELECT ts,rip,rport,cport,proto,dir,bytes,pkts,host,cc,asn,asorg,svc,cip,exporter FROM flows
		WHERE (mac=? OR (mac='' AND cip=?)) AND ts>=? AND ts>?`
	args := []any{key, mac, since, afterTS}
	if f.Svc != "" {
		q += ` AND svc=?`
		args = append(args, f.Svc)
	}
	if f.RIP != "" {
		q += ` AND rip=?`
		args = append(args, f.RIP)
	}
	if f.RPort > 0 {
		q += ` AND rport=? AND proto=?`
		args = append(args, f.RPort, f.Proto)
	}
	if f.CC != "" {
		q += ` AND cc=?`
		args = append(args, f.CC)
	}
	rows, err := s.DB.Query(q+` ORDER BY ts DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Conn{}
	for rows.Next() {
		var c Conn
		rows.Scan(&c.TS, &c.RIP, &c.RPort, &c.CPort, &c.Proto, &c.Dir, &c.Bytes, &c.Pkts, &c.Host, &c.CC, &c.ASN, &c.ASOrg, &c.Svc, &c.CIP, &c.Exporter)
		out = append(out, c)
	}
	return out, rows.Err()
}

type Overview struct {
	DevicesUp, DevicesTotal int
	ClientsOnline           int
	ClientsTotal            int
	BytesUp, BytesDown      int64
	Flows                   int64
	AlertsOpen              int
}

func (s *Store) Totals(since int64) (up, down, flows int64) {
	tbl, al := seriesTable(since, "")
	s.DB.QueryRow(`SELECT coalesce(sum(b_up),0),coalesce(sum(b_down),0),coalesce(sum(flows),0) FROM `+tbl+` WHERE ts>=?`, since/al*al).Scan(&up, &down, &flows)
	return
}

func (s *Store) ClientCounts() (online, total int) {
	s.DB.QueryRow(`SELECT coalesce(sum(online),0), count(*) FROM clients`).Scan(&online, &total)
	return
}

// PortScanSuspects: clients that hit many distinct (ip,port) in a short window.
func (s *Store) PortScanSuspects(window int64, minDistinct int) ([]string, error) {
	rows, err := s.DB.Query(`SELECT mac FROM (SELECT mac, count(DISTINCT rip||':'||rport) n FROM flows
		WHERE ts>=? AND dir='u' GROUP BY mac) WHERE n>=? AND mac<>''`, time.Now().Unix()-window, minDistinct)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		rows.Scan(&m)
		out = append(out, m)
	}
	return out, nil
}
