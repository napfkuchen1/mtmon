package store

import (
	"testing"
	"time"
)

func seedClient(t *testing.T, s *Store, mac, label, ip string, ageDays int, online bool) {
	t.Helper()
	ls := time.Now().Add(-time.Duration(ageDays) * 24 * time.Hour).Unix()
	if _, err := s.DB.Exec(`INSERT INTO clients(mac,hostname,vendor,ip,label,first_seen,last_seen,online) VALUES(?,?,?,?,?,?,?,?)`,
		mac, "h-"+mac, "V", ip, label, ls-100, ls, b2i(online)); err != nil {
		t.Fatal(err)
	}
	s.DB.Exec(`INSERT INTO ip_history(ip,mac,first,last) VALUES(?,?,?,?)`, ip, mac, ls, ls)
	s.DB.Exec(`INSERT INTO roam(ts,mac,from_dev,to_dev) VALUES(?,?,?,?)`, ls, mac, "a", "b")
	s.DB.Exec(`INSERT INTO seen_dest(mac,rip,first) VALUES(?,?,?)`, mac, "1.1.1.1", ls)
	s.DB.Exec(`INSERT INTO rollup_1h(ts,mac,rip,rport,proto,b_up,b_down,b_int,flows) VALUES(?,?,?,?,?,?,?,?,?)`, ls/3600*3600, mac, "1.1.1.1", 443, 6, 10, 20, 0, 1)
}

func count(t *testing.T, s *Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCleanupClients(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedClient(t, s, "AA:00:00:00:00:01", "", "192.168.1.10", 200, false)   // stale, unlabeled
	seedClient(t, s, "AA:00:00:00:00:02", "TV", "192.168.1.11", 200, false) // stale, labeled
	seedClient(t, s, "AA:00:00:00:00:03", "", "192.168.1.12", 10, false)    // recent
	seedClient(t, s, "AA:00:00:00:00:04", "", "192.168.1.13", 400, true)    // stale timestamp but online
	seedClient(t, s, "AA:00:00:00:00:05", "", "192.168.1.1", 300, false)    // managed device by IP
	seedClient(t, s, "AA:00:00:00:00:06", "", "192.168.1.14", 300, false)   // managed device by MAC
	base := CleanupOpts{Days: 90, KeepLabeled: true, ProtectIPs: []string{"192.168.1.1"}, ProtectMACs: []string{"aa:00:00:00:00:06"}}

	if _, err := s.CleanupClients(CleanupOpts{Days: 3}); err == nil {
		t.Fatal("days<7 must be refused")
	}

	dry := base
	dry.DryRun = true
	r, err := s.CleanupClients(dry)
	if err != nil || r.Count != 1 || r.Removed != 0 || len(r.Sample) != 1 || r.Sample[0].MAC != "AA:00:00:00:00:01" {
		t.Fatalf("dry run: %+v %v", r, err)
	}
	if count(t, s, `SELECT count(*) FROM clients`) != 6 {
		t.Fatal("dry run deleted rows")
	}

	// without keep_labeled the labeled one is a candidate too
	nk := dry
	nk.KeepLabeled = false
	if r, _ := s.CleanupClients(nk); r.Count != 2 {
		t.Fatalf("keep_labeled=false: %+v", r)
	}
	// a long threshold finds nothing
	long := dry
	long.Days = 365
	if r, _ := s.CleanupClients(long); r.Count != 0 || r.Sample == nil {
		t.Fatalf("365d: %+v", r)
	}

	r, err = s.CleanupClients(base)
	if err != nil || r.Removed != 1 || r.Count != 1 {
		t.Fatalf("real run: %+v %v", r, err)
	}
	if count(t, s, `SELECT count(*) FROM clients`) != 5 || count(t, s, `SELECT count(*) FROM clients WHERE mac='AA:00:00:00:00:01'`) != 0 {
		t.Fatal("wrong rows removed")
	}
	for _, tbl := range []string{"ip_history", "roam", "seen_dest"} {
		if count(t, s, `SELECT count(*) FROM `+tbl+` WHERE mac='AA:00:00:00:00:01'`) != 0 {
			t.Fatalf("%s rows of removed client remain", tbl)
		}
		if count(t, s, `SELECT count(*) FROM `+tbl+` WHERE mac='AA:00:00:00:00:02'`) != 1 {
			t.Fatalf("%s rows of kept client removed", tbl)
		}
	}
	// aggregate traffic stays
	if count(t, s, `SELECT count(*) FROM rollup_1h`) != 6 {
		t.Fatal("rollups must be kept")
	}
	// online client untouched even with a tiny threshold + no keep_labeled
	all := CleanupOpts{Days: 30, ProtectIPs: base.ProtectIPs, ProtectMACs: base.ProtectMACs}
	r, _ = s.CleanupClients(all)
	if r.Removed != 1 { // only the labeled TV (200 d); online/recent/managed stay
		t.Fatalf("final: %+v", r)
	}
	if count(t, s, `SELECT count(*) FROM clients WHERE online=1`) != 1 || count(t, s, `SELECT count(*) FROM clients`) != 4 {
		t.Fatal("online/managed/recent must stay")
	}
}
