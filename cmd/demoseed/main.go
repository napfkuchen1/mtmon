// SPDX-License-Identifier: AGPL-3.0-only

// demoseed fills a fresh mtmon data directory with fictional history for the demo and the screenshots: traffic
// rollups (36 h per minute, 30 days per hour) with countries, firewall events, a few alerts and device metrics.
// Remote addresses are in the documentation ranges (RFC 5737), clients are the mockros demo clients (RFC 1918).
//
//	demoseed -dir <data_dir>      (run before mtmon starts; scripts/demo.sh does)
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/mockros"
	"github.com/napfkuchen1/mtmon/internal/store"
)

type dest struct {
	ip, host, cc, org string
	asn               int
	port              int
	proto             int
	w                 float64 // popularity
	down              float64 // typical bytes down per flow bucket
}

var dests = []dest{
	{"192.0.2.10", "video.example.net", "DE", "Example Streaming GmbH", 64500, 443, 6, 10, 900000},
	{"192.0.2.20", "cdn.example.net", "DE", "Example CDN", 64501, 443, 17, 8, 500000},
	{"192.0.2.130", "mail.example.org", "NL", "Example Hosting BV", 64502, 993, 6, 2, 40000},
	{"198.51.100.30", "social.example.com", "US", "Example Social Inc", 64510, 443, 6, 7, 300000},
	{"198.51.100.90", "api.example.com", "US", "Example Cloud Inc", 64511, 443, 6, 6, 150000},
	{"198.51.100.44", "updates.example.org", "US", "Example Software Corp", 64512, 80, 6, 3, 700000},
	{"198.51.100.7", "imap.example.net", "FR", "Example Telecom SA", 64520, 993, 6, 1, 30000},
	{"203.0.113.12", "music.example.com", "SE", "Example Audio AB", 64530, 443, 6, 4, 250000},
	{"203.0.113.150", "games.example.net", "GB", "Example Games Ltd", 64540, 443, 17, 3, 400000},
	{"203.0.113.80", "iot.example.org", "IE", "Example IoT Cloud Ltd", 64550, 8883, 6, 5, 8000},
	{"203.0.113.200", "dns.example.net", "CH", "Example DNS AG", 64560, 53, 17, 3, 3000},
	{"192.0.2.55", "vpn.example.org", "AT", "Example Net GmbH", 64570, 51820, 17, 1, 120000},
}

// weight per client host name; unlisted hosts get 1
var heavy = map[string]float64{"appletv-living": 9, "samsung-tv": 7, "pc-office": 5, "macbook-ben": 5, "iphone-anna": 4, "ps5-living": 4,
	"nas-synology": 3, "galaxy-s24": 3, "ipad-kids": 3, "thinkpad-work": 3, "cam-driveway": 2, "cam-garden": 2, "wg-laptop-remote": 2}

func diurnal(ts int64) float64 {
	h := float64(ts%86400)/3600 + 1 // roughly local evening peak
	return 0.2 + 0.8*math.Max(0, math.Sin(math.Pi*(math.Mod(h+24-6, 24))/17))
}

func pick(rng *rand.Rand, ws []float64) int {
	t := 0.0
	for _, w := range ws {
		t += w
	}
	r := rng.Float64() * t
	for i, w := range ws {
		if r -= w; r < 0 {
			return i
		}
	}
	return len(ws) - 1
}

