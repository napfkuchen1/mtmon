// SPDX-License-Identifier: AGPL-3.0-only

package store

import "testing"

func TestTableBytes(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sz, err := st.TableBytes()
	if err != nil {
		t.Skipf("dbstat not available in this SQLite build: %v", err)
	}
	if sz["flows"] <= 0 {
		t.Errorf("flows table has no pages: %v", sz)
	}
	// callers get a copy: changing it must not change the cache
	sz["flows"] = -1
	again, _ := st.TableBytes()
	if again["flows"] == -1 {
		t.Errorf("cached map shared with caller")
	}
}
