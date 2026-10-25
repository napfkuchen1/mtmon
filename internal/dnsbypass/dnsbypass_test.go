package dnsbypass

import (
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func row(mac, rip, host string, port, proto int, flows int64) store.DNSUseRow {
	return store.DNSUseRow{MAC: mac, RIP: rip, Host: host, RPort: port, Proto: proto, Flows: flows, First: 1000, Last: 5000}
}

func TestClassifyAndThresholds(t *testing.T) {
	rows := []store.DNSUseRow{
		row("A", "1.1.1.1", "", 853, 6, 200),                  // DoT, public
		row("A", "9.9.9.9", "", 853, 17, 12),                  // DoQ
		row("B", "192.168.0.53", "", 853, 6, 500),             // local resolver: wanted, not a bypass
		row("B", "8.8.8.8", "", 53, 17, 2),                    // below the threshold
		row("C", "8.8.8.8", "dns.google", 53, 17, 30),         // external DNS
		row("C", "8.8.4.4", "dns.google", 443, 6, 10),         // DoH, too few connections
		row("D", "1.1.1.1", "cloudflare-dns.com", 443, 6, 90), // DoH browser
		row("E", "2606:4700:4700::1111", "", 853, 6, 5),       // IPv6 DoT
	}
	rows = append(rows, row("F", "8.8.8.8", "dns.google", 443, 6, 568)) // mtmon's own WAN probe
	got := fromRows(rows, []string{"8.8.8.8"})
	by := map[string]Client{}
	for _, c := range got {
		by[c.MAC] = c
	}
	if _, ok := by["B"]; ok {
		t.Errorf("a local resolver is no bypass, and 2 flows are no pattern: %+v", by["B"])
	}
	if c := by["A"]; len(c.Types) != 2 || c.Types[0] != DoT || c.Types[1] != DoQ || c.Flows != 212 {
		t.Errorf("A: %+v", c)
	}
	if c := by["C"]; len(c.Types) != 1 || c.Types[0] != DNS || len(c.Resolvers) != 1 {
		t.Errorf("C: DoH below 40 connections must not count: %+v", c)
	}
	if c := by["D"]; len(c.Types) != 1 || c.Types[0] != DoH {
		t.Errorf("D: %+v", c)
	}
	if c := by["E"]; len(c.Types) != 1 || c.Types[0] != DoT {
		t.Errorf("E: %+v", c)
	}
	if _, ok := by["F"]; ok {
		t.Errorf("the WAN probe target is no DoH: %+v", by["F"])
	}
	if len(got) != 4 || got[0].MAC != "A" {
		t.Errorf("busiest first, 4 clients: %+v", got)
	}
	if got[0].Types == nil || got[0].Resolvers == nil {
		t.Error("lists must not be nil")
	}
}

// DNSUse reads the hourly rollup: a client sending DoT shows up, the router (no MAC) and local traffic do not matter.
func TestDetectFromRollup(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	st.Enqueue(
		store.FlowRow{TS: now, MAC: "AA:00:00:00:00:01", CIP: "192.168.0.9", RIP: "1.1.1.1", RPort: 853, CPort: 40000, Proto: 6, Dir: "u", Bytes: 900, Svc: "DNS-over-TLS"},
		store.FlowRow{TS: now, MAC: "AA:00:00:00:00:01", CIP: "192.168.0.9", RIP: "1.1.1.1", RPort: 853, CPort: 40001, Proto: 6, Dir: "u", Bytes: 900, Svc: "DNS-over-TLS"},
		store.FlowRow{TS: now, MAC: "AA:00:00:00:00:01", CIP: "192.168.0.9", RIP: "1.1.1.1", RPort: 853, CPort: 40002, Proto: 6, Dir: "u", Bytes: 900, Svc: "DNS-over-TLS"},
	)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	got, err := Detect(st, now-3600)
	if err != nil || len(got) != 1 || got[0].MAC != "AA:00:00:00:00:01" || got[0].Types[0] != DoT {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestWatcherAlertsOnlyForNewBypassAndStartsQuiet(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	defer st.Close()
	now := time.Now()
	flows := func(mac, rip string, n int) {
		for i := 0; i < n; i++ {
			st.Enqueue(store.FlowRow{TS: now.Unix(), MAC: mac, CIP: "192.168.0.9", RIP: rip, RPort: 853, CPort: uint16Port(i), Proto: 6, Dir: "u", Bytes: 100})
		}
		st.Flush()
	}
	var raised []string
	w := &Watcher{St: st, Raise: func(kind, subject, sev, msg string) { raised = append(raised, kind+"|"+subject+"|"+sev) }}
	flows("AA:00:00:00:00:01", "1.1.1.1", 5)
	w.Check(now) // alert off: nothing, nothing recorded
	if len(raised) != 0 || st.GetMeta(metaSeen) != "" {
		t.Fatalf("off: %v", raised)
	}
	st.SetMeta(MetaAlert, "on")
	w.Check(now) // first check after switching on: records the existing state quietly
	if len(raised) != 0 || st.GetMeta(metaSeen) == "" {
		t.Fatalf("first check must be quiet: %v", raised)
	}
	flows("AA:00:00:00:00:02", "9.9.9.9", 4)
	w.Check(now)
	w.Check(now) // the same client is reported once
	if len(raised) != 1 || raised[0] != "dns_bypass|AA:00:00:00:00:02|DoT|info" {
		t.Fatalf("raised: %v", raised)
	}
}

func uint16Port(i int) uint16 { return uint16(40000 + i) }
