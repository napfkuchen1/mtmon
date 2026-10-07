package store

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

var svcTables = []string{"flows", "rollup_1m", "rollup_1h", "rollup_1d"}

func tableChunk(tbl string) int64 {
	if tbl == "rollup_1d" {
		return 30 * 86400
	}
	return 86400
}

// ruleCond returns the SQL condition (and args) selecting rows a non-CIDR rule matches.
func ruleCond(r ServiceRule) (string, []any) {
	switch r.Kind {
	case "port":
		n, _ := strconv.Atoi(r.Value)
		if r.Proto != 0 {
			return `rport=? AND proto=?`, []any{n, r.Proto}
		}
		return `rport=? AND proto IN (6,17)`, []any{n}
	case "ip":
		return `rip=?`, []any{r.Value}
	case "asn":
		n, _ := strconv.Atoi(r.Value)
		return `asn=?`, []any{n}
	case "host":
		return `(host=? OR host LIKE ? ESCAPE '\')`, []any{r.Value, "%." + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(r.Value)}
	}
	return "", nil
}

func (s *Store) tableBounds(tbl string) (min, max int64) {
	s.DB.QueryRow(`SELECT coalesce(min(ts),0), coalesce(max(ts),0) FROM `+tbl).Scan(&min, &max)
	return
}

// applyRule relabels matching rows in short time slices so the flow writer is never blocked for long.
// With onlySvc set, only rows that carry that label are touched (used to undo a deleted rule).
func (s *Store) applyRule(r ServiceRule, name string) {
	var pfx netip.Prefix
	if r.Kind == "cidr" {
		pfx, _ = netip.ParsePrefix(r.Value)
	}
	for _, tbl := range svcTables {
		lo, hi := s.tableBounds(tbl)
		if hi == 0 {
			continue
		}
		step := tableChunk(tbl)
		for a := lo / step * step; a <= hi; a += step {
			if r.Kind == "cidr" {
				s.relabelCIDR(tbl, pfx, name, a, a+step)
			} else {
				cond, args := ruleCond(r)
				s.DB.Exec(`UPDATE `+tbl+` SET svc=? WHERE ts>=? AND ts<? AND `+cond+` AND svc IS NOT ?`,
					append([]any{name, a, a + step}, append(args, name)...)...)
			}
			time.Sleep(5 * time.Millisecond) // yield to the flow writer
		}
	}
}

func (s *Store) relabelCIDR(tbl string, pfx netip.Prefix, name string, a, b int64) {
	rows, err := s.DB.Query(`SELECT DISTINCT rip FROM `+tbl+` WHERE ts>=? AND ts<?`, a, b)
	if err != nil {
		return
	}
	var ips []any
	for rows.Next() {
		var ip string
		rows.Scan(&ip)
		if x, err := netip.ParseAddr(ip); err == nil && pfx.Contains(x.Unmap()) {
			ips = append(ips, ip)
		}
	}
	rows.Close()
	for i := 0; i < len(ips); i += 400 {
		j := min(i+400, len(ips))
		ph := strings.TrimSuffix(strings.Repeat("?,", j-i), ",")
		s.DB.Exec(`UPDATE `+tbl+` SET svc=? WHERE ts>=? AND ts<? AND svc IS NOT ? AND rip IN (`+ph+`)`,
			append([]any{name, a, b, name}, ips[i:j]...)...)
	}
}

// resetLabel sends rows that carry `name` back to their built-in label (before remaining rules are re-applied).
func (s *Store) resetLabel(name string, builtin func(proto uint8, rport uint16) string) {
	for _, tbl := range svcTables {
		rows, err := s.DB.Query(`SELECT DISTINCT proto, rport FROM `+tbl+` WHERE svc=?`, name)
		if err != nil {
			continue
		}
		type pp struct{ proto, port int }
		var ps []pp
		for rows.Next() {
			var p pp
			rows.Scan(&p.proto, &p.port)
			ps = append(ps, p)
		}
		rows.Close()
		for _, p := range ps {
			s.DB.Exec(`UPDATE `+tbl+` SET svc=? WHERE svc=? AND proto=? AND rport=?`, builtin(uint8(p.proto), uint16(p.port)), name, p.proto, p.port)
			time.Sleep(2 * time.Millisecond)
		}
	}
}

// RebuildServices re-applies every rule, weakest first, optionally after resetting a deleted rule's label.
// ordered must be sorted by precedence (weakest first), as enrich.Classifier does internally.
func (s *Store) RebuildServices(ordered []ServiceRule, resetName string, builtin func(proto uint8, rport uint16) string) {
	if resetName != "" {
		s.resetLabel(resetName, builtin)
	}
	for _, r := range ordered {
		s.applyRule(r, r.Name)
	}
}

