package store

// DNSUseRow is what one client sent to one destination on a DNS-related port, with the first and last hour it was seen.
type DNSUseRow struct {
	MAC, RIP, Host string
	RPort, Proto   int
	Flows          int64
	First, Last    int64
}

// DNSUse returns per client/destination sums for plain DNS (53), DNS-over-TLS/QUIC (853) and, for hosts whose resolved
// name equals or ends with one of dohSuffixes, DNS-over-HTTPS (443). Hourly rollup, range scan on the primary key.
func (s *Store) DNSUse(since int64, dohSuffixes []string) ([]DNSUseRow, error) {
	from := since / 3600 * 3600
	out := []DNSUseRow{}
	scan := func(q string, args ...any) error {
		rows, err := s.DB.Query(q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r DNSUseRow
			if err := rows.Scan(&r.MAC, &r.RIP, &r.RPort, &r.Proto, &r.Host, &r.Flows, &r.First, &r.Last); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	}
	if err := scan(`SELECT mac, rip, rport, proto, coalesce(max(host),''), sum(flows), min(ts), max(ts)
		FROM rollup_1h INDEXED BY sqlite_autoindex_rollup_1h_1 WHERE ts>=? AND rport IN (53,853) AND mac<>''
		GROUP BY mac, rip, rport, proto LIMIT 5000`, from); err != nil {
		return nil, err
	}
	if len(dohSuffixes) > 0 {
		cond, args := "", []any{from}
		for _, sfx := range dohSuffixes {
			cond += " OR host=? OR host LIKE ?"
			args = append(args, sfx, "%."+sfx)
		}
		if err := scan(`SELECT mac, rip, rport, proto, coalesce(max(host),''), sum(flows), min(ts), max(ts)
			FROM rollup_1h INDEXED BY sqlite_autoindex_rollup_1h_1 WHERE ts>=? AND rport=443 AND mac<>'' AND host<>'' AND (0`+cond+`)
			GROUP BY mac, rip, rport, proto LIMIT 5000`, args...); err != nil {
			return nil, err
		}
	}
	return out, nil
}
