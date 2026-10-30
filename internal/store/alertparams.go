package store

import (
	"database/sql"
	"encoding/json"
)

// migrateAlertParams adds the message key and its parameters to alerts (#195). A keyed alert is shown in the
// language of the viewer from key + params; alerts without a key (old ones, kinds not converted yet) show the stored text.
func migrateAlertParams(db *sql.DB) error {
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
	for _, c := range []string{"msgkey", "params"} {
		if !has[c] {
			if _, err := db.Exec(`ALTER TABLE alerts ADD COLUMN ` + c + ` TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		}
	}
	return nil
}

// alertParams decodes the stored parameters; broken or empty JSON gives nil (the alert then shows its stored text).
func alertParams(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	return m
}
