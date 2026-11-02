// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func insertProto(t *testing.T, st *store.Store, mac, rip string, rport, proto int, dir, svc string) {
	t.Helper()
	_, err := st.DB.Exec(`INSERT INTO flows(ts,exporter,mac,cip,rip,cport,rport,proto,dir,bytes,pkts,svc) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		time.Now().Unix(), "gw", mac, "192.168.50.5", rip, 40000, rport, proto, dir, 100, 1, svc)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInternalPortScan(t *testing.T) {
	e, got := newEngine(t)
	for i := 0; i < 250; i++ { // an IoT client probes 250 ports of a LAN host
		insertProto(t, e.St, "aa:50", "192.168.0.10", 1000+i, 6, "i", "")
	}
	e.Checks()
	if kinds(*got) != "port_scan/aa:50" || (*got)[0].Severity != "warning" || !strings.Contains((*got)[0].Msg, "scans your LAN") {
		t.Fatalf("internal scan: %+v", *got)
	}
}

func TestInternalPingSweep(t *testing.T) {
	e, got := newEngine(t)
	for i := 0; i < 220; i++ { // echo request: type 8, code 0 -> 2048
		insertProto(t, e.St, "aa:51", "192.168.0."+itoa(i), 2048, 1, "i", "")
	}
	e.Checks()
	if kinds(*got) != "port_scan/aa:51" || !strings.Contains((*got)[0].Msg, "ICMP echo to 220 hosts") {
		t.Fatalf("ping sweep: %+v", *got)
	}
}

func TestICMPTypeIsNoPort(t *testing.T) {
	e, got := newEngine(t)
	for i := 0; i < 300; i++ { // many ICMP types/codes on one host, and unreachables: never ports
		insertProto(t, e.St, "aa:52", "192.168.0.10", 256*(i%40)+i%7, 1, "i", "")
	}
	e.Checks()
	if len(*got) != 0 {
		t.Fatalf("ICMP counted as ports: %s", kinds(*got))
	}
}

func TestInternalBrowsingNoScan(t *testing.T) {
	e, got := newEngine(t)
	for i := 0; i < 400; i++ { // 200+ LAN hosts on web ports (a dashboard, a browser) must not alert
		insertProto(t, e.St, "aa:53", "192.168.0."+itoa(i%250), 443, 6, "i", "")
	}
	e.Checks()
	if len(*got) != 0 {
		t.Fatalf("LAN browsing raised %s", kinds(*got))
	}
}

func TestScanExemptions(t *testing.T) {
	e, got := newEngine(t)
	if err := e.St.SaveGroup(store.Group{Name: "NMS", ChattyMult: 1, Scanner: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.St.DB.Exec(`INSERT INTO clients(mac, grp) VALUES('AA:54','NMS') ON CONFLICT(mac) DO UPDATE SET grp='NMS'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 250; i++ {
		insertProto(t, e.St, "aa:54", "192.168.0.10", 1000+i, 6, "i", "")               // scanner group
		insertProto(t, e.St, "aa:55", "192.168.0.10", 1000+i, 6, "i", store.MonitorSvc) // mtmon's own probes
	}
	e.Checks()
	if len(*got) != 0 {
		t.Fatalf("exempt clients alerted: %s", kinds(*got))
	}
}
