package store

import (
	"fmt"
	"strconv"
	"strings"
)

// ClientsOf lists the clients behind one row of the Top lists (a destination, port, provider, country or protocol),
// busiest first. It reads the per-client rollups (1-minute for short windows, hourly otherwise), so it works for
// every range; the daily table has no client dimension.
func (s *Store) ClientsOf(what, key string, since int64, limit int) ([]SvcClient, error) {
	var cond string
	var args []any
	switch what {
	case "hosts":
		cond, args = "r.rip=?", []any{key}
	case "asn":
		n, err := strconv.Atoi(key)
		if err != nil {
			return nil, fmt.Errorf("bad asn %q", key)
		}
		cond, args = "r.asn=?", []any{n}
	case "country":
		cond, args = "r.cc=?", []any{key}
	case "ports":
		p, port, ok := strings.Cut(key, "/")
		pn, e1 := strconv.Atoi(p)
		qn, e2 := strconv.Atoi(port)
		if !ok || e1 != nil || e2 != nil {
			return nil, fmt.Errorf("bad port %q", key)
		}
		cond, args = "r.proto=? AND r.rport=?", []any{pn, qn}
	case "protocols":
		n, err := strconv.Atoi(key)
		if err != nil {
			return nil, fmt.Errorf("bad protocol %q", key)
		}
		cond, args = "r.proto=?", []any{n}
	case "services":
		cond, args = "r.svc=?", []any{key}
	default:
		return nil, fmt.Errorf("no client list for %q", what)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	tbl, al := tableFor("hosts", since, "per-client")
	rows, err := s.DB.Query(`SELECT r.mac, coalesce(nullif(c.label,''),nullif(c.hostname,''),r.mac), coalesce(c.ip,''), coalesce(c.vendor,''), coalesce(c.device,''),
		sum(r.b_up), sum(r.b_down), sum(r.flows), count(DISTINCT r.rip), coalesce(c.last_seen,0)
		FROM `+tbl+` r LEFT JOIN clients c ON c.mac=r.mac WHERE r.ts>=? AND `+cond+` AND r.b_up+r.b_down>0
		GROUP BY r.mac ORDER BY sum(r.b_up)+sum(r.b_down) DESC LIMIT ?`, append(append([]any{since / al * al}, args...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SvcClient{}
	for rows.Next() {
		var c SvcClient
		if err := rows.Scan(&c.MAC, &c.Label, &c.IP, &c.Vendor, &c.Device, &c.Up, &c.Down, &c.Flows, &c.Dests, &c.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClientDest is DestAgg with the client it belongs to (for classifying traffic per client, e.g. an app's users).
type ClientDest struct {
	MAC string
	DestAgg
}

// AllClientDests lists the busiest (client, endpoint) pairs over all clients, from the per-client rollups.
func (s *Store) AllClientDests(since int64, limit int) ([]ClientDest, error) {
	if limit <= 0 || limit > 20*AppDestLimit {
		limit = 20 * AppDestLimit
	}
	tbl, al := tableFor("hosts", since, "per-client")
	rows, err := s.DB.Query(`SELECT mac, rip, coalesce(max(host),''), coalesce(max(asorg),''), rport, proto, sum(b_up), sum(b_down), sum(flows), max(ts)
		FROM `+tbl+` WHERE ts>=? AND b_up+b_down>0 GROUP BY mac, rip, rport, proto ORDER BY sum(b_up)+sum(b_down) DESC LIMIT ?`, since/al*al, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientDest{}
	for rows.Next() {
		var d ClientDest
		if err := rows.Scan(&d.MAC, &d.RIP, &d.Host, &d.ASOrg, &d.Port, &d.Proto, &d.Up, &d.Down, &d.Flows, &d.Last); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
