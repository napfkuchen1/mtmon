package store

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
