// SPDX-License-Identifier: AGPL-3.0-only

package store

import "database/sql"

// migrateFwCounter adds the rule-counter bookkeeping (#278): fw_rules.counter marks accept rules whose hits are
// taken from the router's rule counters instead of per-packet log lines; fw_ctr keeps the last counter value seen
// per rule so only the difference is added to fw_hourly.
func migrateFwCounter(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS fw_ctr(
  device TEXT, prefix TEXT, packets INTEGER, ts INTEGER, PRIMARY KEY(device, prefix)) WITHOUT ROWID`); err != nil {
		return err
	}
	rows, err := db.Query(`PRAGMA table_info(fw_rules)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &nn, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "counter" {
			has = true
		}
	}
	rows.Close()
	if !has {
		_, err = db.Exec(`ALTER TABLE fw_rules ADD COLUMN counter INTEGER NOT NULL DEFAULT 0`)
	}
	return err
}

// AddFwCounters books the hits of rules counted by the router (packets = current counter value per log prefix).
// The first value of a rule only sets the baseline; a value below the stored one means the counter was reset
// (rule edited, reboot), so the new value itself is the number of hits since the reset – never a negative delta.
// Returns the number of hits added.
func (s *Store) AddFwCounters(device string, ts int64, packets map[string]int64) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var added int64
	for prefix, cur := range packets {
		var last int64
		err := tx.QueryRow(`SELECT packets FROM fw_ctr WHERE device=? AND prefix=?`, device, prefix).Scan(&last)
		known := err == nil
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
		var d int64
		switch {
		case !known:
			d = 0
		case cur >= last:
			d = cur - last
		default:
			d = cur
		}
		if _, err := tx.Exec(`INSERT INTO fw_ctr(device,prefix,packets,ts) VALUES(?,?,?,?)
			ON CONFLICT(device,prefix) DO UPDATE SET packets=excluded.packets, ts=excluded.ts`, device, prefix, cur, ts); err != nil {
			return 0, err
		}
		if d > 0 {
			if _, err := tx.Exec(`INSERT INTO fw_hourly(ts,device,prefix,n) VALUES(?,?,?,?) ON CONFLICT DO UPDATE SET n=n+excluded.n`,
				ts/3600*3600, device, prefix, d); err != nil {
				return 0, err
			}
			added += d
		}
	}
	return added, tx.Commit()
}

// FwCounterPrefixes returns the log prefixes of the device's rules that are booked from router counters.
func (s *Store) FwCounterPrefixes(device string) map[string]bool {
	out := map[string]bool{}
	rows, err := s.DB.Query(`SELECT prefix FROM fw_rules WHERE device=? AND counter=1`, device)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		rows.Scan(&p)
		out[p] = true
	}
	return out
}
