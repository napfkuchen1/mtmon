package store

import (
	"database/sql"
	"time"
)

// migrateTokens creates the table for read-only API tokens (only a hash of the token is stored).
func migrateTokens(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS api_tokens(
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, hash TEXT NOT NULL UNIQUE, prefix TEXT NOT NULL,
  created INTEGER NOT NULL, last_used INTEGER NOT NULL DEFAULT 0)`)
	return err
}

// APIToken is the listable part of a token (never the secret itself).
type APIToken struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Prefix   string `json:"prefix"`
	Created  int64  `json:"created"`
	LastUsed int64  `json:"last_used"`
}

func (s *Store) CreateToken(name, hash, prefix string) (int64, error) {
	r, err := s.DB.Exec(`INSERT INTO api_tokens(name,hash,prefix,created) VALUES(?,?,?,?)`, name, hash, prefix, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) Tokens() ([]APIToken, error) {
	rows, err := s.DB.Query(`SELECT id,name,prefix,created,last_used FROM api_tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.Created, &t.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteToken(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM api_tokens WHERE id=?`, id)
	return err
}

// TokenValid reports whether a token with this hash exists and records its use (at most once a minute).
func (s *Store) TokenValid(hash string) bool {
	var id, last int64
	if err := s.DB.QueryRow(`SELECT id,last_used FROM api_tokens WHERE hash=?`, hash).Scan(&id, &last); err != nil {
		return false
	}
	if now := time.Now().Unix(); now-last > 60 {
		s.DB.Exec(`UPDATE api_tokens SET last_used=? WHERE id=?`, now, id)
	}
	return true
}
