// SPDX-License-Identifier: AGPL-3.0-only

package store

// SetWatch marks clients as "must be online" (or clears it). Unknown MACs are ignored.
func (s *Store) SetWatch(macs []string, on bool) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range macs {
		if _, err := tx.Exec(`UPDATE clients SET watch=? WHERE mac=?`, b2i(on), NormMAC(m)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// WatchedClients lists the clients that must be online.
func (s *Store) WatchedClients() ([]Client, error) {
	rows, err := s.DB.Query(`SELECT ` + clientCols + ` FROM clients WHERE watch=1 ORDER BY mac`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Client{}
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// WatchCounts returns how many clients are watched and how many of them are online right now.
func (s *Store) WatchCounts() (total, online int) {
	s.DB.QueryRow(`SELECT count(*), coalesce(sum(online),0) FROM clients WHERE watch=1`).Scan(&total, &online)
	return
}
