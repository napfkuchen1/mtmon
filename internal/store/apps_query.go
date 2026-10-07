package store

import "time"

// DestAgg is traffic towards one remote endpoint (address + port + protocol), summed over a window.
type DestAgg struct {
	RIP   string `json:"rip"`
	Host  string `json:"host"`
	ASOrg string `json:"asorg"`
	Port  int    `json:"port"`
	Proto int    `json:"proto"`
	Up    int64  `json:"up"`
	Down  int64  `json:"down"`
	Flows int64  `json:"flows"`
	Last  int64  `json:"last"` // newest rollup bucket with traffic
}

// AppDestLimit bounds how many destination rows feed the app classifier per request. Rows are taken
// by descending traffic, so only the long tail of tiny flows can fall out.
const AppDestLimit = 3000

// ClientDests lists the busiest remote endpoints of one client. It answers from rollup_1m for short
// windows and from rollup_1h otherwise (per-client data only exists in those two), always through the
// (mac, ts) index, so the cost is bounded by the client's own rows in the window.
func (s *Store) ClientDests(mac string, since int64, limit int) ([]DestAgg, error) {
	if limit <= 0 || limit > AppDestLimit {
		limit = AppDestLimit
	}
	tbl, al := tableFor("hosts", since, NormMAC(mac))
	return s.destAgg(`SELECT rip, coalesce(max(host),''), coalesce(max(asorg),''), rport, proto, sum(b_up), sum(b_down), sum(flows), max(ts)
		FROM `+tbl+` WHERE mac=? AND ts>=? AND b_up+b_down>0 GROUP BY rip, rport, proto
		ORDER BY sum(b_up)+sum(b_down) DESC LIMIT ?`, NormMAC(mac), since/al*al, limit)
}

// ClientActiveDests lists endpoints a client exchanged data with in the last `within` seconds
// (1-minute rollups, so the resolution is a minute).
func (s *Store) ClientActiveDests(mac string, within int64) ([]DestAgg, error) {
	from := (time.Now().Unix() - within) / 60 * 60
	return s.destAgg(`SELECT rip, coalesce(max(host),''), coalesce(max(asorg),''), rport, proto, sum(b_up), sum(b_down), sum(flows), max(ts)
		FROM rollup_1m WHERE mac=? AND ts>=? AND b_up+b_down>0 GROUP BY rip, rport, proto LIMIT ?`, NormMAC(mac), from, AppDestLimit)
}

// TopDests lists the busiest remote endpoints across all clients (for the Services page "Apps" view).
// Tiers: 1-minute rollups up to ~2 h, hourly up to 36 h, per-destination daily rollups beyond (so windows
// longer than 36 h are rounded to whole days). The daily tier has no client dimension at all.
func (s *Store) TopDests(since int64, limit int) ([]DestAgg, error) {
	if limit <= 0 || limit > AppDestLimit {
		limit = AppDestLimit
	}
	tbl, al := tableFor("hosts", since, "")
	if tbl == "rollup_1h" && olderThan(since, 36*time.Hour) {
		tbl, al = "rollup_1d", 86400 // 7 d of hourly rows is the slowest scan (~0.4 s at 100 clients); daily rows are ~100x fewer
	}
	return s.destAgg(`SELECT rip, coalesce(max(host),''), coalesce(max(asorg),''), rport, proto, sum(b_up), sum(b_down), sum(flows), max(ts)
		FROM `+tbl+` INDEXED BY sqlite_autoindex_`+tbl+`_1 WHERE ts>=? AND b_up+b_down>0 GROUP BY rip, rport, proto
		ORDER BY sum(b_up)+sum(b_down) DESC LIMIT ?`, since/al*al, limit)
}

func (s *Store) destAgg(q string, args ...any) ([]DestAgg, error) {
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DestAgg{}
	for rows.Next() {
		var d DestAgg
		if err := rows.Scan(&d.RIP, &d.Host, &d.ASOrg, &d.Port, &d.Proto, &d.Up, &d.Down, &d.Flows, &d.Last); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
