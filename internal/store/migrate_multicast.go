// SPDX-License-Identifier: AGPL-3.0-only

package store

import "database/sql"

// multicastRIP matches the text form of a multicast or limited-broadcast address in a rip column.
const multicastRIP = `(rip GLOB '22[4-9].*' OR rip GLOB '23[0-9].*' OR rip = '255.255.255.255' OR lower(rip) GLOB 'ff*:*')`

// migrateMulticast books traffic that older versions counted as internet upload/download to multicast or
// broadcast addresses (mDNS, SSDP, IGMP, DHCPv6) as internal traffic (#290). Runs once.
func migrateMulticast(db *sql.DB) error {
	var done string
	db.QueryRow(`SELECT v FROM meta WHERE k='multicast_rebooked'`).Scan(&done)
	if done == "1" {
		return nil
	}
	for _, t := range []string{"rollup_1m", "rollup_1h", "rollup_1d"} {
		if _, err := db.Exec(`UPDATE ` + t + ` SET b_int=b_int+b_up+b_down, b_up=0, b_down=0 WHERE (b_up>0 OR b_down>0) AND ` + multicastRIP); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`UPDATE flows SET dir='i' WHERE dir IN ('u','d') AND ` + multicastRIP); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO meta(k,v) VALUES('multicast_rebooked','1')`)
	return err
}
