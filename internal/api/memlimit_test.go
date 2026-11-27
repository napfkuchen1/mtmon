// SPDX-License-Identifier: AGPL-3.0-only

package api

import "testing"

func TestMemTooSmall(t *testing.T) {
	const mb = 1 << 20
	cases := []struct {
		name      string
		limit, db int64
		want      bool
	}{
		{"limit below db", 1024 * mb, 2048 * mb, true},
		{"limit above db", 2048 * mb, 2048 * mb, false},
		{"no limit", 0, 2048 * mb, false},
		{"small db never warns", 64 * mb, 150 * mb, false},
	}
	for _, c := range cases {
		if got := memTooSmall(c.limit, c.db); got != c.want {
			t.Errorf("%s: memTooSmall(%d,%d)=%v want %v", c.name, c.limit, c.db, got, c.want)
		}
	}
}
