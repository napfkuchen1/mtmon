// SPDX-License-Identifier: AGPL-3.0-only

package store

import "database/sql"

// stmt returns the statement for q, prepared once on the DB pool and reused by every flush.
// Without it each flush parsed the same SQL again inside its transaction (sqlite3RunParser was a
// large share of idle CPU). database/sql re-prepares per connection as needed; tx.Stmt binds the
// shared statement to a transaction.
func (s *Store) stmt(q string) (*sql.Stmt, error) {
	s.stmtMu.Lock()
	defer s.stmtMu.Unlock()
	if st, ok := s.stmts[q]; ok {
		return st, nil
	}
	st, err := s.DB.Prepare(q)
	if err != nil {
		return nil, err
	}
	if s.stmts == nil {
		s.stmts = map[string]*sql.Stmt{}
	}
	s.stmts[q] = st
	return st, nil
}

// txStmt is tx.Prepare with the statement cached on the Store. Close on the result is still safe
// and expected by callers (it only releases the transaction binding).
func (s *Store) txStmt(tx *sql.Tx, q string) (*sql.Stmt, error) {
	st, err := s.stmt(q)
	if err != nil {
		return nil, err
	}
	return tx.Stmt(st), nil
}
