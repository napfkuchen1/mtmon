// SPDX-License-Identifier: AGPL-3.0-only

package live

import (
	"net/netip"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func dnsPipe(t *testing.T) *Pipeline {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir(), NoTLS: true, LocalNets: []string{"192.168.0.0/16"}}
	cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewPipeline(cfg, st, enrich.New("", "", "", false), NewHub())
}

func dnsRec(dst string, sport uint16) flow.Record {
	now := time.Now()
	return flow.Record{Src: netip.MustParseAddr("192.168.0.50"), Dst: netip.MustParseAddr(dst), SrcPort: sport, DstPort: 53,
		Proto: 17, Bytes: 80, Packets: 1, Start: now, End: now}
}

// A DNS query redirected by dst-nat is exported twice: to the original public server and to the router.
// Only the copy that went to the router counts, whatever the order or batch.
func TestDNSRedirectCountedOnce(t *testing.T) {
	p := dnsPipe(t)
	p.Handle([]flow.Record{dnsRec("8.8.8.8", 38209), dnsRec("192.168.0.1", 38209)}) // same batch
	if n := p.St.QueueLen(); n != 1 {
		t.Fatalf("same batch: %d rows", n)
	}
	p.Handle([]flow.Record{dnsRec("192.168.0.1", 40001)})
	p.Handle([]flow.Record{dnsRec("1.1.1.1", 40001)}) // later batch
	if n := p.St.QueueLen(); n != 2 {
		t.Fatalf("later batch: %d rows", n)
	}
	p.Handle([]flow.Record{dnsRec("8.8.8.8", 40002)}) // no redirect: genuine bypass stays visible
	if n := p.St.QueueLen(); n != 3 {
		t.Fatalf("unredirected: %d rows", n)
	}
}

func tunnelRec(proto uint8, src, dst string, bytes uint64) flow.Record {
	now := time.Now()
	return flow.Record{Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst), Proto: proto, Bytes: bytes, Packets: 10, Start: now, End: now}
}

// DS-Lite: the outer IPv6 packet (proto 4) router <-> AFTR is a copy of the inner IPv4 traffic and not usage.
func TestTunnelOuterNotCounted(t *testing.T) {
	p := dnsPipe(t)
	p.Cfg.LocalNets = append(p.Cfg.LocalNets, "2001:db8:1::/64")
	if err := p.Cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	p.SelfMAC = func(ip string) string {
		if ip == "2001:db8:1::1" {
			return "aa:bb:cc:00:00:01"
		}
		return ""
	}
	inner := tunnelRec(6, "192.168.0.50", "93.184.216.34", 1000)
	outerUp := tunnelRec(4, "2001:db8:1::1", "2001:db8:ffff::1", 1100)
	outerDown := tunnelRec(4, "2001:db8:ffff::1", "2001:db8:1::1", 5000)
	p.Handle([]flow.Record{inner, outerUp, outerDown})
	if n := p.St.QueueLen(); n != 1 {
		t.Fatalf("rows %d, want only the inner flow", n)
	}
	if p.IgnoredCount() != 2 {
		t.Errorf("ignored %d", p.IgnoredCount())
	}
	p.Handle([]flow.Record{tunnelRec(47, "2001:db8:1::1", "203.0.113.9", 500)}) // router's own GRE: outer
	if n := p.St.QueueLen(); n != 1 {
		t.Fatalf("router GRE counted: %d rows", n)
	}
	p.Handle([]flow.Record{tunnelRec(47, "192.168.0.50", "203.0.113.9", 500)}) // client's own GRE: stays
	if n := p.St.QueueLen(); n != 2 {
		t.Fatalf("client GRE dropped: %d rows", n)
	}
}

// WireGuard / Back to Home / IPsec: the transport packets that end on a managed router are not usage, the inner
// flows are. A client's own WireGuard or IPsec (not router-owned) stays counted.
func TestTunnelTransportUDPNotCounted(t *testing.T) {
	p := dnsPipe(t)
	p.Cfg.LocalNets = append(p.Cfg.LocalNets, "2001:db8:1::/64")
	if err := p.Cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	p.SelfMAC = func(ip string) string {
		if ip == "2001:db8:1::1" {
			return "aa:bb:cc:00:00:01"
		}
		return ""
	}
	p.TunnelPort = func(ip string, port uint16) bool { return ip == "2001:db8:1::1" && port == 13231 }
	udp := func(src, dst string, sp, dp uint16, bytes uint64) flow.Record {
		r := tunnelRec(17, src, dst, bytes)
		r.SrcPort, r.DstPort = sp, dp
		return r
	}
	inner := tunnelRec(6, "192.168.0.50", "93.184.216.34", 1000)
	p.Handle([]flow.Record{
		inner,
		udp("2001:db8:1::1", "2001:db8:ffff::1", 13231, 40000, 1100), // WireGuard outer, router sends
		udp("2001:db8:ffff::1", "2001:db8:1::1", 40000, 13231, 5000), // ... and receives
		udp("2001:db8:1::1", "203.0.113.9", 4500, 4500, 700),         // NAT-T of the router
		tunnelRec(50, "203.0.113.9", "2001:db8:1::1", 900),           // ESP to the router
	})
	if n := p.St.QueueLen(); n != 1 {
		t.Fatalf("rows %d, want only the inner flow", n)
	}
	if p.IgnoredCount() != 4 {
		t.Errorf("ignored %d, want 4", p.IgnoredCount())
	}
	p.Handle([]flow.Record{
		udp("192.168.0.50", "203.0.113.9", 51820, 51820, 800), // a client's own WireGuard
		udp("192.168.0.50", "203.0.113.9", 4500, 4500, 600),   // a client's own IPsec
		tunnelRec(50, "192.168.0.50", "203.0.113.9", 500),
	})
	if n := p.St.QueueLen(); n != 4 {
		t.Fatalf("client tunnels dropped: %d rows, want 4", n)
	}
}

