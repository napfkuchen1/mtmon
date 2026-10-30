package store

import (
	"database/sql"
	"strings"
)

// Audit log (#192): who did what. Written by the API for every successful change, login and token use; kept 180 days.

const AuditKeepDays = 180

func migrateAudit(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS audit(
  id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, user TEXT NOT NULL, ip TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS audit_ts ON audit(ts)`)
	return err
}

type AuditEntry struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	User   string `json:"user"`
	IP     string `json:"ip"`
	Action string `json:"action"`
	Detail string `json:"detail"`
}

func (s *Store) AddAudit(e AuditEntry) error {
	_, err := s.DB.Exec(`INSERT INTO audit(ts,user,ip,action,detail) VALUES(?,?,?,?,?)`, e.TS, e.User, e.IP, e.Action, e.Detail)
	return err
}

// Audit lists entries newest first. before>0 continues after an earlier page (entry id); prefix filters the action.
func (s *Store) Audit(limit int, before int64, prefix string) []AuditEntry {
	out := []AuditEntry{}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id,ts,user,ip,action,detail FROM audit WHERE (?=0 OR id<?) AND action LIKE ? ESCAPE '\' ORDER BY id DESC LIMIT ?`
	esc := strings.NewReplacer(`\`, `\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
	rows, err := s.DB.Query(q, before, before, esc, limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var e AuditEntry
		if rows.Scan(&e.ID, &e.TS, &e.User, &e.IP, &e.Action, &e.Detail) == nil {
			out = append(out, e)
		}
	}
	return out
}

// PruneAudit deletes entries older than the retention.
func (s *Store) PruneAudit(now int64) {
	s.DB.Exec(`DELETE FROM audit WHERE ts<?`, now-int64(AuditKeepDays)*86400)
}

// MetaAll returns every meta entry (used to see which settings a request changed).
func (s *Store) MetaAll() map[string]string {
	out := map[string]string{}
	rows, err := s.DB.Query(`SELECT k,v FROM meta`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[k] = v
		}
	}
	return out
}
