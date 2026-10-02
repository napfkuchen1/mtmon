package store

import (
	"database/sql"
	"strings"
	"time"
)

// This file holds everything the Suggestions feature (internal/advice) needs from the database: its own
// schema (additive, idempotent) and a handful of bounded, read-only aggregate queries.

func migrateAdvice(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS advice_dismissed(
  id TEXT PRIMARY KEY, until INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS roam_ts ON roam(ts);`)
	return err
}

// ---- dismissals ----

// DismissSuggestion hides a suggestion; days<=0 hides it for good.
func (s *Store) DismissSuggestion(id string, days int) error {
	now := time.Now().Unix()
	until := int64(0)
	if days > 0 {
		until = now + int64(days)*86400
	}
	_, err := s.DB.Exec(`INSERT INTO advice_dismissed(id,until,created) VALUES(?,?,?)
		ON CONFLICT(id) DO UPDATE SET until=excluded.until, created=excluded.created`, id, until, now)
	return err
}

func (s *Store) RestoreSuggestion(id string) error {
	_, err := s.DB.Exec(`DELETE FROM advice_dismissed WHERE id=?`, id)
	return err
}

// DismissedSuggestions returns id → until (0 = forever); expired snoozes are removed.
func (s *Store) DismissedSuggestions() (map[string]int64, error) {
	now := time.Now().Unix()
	s.DB.Exec(`DELETE FROM advice_dismissed WHERE until>0 AND until<?`, now)
	rows, err := s.DB.Query(`SELECT id,until FROM advice_dismissed`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var u int64
		rows.Scan(&id, &u)
		out[id] = u
	}
	return out, rows.Err()
}

// ---- aggregate queries ----

type MetricStat struct {
	N                int
	AvgCPU, MaxCPU   float64
	AvgMem, MaxMem   float64
	HighCPU, HighMem int // samples above the thresholds
}

// DevMetricStats summarises dev_metrics per device since `since` (uses the (device, ts) index: one row per
// minute and device, so ~360 rows per device for 6 h).
func (s *Store) DevMetricStats(since int64, cpuThr, memThr float64) (map[string]MetricStat, error) {
	rows, err := s.DB.Query(`SELECT device, count(*), avg(cpu), max(cpu),
		avg(100.0*mem_used/mem_total), max(100.0*mem_used/mem_total),
		sum(cpu>?), sum(100.0*mem_used/mem_total>?)
		FROM dev_metrics WHERE ts>=? AND up=1 AND mem_total>0 GROUP BY device`, cpuThr, memThr, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]MetricStat{}
	for rows.Next() {
		var d string
		var m MetricStat
		if err := rows.Scan(&d, &m.N, &m.AvgCPU, &m.MaxCPU, &m.AvgMem, &m.MaxMem, &m.HighCPU, &m.HighMem); err != nil {
			return nil, err
		}
		out[d] = m
	}
	return out, rows.Err()
}

type RoamStat struct {
	MAC  string
	N    int
	APs  int
	Last int64
}

// RoamStats lists clients with at least `min` roaming events since `since`.
func (s *Store) RoamStats(since int64, min int) ([]RoamStat, error) {
	rows, err := s.DB.Query(`SELECT mac, count(*), count(DISTINCT to_dev), max(ts) FROM roam WHERE ts>=?
		GROUP BY mac HAVING count(*)>=? ORDER BY 2 DESC LIMIT 50`, since, min)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RoamStat{}
	for rows.Next() {
		var r RoamStat
		rows.Scan(&r.MAC, &r.N, &r.APs, &r.Last)
		out = append(out, r)
	}
	return out, rows.Err()
}

// IPChurn: number of distinct IPs per MAC since `since` (only MACs with at least `min`).
func (s *Store) IPChurn(since int64, min int) (map[string]int, error) {
	rows, err := s.DB.Query(`SELECT mac, count(*) FROM ip_history WHERE last>=? GROUP BY mac HAVING count(*)>=?`, since, min)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var m string
		var n int
		rows.Scan(&m, &n)
		out[m] = n
	}
	return out, rows.Err()
}

type ClientTotal struct {
	MAC               string
	Up, Down, Int, Fl int64
}

// ClientTotals: per-client sums from the tiny hourly client rollup.
func (s *Store) ClientTotals(since int64) ([]ClientTotal, error) {
	rows, err := s.DB.Query(`SELECT mac, sum(b_up), sum(b_down), sum(b_int), sum(flows) FROM rollup_1h_client
		WHERE ts>=? GROUP BY mac`, since/3600*3600)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientTotal{}
	for rows.Next() {
		var c ClientTotal
		rows.Scan(&c.MAC, &c.Up, &c.Down, &c.Int, &c.Fl)
		out = append(out, c)
	}
	return out, rows.Err()
}

type PortRow struct {
	MAC, RIP, Host string
	RPort, Proto   int
	Bytes, Flows   int64
}

// PortUse returns per client/destination sums for a set of remote ports (hourly rollup, range scan on
// the primary key; the port list keeps the result small).
func (s *Store) PortUse(since int64, ports []int) ([]PortRow, error) {
	if len(ports) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ports)), ",")
	args := []any{since / 3600 * 3600}
	for _, p := range ports {
		args = append(args, p)
	}
	rows, err := s.DB.Query(`SELECT mac, rip, rport, proto, coalesce(max(host),''), sum(b_up+b_down), sum(flows)
		FROM rollup_1h INDEXED BY sqlite_autoindex_rollup_1h_1 WHERE ts>=? AND rport IN (`+ph+`) AND mac<>''
		GROUP BY mac, rip, rport, proto LIMIT 5000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PortRow{}
	for rows.Next() {
		var r PortRow
		rows.Scan(&r.MAC, &r.RIP, &r.RPort, &r.Proto, &r.Host, &r.Bytes, &r.Flows)
		out = append(out, r)
	}
	return out, rows.Err()
}

// HostUse: traffic of every client to destinations whose resolved name ends with one of the suffixes.
func (s *Store) HostUse(since int64, port int, suffixes []string) ([]PortRow, error) {
	if len(suffixes) == 0 {
		return nil, nil
	}
	cond := ""
	args := []any{since / 3600 * 3600, port}
	for _, sfx := range suffixes {
		cond += " OR host=? OR host LIKE ?"
		args = append(args, sfx, "%."+sfx)
	}
	rows, err := s.DB.Query(`SELECT mac, rip, rport, proto, coalesce(max(host),''), sum(b_up+b_down), sum(flows)
		FROM rollup_1h INDEXED BY sqlite_autoindex_rollup_1h_1 WHERE ts>=? AND rport=? AND mac<>'' AND host<>'' AND (0`+cond+`)
		GROUP BY mac, rip, rport, proto LIMIT 5000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PortRow{}
	for rows.Next() {
		var r PortRow
		rows.Scan(&r.MAC, &r.RIP, &r.RPort, &r.Proto, &r.Host, &r.Bytes, &r.Flows)
		out = append(out, r)
	}
	return out, rows.Err()
}

type InboundRow struct {
	MAC, CIP     string
	CPort, Proto int
	Flows        int64
	Sources      int
}

// InboundUse finds download-direction raw flows whose *client-side* port is one of `ports`, i.e. remote
// hosts opening connections to a service on a LAN client. Raw table, therefore a short window.
func (s *Store) InboundUse(since int64, ports []int) ([]InboundRow, error) {
	if len(ports) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ports)), ",")
	args := []any{since}
	for _, p := range ports {
		args = append(args, p)
	}
	rows, err := s.DB.Query(`SELECT mac, cip, cport, proto, count(*), count(DISTINCT rip) FROM flows
		WHERE ts>=? AND dir='d' AND cport IN (`+ph+`) GROUP BY mac, cip, cport, proto LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InboundRow{}
	for rows.Next() {
		var r InboundRow
		rows.Scan(&r.MAC, &r.CIP, &r.CPort, &r.Proto, &r.Flows, &r.Sources)
		out = append(out, r)
	}
	return out, rows.Err()
}

type BlockedSource struct {
	IP   string
	Hits int64
}

// blockedSample caps how many events BlockedSources looks at.
const blockedSample = 60000

// BlockedSources: top non-LAN sources of events that hit one of our logging drop/reject rules, plus the
// total number of such events considered (range scan on the ts index).
func (s *Store) BlockedSources(since int64, limit int) ([]BlockedSource, int64, error) {
	var ph []string
	args := []any{since}
	for k, r := range s.ruleIndex() {
		if Verdict(r.Action) == "blocked" {
			ph = append(ph, "?")
			args = append(args, k[1])
		}
	}
	if len(ph) == 0 {
		return nil, 0, nil
	}
	// Bounded cost: only the most recent blockedSample matching events are aggregated, however busy the log is.
	args = append(args, blockedSample, limit)
	rows, err := s.DB.Query(`SELECT src, count(*) FROM (SELECT src FROM fw_events WHERE ts>=? AND prefix IN (`+strings.Join(ph, ",")+`)
		AND src NOT GLOB '10.*' AND src NOT GLOB '192.168.*' AND src NOT GLOB '172.[123]*' AND src NOT GLOB '169.254.*'
		ORDER BY ts DESC LIMIT ?) GROUP BY src ORDER BY 2 DESC LIMIT ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []BlockedSource{}
	var total int64
	for rows.Next() {
		var b BlockedSource
		rows.Scan(&b.IP, &b.Hits)
		total += b.Hits
		out = append(out, b)
	}
	return out, total, rows.Err()
}

// FwHits: log hits per (device, prefix) since `since`, and the age of the oldest hourly bucket (how much
// history exists at all).
func (s *Store) FwHits(since int64) (hits map[[2]string]int64, oldest int64, err error) {
	hits = map[[2]string]int64{}
	rows, err := s.DB.Query(`SELECT device,prefix,sum(n) FROM fw_hourly WHERE ts>=? GROUP BY device,prefix`, since/3600*3600)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var d, p string
		var n int64
		rows.Scan(&d, &p, &n)
		hits[[2]string{d, p}] = n
	}
	var o sql.NullInt64
	s.DB.QueryRow(`SELECT min(ts) FROM fw_hourly`).Scan(&o)
	return hits, o.Int64, rows.Err()
}
