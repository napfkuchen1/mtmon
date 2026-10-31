// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"database/sql"
	"regexp"
	"strings"
)

var reDiscMAC = regexp.MustCompile(`(?i)([0-9a-f]{2}(?::[0-9a-f]{2}){5})\S*\s+.*?disconnected`)

// wifi_disc keeps one row per "client disconnected" line of the router log (#191): the score counts them per hour.
func migrateWifiDisc(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS wifi_disc(ts INTEGER NOT NULL, mac TEXT NOT NULL, device TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS wifi_disc_mac ON wifi_disc(mac, ts);`)
	if err != nil {
		return err
	}
	if err := backfillWifiDisc(db); err != nil {
		return err
	}
	// older versions stored a bare bit/s rate as if it were Mbit/s: bring those rows to Mbit/s (idempotent)
	_, err = db.Exec(`UPDATE wifi_hist SET tx_sum=tx_sum/1000000.0, rx_sum=rx_sum/1000000.0 WHERE n>0 AND (tx_sum+rx_sum)/2.0/n>=10000`)
	return err
}

// backfillWifiDisc fills an empty wifi_disc from the stored device log (log_events): disconnects that were logged before
// the table existed still count in the score, and the input does not depend on anything held in memory (#191).
func backfillWifiDisc(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM wifi_disc`).Scan(&n); err != nil || n > 0 {
		return err
	}
	rows, err := db.Query(`SELECT ts, device, msg FROM log_events WHERE (topics LIKE '%wireless%' OR topics LIKE '%wifi%') AND topics NOT LIKE '%debug%' AND msg LIKE '%disconnected%'`)
	if err != nil {
		return nil // no device log yet
	}
	type d struct {
		ts       int64
		mac, dev string
	}
	var found []d
	for rows.Next() {
		var ts int64
		var dev, msg string
		rows.Scan(&ts, &dev, &msg)
		if m := reDiscMAC.FindStringSubmatch(msg); m != nil {
			found = append(found, d{ts, NormMAC(strings.ToUpper(m[1])), dev})
		}
	}
	rows.Close()
	for _, x := range found {
		db.Exec(`INSERT INTO wifi_disc(ts,mac,device) VALUES(?,?,?)`, x.ts, x.mac, x.dev)
	}
	return nil
}

// AddWifiDisc records one disconnect of a client (MAC upper case) seen in the log of `device`.
func (s *Store) AddWifiDisc(ts int64, mac, device string) {
	s.DB.Exec(`INSERT INTO wifi_disc(ts,mac,device) VALUES(?,?,?)`, ts, NormMAC(mac), device)
}

// wifiDiscPeak returns, per client, the disconnects of its worst clock hour since `since`. A burst of six in five
// minutes is a bad hour even if the other 23 are quiet; an average over the whole window would hide it.
func (s *Store) wifiDiscPeak(since int64) map[string]int {
	out := map[string]int{}
	rows, err := s.DB.Query(`SELECT mac, max(c) FROM (SELECT mac, ts/3600 AS h, count(*) AS c FROM wifi_disc WHERE ts>=? GROUP BY mac, h) GROUP BY mac`, since)
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
