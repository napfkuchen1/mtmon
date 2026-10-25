package store

import (
	"strconv"
	"time"
)

// Speedtest is one measured (or failed) speed test. DownBps/UpBps 0 mean "not measured" (Hetzner has no upload);
// Err is set for a failed test, which never counts as 0 Mbit/s.
type Speedtest struct {
	ID       int64   `json:"id"`
	TS       int64   `json:"ts"`
	Provider string  `json:"provider"`
	Server   string  `json:"server"`
	Family   string  `json:"family"` // "4", "6" or "" (system choice)
	DownBps  float64 `json:"down_bps"`
	UpBps    float64 `json:"up_bps"`
	PingMS   float64 `json:"ping_ms"`
	JitterMS float64 `json:"jitter_ms"`
	Bytes    int64   `json:"bytes"`
	DurS     float64 `json:"dur_s"`
	Err      string  `json:"error,omitempty"`
	WAN      string  `json:"wan,omitempty"` // WAN interface(s) that were up, so results after a failover are not mixed
	Src      string  `json:"src,omitempty"` // "" = measured by mtmon, "import" = imported history
}

// SpeedtestRetentionDefault is how long results are kept: one tiny row per test, so much longer than flows.
const SpeedtestRetentionDefault = 365

func (s *Store) pruneSpeedtests(now time.Time) {
	days := SpeedtestRetentionDefault
	if n, err := strconv.Atoi(s.GetMeta("speedtest_retention_days")); err == nil && n >= 7 && n <= 3650 {
		days = n
	}
	s.DB.Exec(`DELETE FROM speedtests WHERE ts < ?`, now.AddDate(0, 0, -days).Unix())
}

