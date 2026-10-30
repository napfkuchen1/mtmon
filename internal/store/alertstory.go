package store

import "database/sql"

// migrateAlertStory adds the story columns (#188): an alert can be updated in place while related events keep
// arriving. cnt is the number of events folded into it, first_ts/last_ts the time span (0 = a plain one-event alert).
func migrateAlertStory(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(alerts)`)
	if err != nil {
		return err
	}
	has := map[string]bool{}
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &nn, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		has[name] = true
	}
	rows.Close()
	for _, c := range []string{"cnt", "first_ts", "last_ts"} {
		if !has[c] {
			if _, err := db.Exec(`ALTER TABLE alerts ADD COLUMN ` + c + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
	}
	return nil
}

// UpdateStory rewrites an alert that collects related events: text, severity and the counters.
func (s *Store) UpdateStory(a Alert) error {
	params := ""
	if len(a.Params) > 0 {
		params = encodeParams(a.Params)
	}
	_, err := s.DB.Exec(`UPDATE alerts SET severity=?, msg=?, msgkey=?, params=?, cnt=?, first_ts=?, last_ts=? WHERE id=?`,
		a.Severity, a.Msg, a.MsgKey, params, a.Count, a.First, a.Last, a.ID)
	return err
}
