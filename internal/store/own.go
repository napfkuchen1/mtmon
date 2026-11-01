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
	for _, col := range []string{"o_up", "o_down"} {
		var n int
		db.QueryRow(`SELECT count(*) FROM pragma_table_info('rollup_1h_client') WHERE name=?`, col).Scan(&n)
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE rollup_1h_client ADD COLUMN ` + col + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
	}
	return nil
}
