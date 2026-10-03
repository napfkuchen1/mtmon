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