func main() {
	dir := flag.String("dir", "", "mtmon data directory (created if missing)")
	list := flag.Bool("list-ips", false, "print the demo client IPs (comma separated) for flowgen and exit")
	flag.Parse()
	if *list {
		ips := []string{}
		for _, c := range mockros.DemoClients() {
			ips = append(ips, c.IP)
		}
		fmt.Println(strings.Join(ips, ","))
		return
	}
	if *dir == "" {
		log.Fatal("demoseed: -dir is required")
	}
	st, err := store.Open(*dir)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	rng := rand.New(rand.NewSource(42))
	now := time.Now().Unix()
	clients := mockros.DemoClients()
	cw := make([]float64, len(clients))
	for i, c := range clients {
		cw[i] = 1
		if w, ok := heavy[c.Host]; ok {
			cw[i] = w
		}
	}
	dw := make([]float64, len(dests))
	for i, d := range dests {
		dw[i] = d.w
	}

	tx, err := st.DB.Begin()
	if err != nil {
		log.Fatal(err)
	}
	ins := func(table string, step int64, from int64, perSlot int) int {
		stmt, err := tx.Prepare(`INSERT OR IGNORE INTO ` + table + `(ts,mac,rip,rport,proto,svc,host,cc,asn,asorg,b_up,b_down,b_int,flows) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			log.Fatal(err)
		}
		defer stmt.Close()
		n := 0
		for ts := from / step * step; ts < now; ts += step {
			f := diurnal(ts)
			for k := 0; k < int(float64(perSlot)*(0.4+f)); k++ {
				c := clients[pick(rng, cw)]
				d := dests[pick(rng, dw)]
				scale := float64(step) / 60
				down := int64(d.down * (0.3 + rng.Float64()) * f * scale)
				up := down / int64(6+rng.Intn(10))
				stmt.Exec(ts, c.MAC, d.ip, d.port, d.proto, enrich.Service(uint8(d.proto), uint16(d.port), 0), d.host, d.cc, d.asn, d.org, up, down, 0, 1+rng.Intn(6))
				n++
			}
			// some LAN-internal traffic (NAS, printer, Home Assistant)
			if rng.Intn(3) == 0 {
				c := clients[pick(rng, cw)]
				stmt.Exec(ts, c.MAC, "192.168.88.12", 445, 6, "SMB", "", "", 0, "", 0, 0, int64(20000+rng.Intn(400000))*step/60, 1)
				n++
			}
		}
		return n
	}
	n1 := ins("rollup_1m", 60, now-36*3600, 7)
	n2 := ins("rollup_1h", 3600, now-30*86400, 70)
	tx.Exec(`INSERT OR IGNORE INTO rollup_1h_client SELECT ts,mac,sum(b_up),sum(b_down),sum(b_int),sum(flows) FROM rollup_1h GROUP BY ts,mac`)
	tx.Exec(`INSERT OR IGNORE INTO rollup_1d SELECT ts/86400*86400,rip,rport,proto,max(svc),max(host),max(cc),max(asn),max(asorg),sum(b_up),sum(b_down),sum(b_int),sum(flows) FROM rollup_1h GROUP BY ts/86400*86400,rip,rport,proto`)

	// device metrics: 6 h, one per minute
	m, _ := tx.Prepare(`INSERT INTO dev_metrics(ts,device,cpu,mem_used,mem_total,temp,uptime,clients,up) VALUES(?,?,?,?,?,?,?,?,1)`)
	for _, d := range mockros.DemoDevices()[:4] {
		cpu := map[string]float64{"gw-main": 12, "sw-office": 4, "ap-living": 9, "ap-garden": 6}[d.Name]
		for ts := now - 6*3600; ts < now; ts += 60 {
			m.Exec(ts, d.Name, cpu*(0.6+0.8*rng.Float64())*(0.6+diurnal(ts)), 300000000+rng.Intn(50000000), 1024000000, 38+rng.Float64()*6, ts-now+26*3600, 6)
		}
	}
	m.Close()

	// firewall: rules, blocked scans from the internet, a few allowed (logged) forwards
	tx.Exec(`INSERT OR IGNORE INTO fw_rules(device,prefix,chain,action,descr,managed) VALUES
		('gw-main','drop-wan-in','input','drop','drop all not coming from LAN',0),
		('gw-main','drop-wan-fwd','forward','drop','drop all from WAN not DSTNATed',0),
		('gw-main','allow-ha-https','forward','accept','HTTPS to Home Assistant',0)`)
	ev, _ := tx.Prepare(`INSERT INTO fw_events(ts,device,prefix,chain,in_if,out_if,proto,flags,src,sport,dst,dport,nat,len,mac,cstate) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	ports := []int{22, 23, 80, 443, 445, 3389, 5900, 8080, 8291}
	for i := 0; i < 1800; i++ {
		ts := now - int64(rng.Float64()*rng.Float64()*86400*3) // denser towards now
		src := fmt.Sprintf("198.51.100.%d", 100+int(rng.ExpFloat64()*12)%120)
		if rng.Intn(2) == 0 {
			src = fmt.Sprintf("203.0.113.%d", 20+int(rng.ExpFloat64()*15)%200)
		}
		if rng.Intn(25) == 0 {
			ev.Exec(ts, "gw-main", "allow-ha-https", "forward", "ether1", "bridge", "tcp", "SYN", src, 40000+rng.Intn(20000), "192.168.88.30", 8123, "", 60, "", "new")
			continue
		}
		pre, ch := "drop-wan-in", "input"
		if rng.Intn(3) == 0 {
			pre, ch = "drop-wan-fwd", "forward"
		}
		ev.Exec(ts, "gw-main", pre, ch, "ether1", "", "tcp", "SYN", src, 40000+rng.Intn(20000), "203.0.113.2", ports[rng.Intn(len(ports))], "", 60, "", "new")
	}
	ev.Close()
	tx.Exec(`INSERT OR IGNORE INTO fw_hourly SELECT ts/3600*3600,device,prefix,count(*) FROM fw_events GROUP BY 1,2,3`)
	tx.Exec(`INSERT OR IGNORE INTO fw_src_hourly SELECT ts/3600*3600,device,prefix,src,count(*) FROM fw_events GROUP BY 1,2,3,4`)
	tx.Exec(`INSERT OR IGNORE INTO fw_dst_hourly SELECT ts/3600*3600,device,prefix,dst,dport,count(*) FROM fw_events GROUP BY 1,2,3,4,5`)
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}

	// a few alerts (plain English text; the UI shows Msg when there is no message key)
	for _, a := range []store.Alert{
		{TS: now - 7200, Kind: "new_device", Subject: "iphone-guest", Severity: "info", Msg: "New device iphone-guest (192.168.88.27) joined ap-garden"},
		{TS: now - 5400, Kind: "port_scan", Subject: "198.51.100.112", Severity: "warning", Msg: "198.51.100.112 probed 9 ports on the WAN address within 5 minutes"},
		{TS: now - 3600, Kind: "traffic_spike", Subject: "appletv-living", Severity: "info", Msg: "appletv-living: download 4.2x above its usual level"},
		{TS: now - 1800, Kind: "wifi_weak", Subject: "esp32-irrigation", Severity: "warning", Msg: "esp32-irrigation has a weak Wi-Fi signal on ap-garden (-74 dBm)"},
		{TS: now - 900, Kind: "update_available", Subject: "ap-living", Severity: "info", Msg: "RouterOS update available for ap-living"},
	} {
		if _, err := st.AddAlert(a); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("demoseed: rollup_1m=%d rollup_1h=%d rows, 1800 firewall events, 5 alerts\n", n1, n2)
}