// AddSpeedtest stores a result and returns its id.
func (s *Store) AddSpeedtest(t Speedtest) (int64, error) {
	r, err := s.DB.Exec(`INSERT INTO speedtests(ts,provider,server,family,down_bps,up_bps,ping_ms,jitter_ms,bytes,dur_s,err,wan,src)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, t.TS, t.Provider, t.Server, t.Family, t.DownBps, t.UpBps, t.PingMS, t.JitterMS, t.Bytes, t.DurS, t.Err, t.WAN, t.Src)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

const speedtestCols = `id,ts,provider,coalesce(server,''),coalesce(family,''),coalesce(down_bps,0),coalesce(up_bps,0),coalesce(ping_ms,0),coalesce(jitter_ms,0),coalesce(bytes,0),coalesce(dur_s,0),coalesce(err,''),coalesce(wan,''),coalesce(src,'')`

// Speedtests lists results from since on (newest first), at most limit (<=0: all).
func (s *Store) Speedtests(since int64, limit int) ([]Speedtest, error) {
	q := `SELECT ` + speedtestCols + ` FROM speedtests WHERE ts>=? ORDER BY ts DESC, id DESC`
	args := []any{since}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Speedtest{}
	for rows.Next() {
		var t Speedtest
		if err := rows.Scan(&t.ID, &t.TS, &t.Provider, &t.Server, &t.Family, &t.DownBps, &t.UpBps, &t.PingMS, &t.JitterMS, &t.Bytes, &t.DurS, &t.Err, &t.WAN, &t.Src); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SpeedtestRecentOK returns the newest n successful results of one provider and family (newest first).
func (s *Store) SpeedtestRecentOK(provider, family string, n int) ([]Speedtest, error) {
	rows, err := s.DB.Query(`SELECT `+speedtestCols+` FROM speedtests WHERE provider=? AND coalesce(family,'')=? AND coalesce(err,'')='' ORDER BY ts DESC, id DESC LIMIT ?`, provider, family, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Speedtest{}
	for rows.Next() {
		var t Speedtest
		if err := rows.Scan(&t.ID, &t.TS, &t.Provider, &t.Server, &t.Family, &t.DownBps, &t.UpBps, &t.PingMS, &t.JitterMS, &t.Bytes, &t.DurS, &t.Err, &t.WAN, &t.Src); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SpeedtestLastOK is the newest successful result of any provider (zero value: none yet).
func (s *Store) SpeedtestLastOK() Speedtest {
	var t Speedtest
	s.DB.QueryRow(`SELECT `+speedtestCols+` FROM speedtests WHERE coalesce(err,'')='' ORDER BY ts DESC, id DESC LIMIT 1`).
		Scan(&t.ID, &t.TS, &t.Provider, &t.Server, &t.Family, &t.DownBps, &t.UpBps, &t.PingMS, &t.JitterMS, &t.Bytes, &t.DurS, &t.Err, &t.WAN, &t.Src)
	return t
}

// SpeedtestBytesSince is the traffic the tests of mtmon itself used since ts (data budget). Imports do not count.
func (s *Store) SpeedtestBytesSince(ts int64) int64 {
	var n int64
	s.DB.QueryRow(`SELECT coalesce(sum(bytes),0) FROM speedtests WHERE ts>=? AND coalesce(src,'')=''`, ts).Scan(&n)
	return n
}

// SpeedtestLastTS is the time of the newest test mtmon ran itself (0: none), used to schedule the next one.
func (s *Store) SpeedtestLastTS() int64 {
	var n int64
	s.DB.QueryRow(`SELECT coalesce(max(ts),0) FROM speedtests WHERE coalesce(src,'')=''`).Scan(&n)
	return n
}

// SpeedStat is min/avg/max of one metric over the successful results that measured it.
type SpeedStat struct {
	N   int     `json:"n"`
	Min float64 `json:"min"`
	Avg float64 `json:"avg"`
	Max float64 `json:"max"`
}

// SpeedtestSummary aggregates a window. Failed tests are counted separately and never enter the statistics.
type SpeedtestSummary struct {
	Tests  int       `json:"tests"`
	Failed int       `json:"failed"`
	Down   SpeedStat `json:"down_bps"`
	Up     SpeedStat `json:"up_bps"`
	Ping   SpeedStat `json:"ping_ms"`
	Bytes  int64     `json:"bytes"`
}

func (s *Store) SpeedtestSummary(since int64) SpeedtestSummary {
	var out SpeedtestSummary
	s.DB.QueryRow(`SELECT count(*), coalesce(sum(coalesce(err,'')<>''),0), coalesce(sum(coalesce(bytes,0)),0) FROM speedtests WHERE ts>=?`, since).
		Scan(&out.Tests, &out.Failed, &out.Bytes)
	stat := func(col string) SpeedStat {
		var st SpeedStat
		s.DB.QueryRow(`SELECT count(*), coalesce(min(`+col+`),0), coalesce(avg(`+col+`),0), coalesce(max(`+col+`),0) FROM speedtests
			WHERE ts>=? AND coalesce(err,'')='' AND coalesce(`+col+`,0)>0`, since).Scan(&st.N, &st.Min, &st.Avg, &st.Max)
		return st
	}
	out.Down, out.Up, out.Ping = stat("down_bps"), stat("up_bps"), stat("ping_ms")
	return out
}

// ImportSpeedtests adds history from another tool (src "import"); rows that already exist (same time, provider,
// server) are skipped, so importing the same file twice changes nothing. Returns added and skipped.
func (s *Store) ImportSpeedtests(rows []Speedtest) (added, skipped int, err error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	for _, t := range rows {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM speedtests WHERE ts=? AND provider=? AND coalesce(server,'')=? AND coalesce(src,'')='import'`, t.TS, t.Provider, t.Server).Scan(&n); err != nil {
			return 0, 0, err
		}
		if n > 0 {
			skipped++
			continue
		}
		t.Src = "import"
		if _, err := tx.Exec(`INSERT INTO speedtests(ts,provider,server,family,down_bps,up_bps,ping_ms,jitter_ms,bytes,dur_s,err,wan,src)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, t.TS, t.Provider, t.Server, t.Family, t.DownBps, t.UpBps, t.PingMS, t.JitterMS, t.Bytes, t.DurS, t.Err, t.WAN, t.Src); err != nil {
			return 0, 0, err
		}
		added++
	}
	return added, skipped, tx.Commit()
}