// A client with a DNS server that does not exist: the redirect answers with the imaginary address as source.
// That address is no client; the sighting is remembered for the suggestion.
func TestGhostDNSReplyIsNoClient(t *testing.T) {
	p := dnsPipe(t)
	p.Hub.SetIPMap(map[string]string{"192.168.0.50": "AA:BB:CC:00:00:50", "192.168.0.1": "AA:BB:CC:00:00:01"}, nil)
	now := time.Now()
	reply := func(src string) flow.Record {
		return flow.Record{Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr("192.168.0.50"), SrcPort: 53, DstPort: 40000,
			Proto: 17, Bytes: 120, Packets: 1, Start: now, End: now}
	}
	p.Handle([]flow.Record{reply("192.168.77.77"), reply("192.168.77.77"), reply("192.168.77.77"), reply("192.168.0.1")})
	if n := p.St.QueueLen(); n != 1 {
		t.Fatalf("rows %d, want only the reply of the real router", n)
	}
	g, err := p.St.GhostDNSSince(now.Unix() - 60)
	if err != nil || len(g) != 1 || g[0].IP != "192.168.77.77" || g[0].MAC != "AA:BB:CC:00:00:50" || g[0].Flows != 3 {
		t.Fatalf("ghost sightings: %v %v", g, err)
	}
}

// #235: an address a managed device owns (here the router's LAN address, which no ARP/lease table lists) is never an
// imaginary DNS server; an address in no table still is.
func TestGhostDNSOwnedAddressIsNoGhost(t *testing.T) {
	p := dnsPipe(t)
	p.Hub.SetIPMap(map[string]string{"192.168.0.50": "AA:BB:CC:00:00:50"}, nil)
	p.Owns = func(ip string) bool { return ip == "192.168.0.1" || ip == "fd00::1" }
	now := time.Now()
	reply := func(src string) flow.Record {
		return flow.Record{Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr("192.168.0.50"), SrcPort: 53, DstPort: 40000,
			Proto: 17, Bytes: 120, Packets: 1, Start: now, End: now}
	}
	p.Handle([]flow.Record{reply("192.168.0.1"), reply("192.168.0.1"), reply("192.168.0.1"), reply("192.168.77.77")})
	g, err := p.St.GhostDNSSince(now.Unix() - 60)
	if err != nil || len(g) != 1 || g[0].IP != "192.168.77.77" {
		t.Fatalf("ghost sightings: %v %v", g, err)
	}
}

func expRec(exp, src, dst string, bytes uint64) flow.Record {
	now := time.Now()
	return flow.Record{Exporter: netip.MustParseAddr(exp), Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst),
		SrcPort: 40000, DstPort: 443, Proto: 6, Bytes: bytes, Packets: 5, Start: now, End: now}
}

// Two routers on the same path export the same 5-tuple (upload and download): only the first copy is booked (#286).
func TestSecondExporterCopyNotCounted(t *testing.T) {
	p := dnsPipe(t)
	p.Handle([]flow.Record{expRec("192.168.0.1", "192.168.0.50", "1.1.1.1", 1000), expRec("192.168.0.2", "192.168.0.50", "1.1.1.1", 1000)})
	if n := p.St.QueueLen(); n != 1 {
		t.Fatalf("upload: %d rows, want 1", n)
	}
	p.Handle([]flow.Record{expRec("192.168.0.1", "1.1.1.1", "192.168.0.50", 5000), expRec("192.168.0.2", "1.1.1.1", "192.168.0.50", 5100)})
	if n := p.St.QueueLen(); n != 2 {
		t.Fatalf("download: %d rows, want 2", n)
	}
	// the same exporter reporting the tuple again (next active-timeout record) still counts
	p.Handle([]flow.Record{expRec("192.168.0.1", "192.168.0.50", "1.1.1.1", 700)})
	if n := p.St.QueueLen(); n != 3 {
		t.Fatalf("same exporter again: %d rows, want 3", n)
	}
	if got := p.Diagnostics()["dedup_dropped"].(map[string]int64)["192.168.0.2"]; got != 2 {
		t.Errorf("dedup counter: %d, want 2", got)
	}
}

// Double NAT: the upstream gateway sees the downstream router's transfer address as client of the same traffic.
func TestDoubleNATRouterNotClient(t *testing.T) {
	p := dnsPipe(t)
	macs := map[string]string{"192.168.0.1": "aa:00:00:00:00:01", "192.168.1.2": "aa:00:00:00:00:02", "192.168.0.2": "aa:00:00:00:00:02"}
	p.SelfMAC = func(ip string) string { return macs[ip] }
	// downstream router (192.168.0.2 = its address towards the gateway) exports its client's flow
	p.Handle([]flow.Record{expRec("192.168.0.2", "192.168.1.50", "1.1.1.1", 1000)})
	before := p.St.QueueLen()
	// the gateway reports the same bytes with the downstream router's address as source
	p.Handle([]flow.Record{expRec("192.168.0.1", "192.168.0.2", "1.1.1.1", 1000)})
	if n := p.St.QueueLen(); n != before {
		t.Fatalf("gateway copy booked: %d rows, was %d", n, before)
	}
	// a router that does not export itself stays a normal client of the gateway
	p2 := dnsPipe(t)
	p2.SelfMAC = func(ip string) string { return macs[ip] }
	p2.Handle([]flow.Record{expRec("192.168.0.1", "192.168.0.2", "1.1.1.1", 1000)})
	if n := p2.St.QueueLen(); n != 1 {
		t.Fatalf("silent router: %d rows, want 1", n)
	}
}
