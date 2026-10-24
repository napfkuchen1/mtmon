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

// DBGrowthPerDay is the growth of the database per day over the last 7 days of samples (bytes, never negative).
// ok is false until the samples span at least 6 hours.
func (s *Store) DBGrowthPerDay() (perDay int64, ok bool) {
	var t0, b0, t1, b1 int64
	cut := time.Now().Unix() - 7*86400
	if s.DB.QueryRow(`SELECT ts,bytes FROM db_size_log WHERE ts>=? ORDER BY ts LIMIT 1`, cut).Scan(&t0, &b0) != nil ||
		s.DB.QueryRow(`SELECT ts,bytes FROM db_size_log ORDER BY ts DESC LIMIT 1`).Scan(&t1, &b1) != nil || t1-t0 < 6*3600 {
		return 0, false
	}
	if b1 <= b0 {
		return 0, true
	}
	return (b1 - b0) * 86400 / (t1 - t0), true
}
