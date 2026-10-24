package store

import (
	"testing"
	"time"
)

// Lists over weeks read the per-dimension daily table; it is filled by every flush and, for older databases, from
// the daily destination rollups when the table is first created.
func TestDimRollupsMatchDestinationRollups(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().Add(-20*24*time.Hour).Unix() / 86400 * 86400
	st.Enqueue(
		FlowRow{TS: day + 10, MAC: "AA:00:00:00:00:01", CIP: "10.0.0.2", RIP: "1.1.1.1", RPort: 443, Proto: 6, Dir: "d", Bytes: 1000, Svc: "Web", CC: "DE", ASN: 13335, ASOrg: "Cloudflare"},
		FlowRow{TS: day + 20, MAC: "AA:00:00:00:00:01", CIP: "10.0.0.2", RIP: "1.0.0.1", RPort: 443, Proto: 6, Dir: "u", Bytes: 500, Svc: "Web", CC: "DE", ASN: 13335, ASOrg: "Cloudflare"},
		FlowRow{TS: day + 30, MAC: "AA:00:00:00:00:01", CIP: "10.0.0.2", RIP: "9.9.9.9", RPort: 53, Proto: 17, Dir: "u", Bytes: 70, Svc: "DNS", CC: "US", ASN: 19281, ASOrg: "Quad9"},
	)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-30 * 24 * time.Hour).Unix()
	check := func(st *Store) {
		t.Helper()
		for what, want := range map[string]struct {
			key  string
			tot  int64
			n    int
			sub  string
			desc string
		}{
			"services":  {"Web", 1500, 2, "", ""},
			"country":   {"DE", 1500, 2, "", ""},
			"asn":       {"13335", 1500, 2, "", "Cloudflare"},
			"ports":     {"6/443", 1500, 2, "Web", ""},
			"protocols": {"6", 1500, 2, "", ""},
		} {
			rows, err := st.TopBy(what, since, 10, "")
			if err != nil || len(rows) != want.n || rows[0].Key != want.key || rows[0].Up+rows[0].Down != want.tot || rows[0].Sub != want.sub {
				t.Errorf("%s: %+v %v", what, rows, err)
			}
			if want.desc != "" && rows[0].Label != want.desc {
				t.Errorf("%s label: %+v", what, rows[0])
			}
		}
		svcs, err := st.Services(since)
		if err != nil || len(svcs) != 2 || svcs[0].Name != "Web" || svcs[0].Up+svcs[0].Down != 1500 {
			t.Errorf("services: %+v %v", svcs, err)
		}
	}
	check(st)
	st.DB.Exec(`DROP TABLE rollup_1d_dim`)
	st.Close()
	if st, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	check(st)
}
