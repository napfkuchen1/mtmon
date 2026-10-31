// SPDX-License-Identifier: AGPL-3.0-only

package store

import "database/sql"

// Login sessions survive a restart (an update would otherwise log everybody out). Only the SHA-256 of the cookie
// value is stored, so a copy of the database does not hand out valid sessions.

// SaveSession inserts or refreshes a session (unix seconds).
func (s *Store) SaveSession(hash string, idleUntil, born int64) {
	s.DB.Exec(`INSERT INTO sessions(hash,idle_until,born) VALUES(?,?,?)
		ON CONFLICT(hash) DO UPDATE SET idle_until=excluded.idle_until`, hash, idleUntil, born)
}

// LoadSession returns a stored session.
func (s *Store) LoadSession(hash string) (idleUntil, born int64, ok bool) {
	err := s.DB.QueryRow(`SELECT idle_until,born FROM sessions WHERE hash=?`, hash).Scan(&idleUntil, &born)
	return idleUntil, born, err == nil
}

// DropSession removes one session (logout, expiry).
func (s *Store) DropSession(hash string) { s.DB.Exec(`DELETE FROM sessions WHERE hash=?`, hash) }

// PruneSessions removes sessions that idled out before `idleBefore` or were born before `bornBefore`.
func (s *Store) PruneSessions(idleBefore, bornBefore int64) {
	s.DB.Exec(`DELETE FROM sessions WHERE idle_until<? OR born<?`, idleBefore, bornBefore)
}

// Roles (#192): sessions of the read-only "viewer" account carry the role "viewer"; "" means admin.
func migrateSessionRoles(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var dflt any
		if rows.Scan(&cid, &name, &typ, &nn, &dflt, &pk) == nil && name == "role" {
			has = true
		}
	}
	rows.Close()
	if has {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN role TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *Store) SetSessionRole(hash, role string) {
	s.DB.Exec(`UPDATE sessions SET role=? WHERE hash=?`, role, hash)
}

func (s *Store) SessionRole(hash string) string {
	var r string
	s.DB.QueryRow(`SELECT role FROM sessions WHERE hash=?`, hash).Scan(&r)
	return r
}
