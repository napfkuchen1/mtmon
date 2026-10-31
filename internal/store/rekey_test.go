// SPDX-License-Identifier: AGPL-3.0-only

package store

import "testing"

func TestRekeyTrafficMergesBareAddressIntoClient(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const key, v6 = "VPN:192.168.216.4", "fd00::4"
	for _, q := range []string{
		`INSERT INTO rollup_1h(ts,mac,rip,rport,proto,b_up,b_down,b_int,flows) VALUES(3600,'` + key + `','1.1.1.1',443,6,10,0,0,1)`,
		`INSERT INTO rollup_1h(ts,mac,rip,rport,proto,b_up,b_down,b_int,flows) VALUES(3600,'` + v6 + `','1.1.1.1',443,6,5,100,0,2)`,
		`INSERT INTO rollup_1h(ts,mac,rip,rport,proto,b_up,b_down,b_int,flows) VALUES(7200,'` + v6 + `','2.2.2.2',80,6,1,2,0,1)`,
		`INSERT INTO rollup_1h_client(ts,mac,b_up,b_down,b_int,flows) VALUES(3600,'` + key + `',10,0,0,1)`,
		`INSERT INTO rollup_1h_client(ts,mac,b_up,b_down,b_int,flows) VALUES(3600,'` + v6 + `',6,102,0,3)`,
		`INSERT INTO rollup_1m(ts,mac,rip,rport,proto,b_up,b_down,b_int,flows) VALUES(60,'` + v6 + `','1.1.1.1',443,6,1,1,0,1)`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatal(err, q)
		}
	}
	for i := 0; i < 2; i++ { // idempotent
		if err := st.RekeyTraffic(v6, key); err != nil {
			t.Fatal(err)
		}
	}
	var up, down, fl, left int64
	st.DB.QueryRow(`SELECT b_up,b_down,flows FROM rollup_1h WHERE ts=3600 AND mac=? AND rip='1.1.1.1'`, key).Scan(&up, &down, &fl)
	if up != 15 || down != 100 || fl != 3 {
		t.Errorf("merged rollup: %d %d %d", up, down, fl)
	}
	st.DB.QueryRow(`SELECT b_up,b_down FROM rollup_1h_client WHERE ts=3600 AND mac=?`, key).Scan(&up, &down)
	if up != 16 || down != 102 {
		t.Errorf("merged client totals: %d %d", up, down)
	}
	st.DB.QueryRow(`SELECT (SELECT count(*) FROM rollup_1h WHERE mac=?1)+(SELECT count(*) FROM rollup_1m WHERE mac=?1)+(SELECT count(*) FROM rollup_1h_client WHERE mac=?1)`, v6).Scan(&left)
	if left != 0 {
		t.Errorf("%d rows left under the bare address", left)
	}
	if st.DB.QueryRow(`SELECT 1 FROM rollup_1h WHERE mac=? AND rip='2.2.2.2'`, key).Scan(&left) != nil {
		t.Error("row without a counterpart must move")
	}
}
