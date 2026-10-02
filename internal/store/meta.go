package store

// GetMeta returns a value from the small key/value meta table ("" when unset).
func (s *Store) GetMeta(k string) string {
	var v string
	if err := s.DB.QueryRow(`SELECT v FROM meta WHERE k=?`, k).Scan(&v); err != nil {
		return ""
	}
	return v
}

// SetMeta stores a value in the meta table.
func (s *Store) SetMeta(k, v string) error {
	_, err := s.DB.Exec(`INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}
