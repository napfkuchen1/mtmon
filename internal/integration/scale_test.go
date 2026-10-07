package integration

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/advice"
	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// TestScale fills the DB with a realistic 100-client / 30-day data set and measures size + query latency.
// Run: MTMON_SCALE=1 go test ./internal/integration -run TestScale -v -timeout 20m
func TestScale(t *testing.T) {
	if os.Getenv("MTMON_SCALE") == "" {
		t.Skip("set MTMON_SCALE=1 to run")
	}
	dir := os.Getenv("MTMON_SCALE_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rng := rand.New(rand.NewSource(7))
	const clients, dests = 100, 2000
	now := time.Now().Unix()
	ins := func(table string, step int64, days int, perClient int, activeHours int) int {
		tx, _ := st.DB.Begin()
		stmt, _ := tx.Prepare(`INSERT OR IGNORE INTO ` + table + `(ts,mac,rip,rport,proto,svc,host,cc,asn,asorg,b_up,b_down,b_int,flows) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		n := 0
		for d := 0; d < days; d++ {
			for ts := now - int64(d)*86400; ts > now-int64(d+1)*86400; ts -= step {
				hour := (ts / 3600) % 24
				if hour < int64(24-activeHours) { // active window
					continue
				}
				for c := 0; c < clients; c++ {
					for k := 0; k < perClient; k++ {
						z := int(rng.ExpFloat64()*float64(dests)/6) % dests
						stmt.Exec(ts/step*step, fmt.Sprintf("AA:BB:CC:00:%02X:%02X", c/256, c%256), fmt.Sprintf("10.%d.%d.%d", 100+z/65536, z/256%256, z%256),
							443, 6, "HTTPS", "", "", 0, "", rng.Intn(50000), rng.Intn(500000), 0, 1+rng.Intn(5))
						n++
					}
				}
			}
		}
		tx.Commit()
		stmt.Close()
		return n
	}
	t0 := time.Now()
	nm := ins("rollup_1m", 60, 3, 6, 8)
	nh := ins("rollup_1h", 3600, 30, 40, 8)
	t.Logf("generated rollup_1m=%d rows, rollup_1h=%d rows in %v", nm, nh, time.Since(t0).Round(time.Millisecond))
	st.DB.Exec(`INSERT INTO rollup_1h_client SELECT ts,mac,sum(b_up),sum(b_down),sum(b_int),sum(flows) FROM rollup_1h GROUP BY ts,mac`)
	st.DB.Exec(`INSERT INTO rollup_1d SELECT ts/86400*86400,rip,rport,proto,max(svc),max(host),max(cc),max(asn),max(asorg),sum(b_up),sum(b_down),sum(b_int),sum(flows) FROM rollup_1h GROUP BY ts/86400*86400,rip,rport,proto`)
	// raw flows: 3 days
	tx, _ := st.DB.Begin()
	stmt, _ := tx.Prepare(`INSERT INTO flows(ts,exporter,mac,cip,rip,cport,rport,proto,dir,bytes,pkts,flags,host,cc,asn,asorg,svc) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	raw := 0
	for ts := now - 3*86400; ts < now; ts += 60 {
		if h := (ts / 3600) % 24; h < 16 {
			continue
		}
		for c := 0; c < clients; c++ {
			for k := 0; k < 12; k++ {
				stmt.Exec(ts+int64(k), "10.0.0.1", fmt.Sprintf("AA:BB:CC:00:%02X:%02X", c/256, c%256), "192.168.88.10", fmt.Sprintf("10.100.%d.%d", rng.Intn(8), rng.Intn(250)),
					40000, 443, 6, "u", rng.Intn(100000), 10, 0, "", "", 0, "", "HTTPS")
				raw++
			}
		}
	}
	tx.Commit()
	stmt.Close()
	// data for the Suggestions rules: firewall log (400k events over 3 days), device metrics (30 days, 1/min),
	// 100 clients with IP history and roaming events
	seedAdvice(st, rng, now, clients)
	var fwOnce sync.Once
	cfg := &config.Config{}
	cfg.Devices = []config.Device{{Name: "gw", Addr: "10.0.0.1", Role: "router"}, {Name: "ap1", Addr: "10.0.0.2", Role: "ap"}, {Name: "ap2", Addr: "10.0.0.3", Role: "ap"}}
	st.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	fi, _ := os.Stat(filepath.Join(dir, "mtmon.db"))
	t.Logf("raw flows=%d  DB size=%.0f MB", raw, float64(fi.Size())/1e6)

	for _, q := range []struct {
		name string
		f    func() error
	}{
		{"top clients 30d (hourly/client)", func() error { _, e := st.TopBy("clients", now-30*86400, 20, ""); return e }},
		{"top clients 24h", func() error { _, e := st.TopBy("clients", now-86400, 20, ""); return e }},
		{"top hosts 24h", func() error { _, e := st.TopBy("hosts", now-86400, 20, ""); return e }},
		{"top hosts 7d", func() error { _, e := st.TopBy("hosts", now-7*86400, 20, ""); return e }},
		{"top hosts 30d (hourly)", func() error { _, e := st.TopBy("hosts", now-30*86400, 20, ""); return e }},
		{"top services 30d", func() error { _, e := st.TopBy("services", now-30*86400, 20, ""); return e }},
		{"services list 30d", func() error { _, e := st.Services(now - 30*86400); return e }},
		{"service detail 30d", func() error { _, e := st.ServiceDetail("HTTPS", now-30*86400, 14400); return e }},
		{"services of client 30d", func() error { _, e := st.ServicesOfClient("AA:BB:CC:00:00:05", now-30*86400); return e }},
		{"firewall summary 30d", func() error { _, e := st.FwSummary(now - 30*86400); return e }},
		{"series 30d", func() error { _, e := st.Series(now-30*86400, 14400, ""); return e }},
		{"series 24h", func() error { _, e := st.Series(now-86400, 900, ""); return e }},
		{"client top hosts 30d", func() error { _, e := st.TopBy("hosts", now-30*86400, 10, "AA:BB:CC:00:00:05"); return e }},
		{"client connections 1h", func() error { _, e := st.Connections("AA:BB:CC:00:00:05", now-3600, 200, 0); return e }},
		{"totals 30d", func() error { st.Totals(now - 30*86400); return nil }},
		{"advice: seed firewall log", func() error { fwOnce.Do(func() { seedFw(st, rng, now) }); return nil }},
		{"advice: device metrics 6h", func() error { _, e := st.DevMetricStats(now-6*3600, 70, 85); return e }},
		{"advice: port use 24h", func() error { _, e := st.PortUse(now-86400, advice.PortsOut); return e }},
		{"advice: DoH hosts 24h", func() error { _, e := st.HostUse(now-86400, 443, advice.DoHSuffixes()); return e }},
		{"advice: inbound raw 1h", func() error { _, e := st.InboundUse(now-3600, advice.PortsInbound); return e }},
		{"advice: blocked sources 24h", func() error { _, _, e := st.BlockedSources(now-86400, 300); return e }},
		{"advice: roams + ip churn", func() error { st.RoamStats(now-86400, 20); _, e := st.IPChurn(now-30*86400, 3); return e }},
		{"advice: full evaluation", func() error {
			advice.Evaluate(advice.NewLive(st, cfg, nil, nil, time.Now().Add(-time.Hour)))
			return nil
		}},
		{"client apps dests 30d", func() error { _, e := st.ClientDests("AA:BB:CC:00:00:05", now-30*86400, 3000); return e }},
		{"client apps dests 24h", func() error { _, e := st.ClientDests("AA:BB:CC:00:00:05", now-86400, 3000); return e }},
		{"client apps dests 1h", func() error { _, e := st.ClientDests("AA:BB:CC:00:00:05", now-3600, 3000); return e }},
		{"client apps active", func() error { _, e := st.ClientActiveDests("AA:BB:CC:00:00:05", 120); return e }},
		{"top dests (apps) 30d", func() error { _, e := st.TopDests(now-30*86400, 3000); return e }},
		{"top dests (apps) 7d", func() error { _, e := st.TopDests(now-7*86400, 3000); return e }},
		{"top dests (apps) 24h", func() error { _, e := st.TopDests(now-86400, 3000); return e }},
		{"top dests (apps) 1h", func() error { _, e := st.TopDests(now-3600, 3000); return e }},
	} {
		best := time.Hour
		for i := 0; i < 3; i++ {
			s := time.Now()
			if err := q.f(); err != nil {
				t.Fatal(q.name, err)
			}
			if d := time.Since(s); d < best {
				best = d
			}
		}
		t.Logf("%-28s %8v", q.name, best.Round(time.Microsecond))
		if best > 500*time.Millisecond {
			t.Errorf("%s too slow: %v (gate 500ms)", q.name, best)
		}
	}
	s := time.Now()
	st.Cleanup(3, 30)
	t.Logf("cleanup: %v", time.Since(s).Round(time.Millisecond))
}

// seedFw adds 400k firewall log events over 3 days. It runs right before the advice queries so the existing
// (unrelated) firewall summary measurement is not affected.
func seedFw(st *store.Store, rng *rand.Rand, now int64) {
	tx, _ := st.DB.Begin()
	tx.Exec(`INSERT OR IGNORE INTO fw_rules(device,prefix,chain,action,descr,managed) VALUES('gw','drop-in','input','drop','drop input',1),('gw','acc','forward','accept','accept',1),('gw','old','forward','drop','old rule',0)`)
	ev, _ := tx.Prepare(`INSERT INTO fw_events(ts,device,prefix,chain,in_if,out_if,proto,flags,src,sport,dst,dport,nat,len,mac,cstate) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	for i := 0; i < 400000; i++ {
		ts := now - int64(rng.Intn(3*86400))
		ev.Exec(ts, "gw", "drop-in", "input", "ether1", "", "tcp", "", fmt.Sprintf("%d.%d.%d.%d", 45+rng.Intn(100), rng.Intn(256), rng.Intn(256), 1+rng.Intn(250)),
			40000, "203.0.113.2", 22, "", 60, "", "new")
	}
	ev.Close()
	tx.Exec(`INSERT OR IGNORE INTO fw_hourly SELECT ts/3600*3600,'gw',prefix,count(*) FROM fw_events GROUP BY 1,3`)
	tx.Commit()
}

func seedAdvice(st *store.Store, rng *rand.Rand, now int64, clients int) {
	tx, _ := st.DB.Begin()
	m, _ := tx.Prepare(`INSERT INTO dev_metrics(ts,device,cpu,mem_used,mem_total,temp,uptime,clients,up) VALUES(?,?,?,?,?,?,?,?,1)`)
	for _, d := range []string{"gw", "ap1", "ap2"} {
		for ts := now - 30*86400; ts < now; ts += 60 {
			m.Exec(ts, d, rng.Float64()*40, 100+rng.Intn(100), 256, 40, ts, 10)
		}
	}
	m.Close()
	cl, _ := tx.Prepare(`INSERT OR IGNORE INTO clients(mac,hostname,vendor,ip,first_seen,last_seen,device,wifi,online) VALUES(?,?,?,?,?,?,?,?,1)`)
	ip, _ := tx.Prepare(`INSERT OR IGNORE INTO ip_history(ip,mac,first,last) VALUES(?,?,?,?)`)
	ro, _ := tx.Prepare(`INSERT INTO roam(ts,mac,from_dev,to_dev) VALUES(?,?,?,?)`)
	for c := 0; c < clients; c++ {
		mac := fmt.Sprintf("AA:BB:CC:00:%02X:%02X", c/256, c%256)
		cl.Exec(mac, fmt.Sprintf("host%d", c), "Vendor", fmt.Sprintf("192.168.88.%d", c), now-40*86400, now, "ap1", c%2)
		for k := 0; k < 5; k++ {
			ip.Exec(fmt.Sprintf("192.168.%d.%d", k, c), mac, now-int64(k)*86400, now-int64(k)*3600)
		}
		for k := 0; k < 100; k++ {
			ro.Exec(now-int64(rng.Intn(30*86400)), mac, "ap1", "ap2")
		}
	}
	cl.Close()
	ip.Close()
	ro.Close()
	tx.Commit()
}
