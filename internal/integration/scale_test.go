package integration

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daniel/mtmon/internal/store"
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
