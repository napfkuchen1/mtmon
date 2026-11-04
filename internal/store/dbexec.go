// SPDX-License-Identifier: AGPL-3.0-only

package store

import "log/slog"

// exec runs a statement whose failure the caller cannot act on (retention deletes, counters, best-effort updates).
// A failure (disk full, locked database) is logged with the start of the statement so it leaves a trace (#284);
// the arguments are never logged.
func (s *Store) exec(query string, args ...any) {
	if _, err := s.DB.Exec(query, args...); err != nil {
		q := query
		if len(q) > 60 {
			q = q[:60]
		}
		slog.Error("database write failed", "stmt", q, "err", err)
	}
}
