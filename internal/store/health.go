// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"os"
	"path/filepath"
	"time"
)

// db_size_log keeps the size of the database files (hourly samples, 35 days) to forecast when the disk fills up.
const healthSchema = `CREATE TABLE IF NOT EXISTS db_size_log(ts INTEGER PRIMARY KEY, bytes INTEGER);`

// DBBytes is the size of the database including its write-ahead log.
func (s *Store) DBBytes() int64 {
	var n int64
	for _, f := range []string{"mtmon.db", "mtmon.db-wal"} {
		if fi, err := os.Stat(filepath.Join(s.dir, f)); err == nil {
			n += fi.Size()
		}
	}
	return n
}

// LogDBSize records the current size (at most once per 50 minutes) and drops samples older than 35 days.
func (s *Store) LogDBSize() {
	now := time.Now().Unix()
	var last int64
	s.DB.QueryRow(`SELECT coalesce(max(ts),0) FROM db_size_log`).Scan(&last)
	if now-last >= 50*60 {
		s.DB.Exec(`INSERT OR REPLACE INTO db_size_log(ts,bytes) VALUES(?,?)`, now, s.DBBytes())
	}
	s.DB.Exec(`DELETE FROM db_size_log WHERE ts<?`, now-35*86400)
}

// DBGrowthPerDay is the growth of the database per day over the last 7 days of samples (bytes, never negative):
// the slope of a least-squares line, so a checkpoint or vacuum shortly before a restart does not zero it.
// ok is false until at least 2 samples span 6 hours; spanH is how many hours of samples there are.
func (s *Store) DBGrowthPerDay() (perDay int64, ok bool) {
	perDay, ok, _ = s.DBGrowthInfo()
	return perDay, ok
}

// DBGrowthInfo is DBGrowthPerDay plus the span of the samples in hours.
func (s *Store) DBGrowthInfo() (perDay int64, ok bool, spanH int) {
	rows, err := s.DB.Query(`SELECT ts,bytes FROM db_size_log WHERE ts>=? ORDER BY ts`, time.Now().Unix()-7*86400)
	if err != nil {
		return 0, false, 0
	}
	defer rows.Close()
	var ts, bs []float64
	for rows.Next() {
		var t, b int64
		if rows.Scan(&t, &b) == nil {
			ts, bs = append(ts, float64(t)), append(bs, float64(b))
		}
	}
	if len(ts) == 0 {
		return 0, false, 0
	}
	span := ts[len(ts)-1] - ts[0]
	spanH = int(span / 3600)
	if len(ts) < 2 || span < 6*3600 {
		return 0, false, spanH
	}
	var mt, mb float64
	for i := range ts {
		mt += ts[i]
		mb += bs[i]
	}
	n := float64(len(ts))
	mt, mb = mt/n, mb/n
	var num, den float64
	for i := range ts {
		num += (ts[i] - mt) * (bs[i] - mb)
		den += (ts[i] - mt) * (ts[i] - mt)
	}
	if den == 0 || num <= 0 {
		return 0, true, spanH
	}
	return int64(num / den * 86400), true, spanH
}
