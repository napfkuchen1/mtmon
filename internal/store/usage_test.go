package store

import (
	"testing"
	"time"
)

func TestClientsOfAndAllClientDests(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := time.Now().Unix() / 3600 * 3600
	ins := func(mac, rip string, port, proto, up, down int) {
		if _, err := st.DB.Exec(`INSERT INTO rollup_1h(ts,mac,rip,rport,proto,svc,host,cc,asn,asorg,b_up,b_down,b_int,flows) VALUES(?,?,?,?,?,'web','h.example','DE',64496,'Org',?,?,0,1)`,
			ts, mac, rip, port, proto, up, down); err != nil {
			t.Fatal(err)
		}
	}
	ins("AA", "203.0.113.1", 443, 6, 10, 90)
	ins("BB", "203.0.113.1", 443, 6, 5, 5)
	ins("BB", "203.0.113.2", 80, 6, 1, 1)
	since := ts - 3*3600
	for _, c := range []struct{ what, key string }{{"hosts", "203.0.113.1"}, {"ports", "6/443"}, {"asn", "64496"}, {"country", "DE"}, {"protocols", "6"}, {"services", "web"}} {
		cl, err := st.ClientsOf(c.what, c.key, since, 10)
		if err != nil || len(cl) == 0 || cl[0].MAC != "AA" {
			t.Errorf("%s/%s: %+v %v", c.what, c.key, cl, err)
		}
	}
	if cl, _ := st.ClientsOf("hosts", "203.0.113.1", since, 10); len(cl) != 2 || cl[0].Down != 90 || cl[1].MAC != "BB" {
		t.Errorf("hosts: %+v", cl)
	}
	if cl, err := st.ClientsOf("hosts", "198.51.100.9", since, 10); err != nil || cl == nil || len(cl) != 0 {
		t.Errorf("unknown host must give an empty, non-nil list: %+v %v", cl, err)
	}
	if _, err := st.ClientsOf("ports", "bogus", since, 10); err == nil {
		t.Error("bad port key must fail")
	}
	if _, err := st.ClientsOf("nope", "x", since, 10); err == nil {
		t.Error("unknown list must fail")
	}
	ds, err := st.AllClientDests(since, 0)
	if err != nil || len(ds) != 3 || ds[0].MAC != "AA" {
		t.Errorf("dests: %+v %v", ds, err)
	}
}
