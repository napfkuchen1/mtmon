package store

import (
	"testing"
	"time"
)

func TestAdviceQueries(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()

	// dismissals: forever, snoozed, expired
	st.DismissSuggestion("a", 0)
	st.DismissSuggestion("b", 7)
	st.DB.Exec(`INSERT INTO advice_dismissed(id,until,created) VALUES('old',?,?)`, now-10, now-1000)
	d, err := st.DismissedSuggestions()
	if err != nil || len(d) != 2 || d["a"] != 0 || d["b"] < now+6*86400 {
		t.Fatalf("%v %v", d, err)
	}
	st.RestoreSuggestion("a")
	if d, _ = st.DismissedSuggestions(); len(d) != 1 {
		t.Fatalf("%v", d)
	}
	st.DismissSuggestion("b", 0) // re-dismiss for good overrides the snooze
	if d, _ = st.DismissedSuggestions(); d["b"] != 0 {
		t.Fatal(d)
	}

	// device metrics
	for i := 0; i < 10; i++ {
		st.SaveDevMetric(DevMetric{TS: now - int64(i)*60, Device: "wAP", CPU: 80, MemUsed: 90, MemTotal: 100, Up: true})
	}
	st.SaveDevMetric(DevMetric{TS: now, Device: "down", Up: false})
	ms, err := st.DevMetricStats(now-3600, 70, 85)
	if err != nil || len(ms) != 1 || ms["wAP"].N != 10 || ms["wAP"].HighCPU != 10 || ms["wAP"].AvgMem != 90 {
		t.Fatalf("%+v %v", ms, err)
	}

	// port use / host use / client totals from the hourly rollups
	ts := now / 3600 * 3600
	st.DB.Exec(`INSERT INTO rollup_1h(ts,mac,rip,rport,proto,svc,host,cc,asn,asorg,b_up,b_down,b_int,flows) VALUES
		(?,'AA','203.0.113.1',23,6,'Telnet','',  '',0,'',10,20,0,5),
		(?,'AA','203.0.113.2',443,6,'HTTPS','dns.google','',0,'',10,20,0,50),
		(?,'AA','203.0.113.3',443,6,'HTTPS','example.org','',0,'',10,20,0,50)`, ts, ts, ts)
	st.DB.Exec(`INSERT INTO rollup_1h_client(ts,mac,b_up,b_down,b_int,flows) VALUES(?,'AA',30,60,0,105)`, ts)
	pu, err := st.PortUse(now-86400, []int{23, 53})
	if err != nil || len(pu) != 1 || pu[0].RPort != 23 || pu[0].Flows != 5 || pu[0].Bytes != 30 {
		t.Fatalf("%+v %v", pu, err)
	}
	hu, err := st.HostUse(now-86400, 443, []string{"dns.google", "cloudflare-dns.com"})
	if err != nil || len(hu) != 1 || hu[0].Host != "dns.google" {
		t.Fatalf("%+v %v", hu, err)
	}
	ct, err := st.ClientTotals(now - 86400)
	if err != nil || len(ct) != 1 || ct[0].Fl != 105 {
		t.Fatalf("%+v %v", ct, err)
	}

	// roaming + IP churn
	for i := 0; i < 5; i++ {
		st.DB.Exec(`INSERT INTO roam(ts,mac,from_dev,to_dev) VALUES(?,?,?,?)`, now-int64(i), "AA", "ap1", "ap2")
	}
	st.DB.Exec(`INSERT INTO roam(ts,mac,from_dev,to_dev) VALUES(?,?,?,?)`, now, "BB", "ap1", "ap2")
	rs, err := st.RoamStats(now-86400, 3)
	if err != nil || len(rs) != 1 || rs[0].MAC != "AA" || rs[0].N != 5 || rs[0].APs != 1 {
		t.Fatalf("%+v %v", rs, err)
	}
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		st.DB.Exec(`INSERT INTO ip_history(ip,mac,first,last) VALUES(?,?,?,?)`, ip, "AA", now-100, now)
	}
	if ch, _ := st.IPChurn(now-86400, 3); ch["AA"] != 3 {
		t.Fatalf("%v", ch)
	}

	// raw inbound flows: download direction with the *client* port in the list
	st.DB.Exec(`INSERT INTO flows(ts,exporter,mac,cip,rip,cport,rport,proto,dir,bytes,pkts,flags,host,cc,asn,asorg,svc) VALUES
		(?,'x','AA','192.168.88.9','198.51.100.1',3389,50000,6,'d',10,1,0,'','',0,'',''),
		(?,'x','AA','192.168.88.9','198.51.100.2',3389,50001,6,'d',10,1,0,'','',0,'',''),
		(?,'x','AA','192.168.88.9','198.51.100.2',50002,443,6,'d',10,1,0,'','',0,'','')`, now, now, now)
	in, err := st.InboundUse(now-3600, []int{3389, 445})
	if err != nil || len(in) != 1 || in[0].Flows != 2 || in[0].Sources != 2 {
		t.Fatalf("%+v %v", in, err)
	}

	// firewall: blocked sources only count events of drop rules and skip LAN sources
	st.SaveFwRules([]FwRule{{Device: "gw", Prefix: "drop-in", Chain: "input", Action: "drop", Descr: "x"}, {Device: "gw", Prefix: "ok", Chain: "forward", Action: "accept"}})
	st.DB.Exec(`INSERT INTO fw_events(ts,device,prefix,chain,src,dst,dport) VALUES
		(?,'gw','drop-in','input','203.0.113.5','1.1.1.1',22),(?,'gw','drop-in','input','203.0.113.5','1.1.1.1',22),
		(?,'gw','drop-in','input','192.168.88.3','1.1.1.1',22),(?,'gw','ok','forward','203.0.113.6','1.1.1.1',22)`, now, now, now, now)
	bs, tot, err := st.BlockedSources(now-3600, 10)
	if err != nil || len(bs) != 1 || bs[0].IP != "203.0.113.5" || bs[0].Hits != 2 || tot != 2 {
		t.Fatalf("%+v %v %v", bs, tot, err)
	}
	st.DB.Exec(`INSERT INTO fw_hourly(ts,device,prefix,n) VALUES(?,'gw','drop-in',7)`, now/3600*3600-3600*30)
	hits, oldest, err := st.FwHits(now - 86400)
	if err != nil || len(hits) != 0 || oldest == 0 {
		t.Fatalf("%v %v %v", hits, oldest, err)
	}
}
