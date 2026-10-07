package store

import (
	"testing"
	"time"
)

func TestConnectionsFiltered(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().Unix()
	ins := func(mac, rip string, rport, proto int, svc, cc string) {
		if _, err := s.DB.Exec(`INSERT INTO flows(ts,exporter,mac,cip,rip,cport,rport,proto,dir,bytes,pkts,flags,host,cc,asn,asorg,svc)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, now, "r", mac, "10.0.0.2", rip, 50000, rport, proto, "u", 10, 1, 0, "", cc, 0, "", svc); err != nil {
			t.Fatal(err)
		}
	}
	m := "AA:00:00:00:00:01"
	ins(m, "1.1.1.1", 443, 6, "Netflix", "US")
	ins(m, "2.2.2.2", 443, 6, "YouTube", "DE")
	ins(m, "2.2.2.2", 53, 17, "", "DE")
	ins("AA:00:00:00:00:02", "1.1.1.1", 443, 6, "Netflix", "US") // other client
	cases := []struct {
		name string
		f    ConnFilter
		want int
	}{
		{"none", ConnFilter{}, 3},
		{"service", ConnFilter{Svc: "Netflix"}, 1},
		{"ip", ConnFilter{RIP: "2.2.2.2"}, 2},
		{"port", ConnFilter{Proto: 17, RPort: 53}, 1},
		{"country", ConnFilter{CC: "DE"}, 2},
		{"combined", ConnFilter{RIP: "2.2.2.2", CC: "US"}, 0},
	}
	for _, c := range cases {
		got, err := s.ConnectionsFiltered(m, 0, 100, 0, c.f)
		if err != nil || len(got) != c.want {
			t.Errorf("%s: %d rows, want %d (%v)", c.name, len(got), c.want, err)
		}
	}
}

func TestConnectionsTowardsClientAndUnattributed(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().Unix()
	a, b := "AA:00:00:00:00:01", "AA:00:00:00:00:02"
	s.DB.Exec(`INSERT INTO ip_history(ip,mac,first,last) VALUES('10.0.0.1',?,1,1),('10.0.0.2',?,1,1)`, a, b)
	ins := func(mac, cip, rip, dir string, cport, rport int) {
		s.DB.Exec(`INSERT INTO flows(ts,exporter,mac,cip,rip,cport,rport,proto,dir,bytes,pkts,flags,host,cc,asn,asorg,svc)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, now, "r", mac, cip, rip, cport, rport, 6, dir, 10, 1, 0, "", "", 0, "", "")
	}
	ins(a, "10.0.0.1", "8.8.8.8", "u", 50000, 53)   // a -> internet
	ins(b, "10.0.0.2", "10.0.0.1", "i", 40000, 22)  // b -> a (LAN)
	ins("", "10.0.0.1", "9.9.9.9", "u", 50001, 443) // not attributed yet, but a's address
	ins(b, "10.0.0.2", "8.8.8.8", "u", 50002, 53)   // someone else entirely
	got, err := s.ConnectionsFiltered(a, 0, 100, 0, ConnFilter{})
	if err != nil || len(got) != 3 {
		t.Fatalf("a: %d rows (%v)", len(got), err)
	}
	var lan *Conn
	for i := range got {
		if got[i].Dir == "i" {
			lan = &got[i]
		}
	}
	if lan == nil || lan.RIP != "10.0.0.2" || lan.RPort != 40000 || lan.CPort != 22 {
		t.Fatalf("LAN flow must be shown from a's point of view: %+v", lan)
	}
	// with a filter only a's own flows are searched
	if got, _ := s.ConnectionsFiltered(a, 0, 100, 0, ConnFilter{RIP: "8.8.8.8"}); len(got) != 1 {
		t.Fatalf("filtered: %d", len(got))
	}
}
