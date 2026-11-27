// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"time"
)

// TableBytes returns the on-disk size per table and index (SQLite dbstat), so the database size can be explained:
// raw flows, rollups, firewall events, indexes. Read-only and cached for an hour; nil when dbstat is not compiled in.
func (s *Store) TableBytes() (map[string]int64, error) {
	return memoize(&s.memo, "tablebytes", time.Hour, cloneSizes, func() (map[string]int64, error) {
		rows, err := s.DB.Query(`SELECT name, SUM(pgsize) FROM dbstat GROUP BY name`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := map[string]int64{}
		for rows.Next() {
			var n string
			var b int64
			if err := rows.Scan(&n, &b); err != nil {
				return nil, err
			}
			out[n] = b
		}
		return out, rows.Err()
	})
}

func cloneSizes(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
