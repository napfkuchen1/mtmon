// SPDX-License-Identifier: AGPL-3.0-only

package store

import "testing"

// #290: old rollup/flow rows to multicast or broadcast addresses move from internet to internal, once.
func TestMigrateMulticastRebooks(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db := st.DB
	for _, rip := range []string{"224.0.0.251", "239.255.255.250", "255.255.255.255", "ff02::1:2", "8.8.8.8", "223.1.1.1"} {
		if _, err := db.Exec(`INSERT INTO rollup_1m(ts,mac,rip,rport,proto,b_up,b_down,b_int,flows) VALUES(60,'m',?,1,17,100,50,7,1)`, rip); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO rollup_1d(ts,rip,rport,proto,b_up,b_down,b_int,flows) VALUES(60,?,1,17,100,50,7,1)`, rip); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO flows(ts,rip,dir,bytes) VALUES(60,?,'u',100)`, rip); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(`DELETE FROM meta WHERE k='multicast_rebooked'`)
	if err := migrateMulticast(db); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		rows, err := db.Query(`SELECT rip, b_up, b_down, b_int FROM rollup_1m`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var rip string
			var up, down, in int64
			rows.Scan(&rip, &up, &down, &in)
			switch rip {
			case "8.8.8.8", "223.1.1.1": // unicast stays internet
				if up != 100 || down != 50 || in != 7 {
					t.Errorf("%s changed: %d/%d/%d", rip, up, down, in)
				}
			default:
				if up != 0 || down != 0 || in != 157 {
					t.Errorf("%s not rebooked: %d/%d/%d", rip, up, down, in)
				}
			}
		}
		var u int
		db.QueryRow(`SELECT count(*) FROM flows WHERE dir='u'`).Scan(&u)
		if u != 2 {
			t.Errorf("%d flows still dir=u, want the 2 unicast ones", u)
		}
		var d int
		db.QueryRow(`SELECT count(*) FROM rollup_1d WHERE b_up>0`).Scan(&d)
		if d != 2 {
			t.Errorf("rollup_1d: %d rows with upload, want 2", d)
		}
	}
	check()
	// runs once: a second call must not add the bytes again
	if err := migrateMulticast(db); err != nil {
		t.Fatal(err)
	}
	check()
}
