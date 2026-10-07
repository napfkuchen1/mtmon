package store

import (
	"testing"
	"time"
)

func TestReviewInbox(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	s.DB.Exec(`INSERT INTO clients(mac,first_seen,last_seen,online) VALUES('AA:00:00:00:00:01',?,?,1)`, now, now)
	s.DB.Exec(`INSERT INTO clients(mac,first_seen,last_seen,online) VALUES('AA:00:00:00:00:02',?,?,0)`, now, now)
	if n := s.UnreviewedCount(); n != 2 {
		t.Fatalf("new clients must start unreviewed, got %d", n)
	}
	n, err := s.ReviewClients([]ReviewItem{{MAC: "aa:00:00:00:00:01", Label: " TV "}, {MAC: "AA:00:00:00:00:99"}})
	if err != nil || n != 1 {
		t.Fatalf("review: %d %v", n, err)
	}
	if s.UnreviewedCount() != 1 {
		t.Fatal("reviewed client still counted")
	}
	if c, _ := s.Client("AA:00:00:00:00:01"); c.Label != "TV" {
		t.Fatalf("label %q", c.Label)
	}
	s.DB.Exec(`UPDATE clients SET hostname='h' WHERE mac='AA:00:00:00:00:01'`) // has a name already: not "unnamed"
	if un, _ := s.Inbox("unnamed", 0); len(un) != 1 || un[0].MAC != "AA:00:00:00:00:02" {
		t.Fatalf("unnamed: %+v", un)
	}
	// reopen: migration must not mark rows reviewed again / must be idempotent
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.UnreviewedCount() != 1 {
		t.Fatal("reopen changed review state")
	}
}

func TestMigrateMarksExistingReviewed(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.DB.Exec(`INSERT INTO clients(mac,first_seen,last_seen) VALUES('AA:00:00:00:00:01',1,1)`)
	s.DB.Exec(`ALTER TABLE clients DROP COLUMN reviewed`) // simulate a database from before this version
	s.Close()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.UnreviewedCount() != 0 {
		t.Fatal("pre-existing clients must start reviewed")
	}
}

func TestTotalsBetween(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	now := time.Now().Unix()
	if _, _, _, ok := s.TotalsBetween(now-7200, now-3600); ok {
		t.Fatal("no data: window must not count as covered")
	}
	h := (now - 5400) / 60 * 60
	s.DB.Exec(`INSERT INTO rollup_1m(ts,mac,rip,rport,proto,b_up,b_down,b_int,flows) VALUES(?,?,?,?,?,?,?,?,?)`, h-3600*0, "m", "1.1.1.1", 443, 6, 5, 7, 0, 1)
	s.DB.Exec(`INSERT INTO rollup_1m(ts,mac,rip,rport,proto,b_up,b_down,b_int,flows) VALUES(?,?,?,?,?,?,?,?,?)`, now-7300, "m", "1.1.1.1", 443, 6, 1, 1, 0, 1)
	up, down, fl, ok := s.TotalsBetween(now-7200, now-3600)
	if !ok || up != 5 || down != 7 || fl != 1 {
		t.Fatalf("got %d %d %d %v", up, down, fl, ok)
	}
}
