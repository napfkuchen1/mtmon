// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"database/sql"
)

// wifi_disc keeps one row per "client disconnected" line of the router log (#191): the score counts them per hour.
func migrateWifiDisc(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS wifi_disc(ts INTEGER NOT NULL, mac TEXT NOT NULL, device TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS wifi_disc_mac ON wifi_disc(mac, ts);`)
	if err != nil {
		return err
	}
	// older versions stored a bare bit/s rate as if it were Mbit/s: bring those rows to Mbit/s (idempotent)
	_, err = db.Exec(`UPDATE wifi_hist SET tx_sum=tx_sum/1000000.0, rx_sum=rx_sum/1000000.0 WHERE n>0 AND (tx_sum+rx_sum)/2.0/n>=10000`)
	return err
}

// AddWifiDisc records one disconnect of a client (MAC upper case) seen in the log of `device`.
func (s *Store) AddWifiDisc(ts int64, mac, device string) {
	s.DB.Exec(`INSERT INTO wifi_disc(ts,mac,device) VALUES(?,?,?)`, ts, NormMAC(mac), device)
}

// wifiDiscCounts returns the disconnects per client since `since`.
func (s *Store) wifiDiscCounts(since int64) map[string]int {
	out := map[string]int{}
	rows, err := s.DB.Query(`SELECT mac, count(*) FROM wifi_disc WHERE ts>=? GROUP BY mac`, since)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var mac string
		var n int
		rows.Scan(&mac, &n)
		out[mac] = n
	}
	return out
}