// RulePreview estimates what a rule would relabel within `since` (hourly rollups).
func (s *Store) RulePreview(r ServiceRule, since int64) (clients, dests int, bytes, flows int64) {
	tbl := "rollup_1h"
	if r.Kind == "cidr" {
		pfx, err := netip.ParsePrefix(r.Value)
		if err != nil {
			return
		}
		rows, err := s.DB.Query(`SELECT rip, count(DISTINCT mac), sum(b_up+b_down+b_int), sum(flows) FROM `+tbl+` WHERE ts>=? GROUP BY rip`, since/3600*3600)
		if err != nil {
			return
		}
		defer rows.Close()
		macs := 0
		for rows.Next() {
			var ip string
			var c int
			var b, f int64
			rows.Scan(&ip, &c, &b, &f)
			if x, err := netip.ParseAddr(ip); err == nil && pfx.Contains(x.Unmap()) {
				dests++
				bytes += b
				flows += f
				if c > macs {
					macs = c
				}
			}
		}
		return macs, dests, bytes, flows
	}
	cond, args := ruleCond(r)
	if cond == "" {
		return
	}
	s.DB.QueryRow(`SELECT count(DISTINCT mac), count(DISTINCT rip), coalesce(sum(b_up+b_down+b_int),0), coalesce(sum(flows),0) FROM `+tbl+` WHERE ts>=? AND `+cond,
		append([]any{since / 3600 * 3600}, args...)...).Scan(&clients, &dests, &bytes, &flows)
	return
}

// ---- Services page ----

type ServiceRow struct {
	Name    string `json:"name"`
	Up      int64  `json:"up"`
	Down    int64  `json:"down"`
	Int     int64  `json:"internal"`
	Flows   int64  `json:"flows"`
	Clients int64  `json:"clients"`
	Dests   int64  `json:"dests"`
}

func svcTable(since int64) (string, int64) {
	if !olderThan(since, 2*time.Hour+5*time.Minute) {
		return "rollup_1m", 60
	}
	if !olderThan(since, 8*24*time.Hour) {
		return "rollup_1h", 3600
	}
	return "rollup_1d", 86400 // no client dimension: client data comes from a short recent window
}

// svcClientWindow is how far back client lists look when the main window is answered from rollup_1d.
const svcClientWindow = 48 * 3600

// Services lists all services with traffic in the window.
func (s *Store) Services(since int64) ([]ServiceRow, error) {
	tbl, al := svcTable(since)
	q := `SELECT svc, sum(b_up), sum(b_down), sum(b_int), sum(flows), count(DISTINCT mac), count(DISTINCT rip)
		FROM ` + tbl + ` WHERE ts>=? AND svc<>'' GROUP BY svc ORDER BY sum(b_up)+sum(b_down)+sum(b_int) DESC LIMIT 200`
	if tbl == "rollup_1d" {
		q = `SELECT svc, sum(b_up), sum(b_down), sum(b_int), sum(flows), 0, count(DISTINCT rip)
		FROM rollup_1d WHERE ts>=? AND svc<>'' GROUP BY svc ORDER BY sum(b_up)+sum(b_down)+sum(b_int) DESC LIMIT 200`
	}
	rows, err := s.DB.Query(q, since/al*al)
	if err != nil {
		return nil, err
	}
	out := []ServiceRow{}
	for rows.Next() {
		var r ServiceRow
		rows.Scan(&r.Name, &r.Up, &r.Down, &r.Int, &r.Flows, &r.Clients, &r.Dests)
		out = append(out, r)
	}
	rows.Close()
	if tbl == "rollup_1d" { // client counts from the recent hourly window only
		cnt := map[string]int64{}
		cr, err := s.DB.Query(`SELECT svc, count(DISTINCT mac) FROM rollup_1h WHERE ts>=? AND svc<>'' GROUP BY svc`, (time.Now().Unix()-svcClientWindow)/3600*3600)
		if err == nil {
			for cr.Next() {
				var n string
				var c int64
				cr.Scan(&n, &c)
				cnt[n] = c
			}
			cr.Close()
		}
		for i := range out {
			out[i].Clients = cnt[out[i].Name]
		}
	}
	return out, nil
}

