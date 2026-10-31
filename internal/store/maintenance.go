// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"strconv"
	"strings"
	"time"
)

// Maintenance windows: while a device is in one, its offline and link-down alerts are recorded as info.
// Stored in meta as "maint:<device>" = unix time the window ends.

const maintPrefix = "maint:"

// SetMaintenance opens a window for the device until the given unix time; until <= 0 ends it.
func (s *Store) SetMaintenance(device string, until int64) error {
	if until <= 0 {
		_, err := s.DB.Exec(`DELETE FROM meta WHERE k=?`, maintPrefix+device)
		return err
	}
	return s.SetMeta(maintPrefix+device, strconv.FormatInt(until, 10))
}

// Maintenance returns the windows that are still open (device -> end, unix) and drops expired ones.
func (s *Store) Maintenance() map[string]int64 {
	out := map[string]int64{}
	rows, err := s.DB.Query(`SELECT k,v FROM meta WHERE k LIKE 'maint:%'`)
	if err != nil {
		return out
	}
	defer rows.Close()
	now := time.Now().Unix()
	var expired []string
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) != nil {
			continue
		}
		until, _ := strconv.ParseInt(v, 10, 64)
		if until > now {
			out[strings.TrimPrefix(k, maintPrefix)] = until
		} else {
			expired = append(expired, k)
		}
	}
	rows.Close()
	for _, k := range expired {
		s.DB.Exec(`DELETE FROM meta WHERE k=?`, k)
	}
	return out
}
