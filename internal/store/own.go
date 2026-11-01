// SPDX-License-Identifier: AGPL-3.0-only

package store

import "database/sql"

// MonitorSvc labels the traffic between mtmon and the routers it manages (REST/API, NetFlow/IPFIX, syslog, SNMP).
// Like SpeedtestSvc it is mtmon's own traffic: kept in the data, but left out of the top lists and totals unless asked for.
const MonitorSvc = "mtmon monitoring"

// ownSvcs is the SQL list of the service labels that count as mtmon's own traffic.
const ownSvcs = `('` + SpeedtestSvc + `','` + MonitorSvc + `')`

// IsOwnSvc reports whether a flow with this service label is mtmon's own traffic.
func IsOwnSvc(svc string) bool { return svc == SpeedtestSvc || svc == MonitorSvc }

// migrateOwn adds the own-traffic bytes to the hourly per-client totals (that table has no service column):
// the lists subtract them again unless the user asked to see mtmon's own traffic.
func migrateOwn(db *sql.DB) error {
	for _, col := range []string{"o_up", "o_down", "o_int"} {
		var n int
		db.QueryRow(`SELECT count(*) FROM pragma_table_info('rollup_1h_client') WHERE name=?`, col).Scan(&n)
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE rollup_1h_client ADD COLUMN ` + col + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
	}
	return backfillOwn(db)
}

// oldMonitorSvcs are the service names the router-to-mtmon traffic carried before it had a label of its own.
const oldMonitorSvcs = `('NetFlow','Syslog','SNMP','SNMP-trap','RouterOS-API','RouterOS-API-SSL')`

// backfillOwn runs once for data written before the own-traffic columns covered internal bytes (#279): purely
// internal rows (both ends local) of the monitoring services get the monitoring label, and the per-client hourly
// totals take their own-traffic bytes from the hourly rollup, which knows the service.
func backfillOwn(db *sql.DB) error {
	var done string
	db.QueryRow(`SELECT v FROM meta WHERE k='own_backfill'`).Scan(&done)
	if done == "2" {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range []string{"rollup_1m", "rollup_1h"} {
		if _, err := tx.Exec(`UPDATE `+t+` SET svc=? WHERE svc IN `+oldMonitorSvcs+` AND b_int>0 AND b_up=0 AND b_down=0`, MonitorSvc); err != nil {
			return err
		}
	}
	for col, src := range map[string]string{"o_up": "b_up", "o_down": "b_down", "o_int": "b_int"} {
		if _, err := tx.Exec(`UPDATE rollup_1h_client SET ` + col + `=coalesce((SELECT sum(h.` + src + `) FROM rollup_1h h
			WHERE h.ts=rollup_1h_client.ts AND h.mac=rollup_1h_client.mac AND h.svc IN ` + ownSvcs + `),0)`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(k,v) VALUES('own_backfill','2')`); err != nil {
		return err
	}
	return tx.Commit()
}