type SvcClient struct {
	MAC      string `json:"mac"`
	Label    string `json:"label"`
	IP       string `json:"ip"`
	Vendor   string `json:"vendor"`
	Device   string `json:"device"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	Flows    int64  `json:"flows"`
	Dests    int64  `json:"dests"`
	LastSeen int64  `json:"last_seen"`
}

type SvcDest struct {
	RIP   string `json:"rip"`
	Host  string `json:"host"`
	CC    string `json:"cc"`
	ASOrg string `json:"asorg"`
	Port  int    `json:"port"`
	Proto int    `json:"proto"`
	Up    int64  `json:"up"`
	Down  int64  `json:"down"`
	Flows int64  `json:"flows"`
	Cl    int64  `json:"clients"`
}

type ServiceDetail struct {
	Name    string      `json:"name"`
	Up      int64       `json:"up"`
	Down    int64       `json:"down"`
	Flows   int64       `json:"flows"`
	Clients []SvcClient `json:"clients"`
	Dests   []SvcDest   `json:"dests"`
	Series  []Point     `json:"series"`
	// ClientsSince > 0: the client list only covers [ClientsSince, now] (long ranges have no per-client rollup).
	ClientsSince int64 `json:"clients_since"`
}

func (s *Store) ServiceDetail(name string, since, bucket int64) (*ServiceDetail, error) {
	tbl, al := svcTable(since)
	if al >= 3600 && bucket < al {
		bucket = al
	}
	d := &ServiceDetail{Name: name, Clients: []SvcClient{}, Dests: []SvcDest{}, Series: []Point{}}
	from := since / al * al
	s.DB.QueryRow(`SELECT coalesce(sum(b_up),0),coalesce(sum(b_down),0),coalesce(sum(flows),0) FROM `+tbl+` WHERE svc=? AND ts>=?`, name, from).Scan(&d.Up, &d.Down, &d.Flows)

	ctbl, cfrom := tbl, from
	if tbl == "rollup_1d" {
		ctbl, cfrom = "rollup_1h", (time.Now().Unix()-svcClientWindow)/3600*3600
		d.ClientsSince = cfrom
	}
	rows, err := s.DB.Query(`SELECT r.mac, coalesce(nullif(c.label,''),nullif(c.hostname,''),r.mac), coalesce(c.ip,''), coalesce(c.vendor,''), coalesce(c.device,''),
		sum(r.b_up), sum(r.b_down), sum(r.flows), count(DISTINCT r.rip), coalesce(c.last_seen,0)
		FROM `+ctbl+` r LEFT JOIN clients c ON c.mac=r.mac WHERE r.svc=? AND r.ts>=? GROUP BY r.mac ORDER BY sum(r.b_up)+sum(r.b_down) DESC LIMIT 100`, name, cfrom)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c SvcClient
		rows.Scan(&c.MAC, &c.Label, &c.IP, &c.Vendor, &c.Device, &c.Up, &c.Down, &c.Flows, &c.Dests, &c.LastSeen)
		d.Clients = append(d.Clients, c)
	}
	rows.Close()
	cl := "count(DISTINCT mac)"
	if tbl == "rollup_1d" {
		cl = "-1" // unknown: no client dimension in the daily table
	}
	rows, err = s.DB.Query(`SELECT rip, coalesce(max(host),''), coalesce(max(cc),''), coalesce(max(asorg),''), rport, proto,
		sum(b_up), sum(b_down), sum(flows), `+cl+` FROM `+tbl+` WHERE svc=? AND ts>=? GROUP BY rip, rport, proto
		ORDER BY sum(b_up)+sum(b_down) DESC LIMIT 50`, name, from)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x SvcDest
		rows.Scan(&x.RIP, &x.Host, &x.CC, &x.ASOrg, &x.Port, &x.Proto, &x.Up, &x.Down, &x.Flows, &x.Cl)
		d.Dests = append(d.Dests, x)
	}
	rows.Close()
	rows, err = s.DB.Query(`SELECT (ts/?)*? t, sum(b_up), sum(b_down) FROM `+tbl+` WHERE svc=? AND ts>=? GROUP BY t ORDER BY t`, bucket, bucket, name, from)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p Point
		rows.Scan(&p.TS, &p.Up, &p.Down)
		d.Series = append(d.Series, p)
	}
	rows.Close()
	return d, nil
}

// ServicesOfClient lists services used by one client.
func (s *Store) ServicesOfClient(mac string, since int64) ([]ServiceRow, error) {
	tbl, al := svcTable(since)
	if tbl == "rollup_1d" { // per-client data only exists hourly
		tbl, al = "rollup_1h", 3600
	}
	rows, err := s.DB.Query(`SELECT svc, sum(b_up), sum(b_down), sum(b_int), sum(flows), 1, count(DISTINCT rip) FROM `+tbl+`
		WHERE mac=? AND ts>=? AND svc<>'' GROUP BY svc ORDER BY sum(b_up)+sum(b_down)+sum(b_int) DESC LIMIT 100`, NormMAC(mac), since/al*al)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServiceRow{}
	for rows.Next() {
		var r ServiceRow
		rows.Scan(&r.Name, &r.Up, &r.Down, &r.Int, &r.Flows, &r.Clients, &r.Dests)
		out = append(out, r)
	}
	return out, rows.Err()
}

var _ = fmt.Sprint
var _ = sort.Ints
