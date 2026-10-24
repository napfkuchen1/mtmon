package store

import "database/sql"

// Hourly per-source and per-destination counts of the firewall log. The "top blocked sources / destinations" lists
// of the firewall summary read these instead of grouping millions of raw events (seconds on a busy log); they are
// kept as long as the raw events.
const fwTopSchema = `
CREATE TABLE IF NOT EXISTS fw_src_hourly(
  ts INTEGER, device TEXT, prefix TEXT, src TEXT, n INTEGER, PRIMARY KEY(ts, device, prefix, src)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS fw_dst_hourly(
  ts INTEGER, device TEXT, prefix TEXT, dst TEXT, dport INTEGER, n INTEGER, PRIMARY KEY(ts, device, prefix, dst, dport)) WITHOUT ROWID;`

// migrateFwTop creates the tables and, when they are new, fills them from the events already stored.
func migrateFwTop(db *sql.DB) error {
	var have int
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='fw_src_hourly'`).Scan(&have)
	if _, err := db.Exec(fwTopSchema); err != nil {
		return err
	}
	if have > 0 {
		return nil
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO fw_src_hourly SELECT ts/3600*3600, device, prefix, src, count(*) FROM fw_events GROUP BY 1,2,3,4`); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO fw_dst_hourly SELECT ts/3600*3600, device, prefix, dst, dport, count(*) FROM fw_events GROUP BY 1,2,3,4,5`)
	return err
}
