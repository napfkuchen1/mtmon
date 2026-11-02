// SPDX-License-Identifier: AGPL-3.0-only

package store

import "database/sql"

// Login sessions survive a restart (an update would otherwise log everybody out). Only the SHA-256 of the cookie
// value is stored, so a copy of the database does not hand out valid sessions.

// CreateSession stores a new session with its role and credential fingerprint in one write, so a session can never
// exist without them (a separate role update that failed used to leave a viewer session with the admin role "").
func (s *Store) CreateSession(hash string, idleUntil, born int64, role, cred string) error {
	_, err := s.DB.Exec(`INSERT INTO sessions(hash,idle_until,born,role,cred) VALUES(?,?,?,?,?)`, hash, idleUntil, born, role, cred)
	return err
}

// TouchSession extends the idle deadline of an existing session. It never creates one.
func (s *Store) TouchSession(hash string, idleUntil int64) {
	s.DB.Exec(`UPDATE sessions SET idle_until=? WHERE hash=?`, idleUntil, hash)
}

// StoredSession is one row of the sessions table.
type StoredSession struct {
	IdleUntil, Born int64
	Role, Cred      string
}

// LoadSession returns a stored session.
func (s *Store) LoadSession(hash string) (StoredSession, bool) {
	var x StoredSession
	err := s.DB.QueryRow(`SELECT idle_until,born,role,cred FROM sessions WHERE hash=?`, hash).Scan(&x.IdleUntil, &x.Born, &x.Role, &x.Cred)
	return x, err == nil
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

// Credential binding: every session stores a fingerprint of the password hash it was opened with. When the password
// changes or the account is removed, the fingerprint no longer matches and the session ends. Sessions from before
// this column have an empty cred and end once (one new login after the update).
func migrateSessionCred(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('sessions') WHERE name='cred'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(`ALTER TABLE sessions ADD COLUMN cred TEXT NOT NULL DEFAULT ''`)
	return err
}
