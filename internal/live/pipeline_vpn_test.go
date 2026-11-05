// SPDX-License-Identifier: AGPL-3.0-only

package live

import (
	"net/netip"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/store"
)

const (
	vpnPeerA  = "192.168.216.2"
	vpnPeerB  = "192.168.216.3"
	vpnNAT    = "192.0.0.2" // the router's address on the DS-Lite tunnel
	vpnServer = "93.184.216.34"
	vpnWG     = 96
	vpnWAN    = 85
)

// vpnPipe is a router with a WireGuard interface and the given active peers.
func vpnPipe(t *testing.T, active ...string) *Pipeline {
	t.Helper()
	p := netPipe(t, "192.168.0.0/16")
	p.Cfg.Devices = []config.Device{{Name: "gw", Addr: "192.168.0.1"}}
	p.SelfMAC = func(ip string) string {
		if ip == vpnNAT || ip == "192.168.0.1" {
			return "AA:BB:CC:00:00:01"
		}
		return ""
	}
	var ifc VPNIface
	for _, a := range active {
		ifc.Active = append(ifc.Active, netip.MustParseAddr(a))
	}
	p.VPNIf = func(dev string, idx uint32) (VPNIface, bool) { return ifc, dev == "gw" && idx == vpnWG }
	return p
}

func vpnRec(src, dst string, sp, dp uint16, in, out uint32, bytes uint64) flow.Record {
	now := time.Now()
	return flow.Record{Exporter: netip.MustParseAddr("192.168.0.1"), Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst),
		SrcPort: sp, DstPort: dp, Proto: 6, Bytes: bytes, Packets: 10, Start: now, End: now, InIf: in, OutIf: out}
}

// vpnBooked drains the queued rows through the store and returns the per-client bytes.
func vpnBooked(t *testing.T, p *Pipeline) map[string][2]int64 {
	t.Helper()
	p.St.Flush()
	rows, err := p.St.DB.Query(`SELECT mac, sum(b_up), sum(b_down) FROM rollup_1h_if GROUP BY mac`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var mac string
		var up, down int64
		if err := rows.Scan(&mac, &up, &down); err != nil {
			t.Fatal(err)
		}
		out[mac] = [2]int64{up, down}
	}
	return out
}

func vpnCounter(p *Pipeline, key string) int64 {
	n, _ := p.Diagnostics()[key].(int64)
	return n
}

// The pair from a real Back to Home download: upload from the peer, reply to the router's NAT address on the tunnel.
func TestVPNReplyMatchesUploadOfPeer(t *testing.T) {
	for _, order := range []string{"upload first", "reply first"} {
		t.Run(order, func(t *testing.T) {
			p := vpnPipe(t, vpnPeerA, vpnPeerB) // two active peers: only the upload can tell them apart
			up := vpnRec(vpnPeerA, vpnServer, 60118, 80, vpnWG, vpnWAN, 2000)
			down := vpnRec(vpnServer, vpnNAT, 80, 60118, vpnWAN, vpnWG, 6435916)
			other := vpnRec(vpnPeerB, "93.184.216.35", 50000, 443, vpnWG, vpnWAN, 700)
			if order == "upload first" {
				p.Handle([]flow.Record{up, other, down})
			} else {
				p.Handle([]flow.Record{down, other, up})
			}
			got := vpnBooked(t, p)
			if a := got[vpnPeerA]; a != [2]int64{2000, 6435916} {
				t.Errorf("peer A %v, all %v", a, got)
			}
			if a := got[vpnPeerB]; a != [2]int64{700, 0} {
				t.Errorf("peer B %v", a)
			}
			if _, ok := got[store.VPNUnassignedMAC]; ok {
				t.Errorf("booked as not attributable: %v", got)
			}
			if vpnCounter(p, "vpn_replies_matched") != 1 || vpnCounter(p, "vpn_replies_unassigned") != 0 {
				t.Errorf("counters: %v", p.Diagnostics())
			}
		})
	}
}

// vpnExpire ends the wait of every buffered reply of an exporter whose timeout is not known.
func vpnExpire(p *Pipeline) { p.SweepVPN(time.Now().Add((vpnHoldDefault + 1) * time.Second)) }

// Each half of a flow has its own export timers: two peers active, the reply arrives first, its upload in a later
// batch. The reply waits and lands on the right peer, even though the other peer talked to that server before.
func TestVPNReplyBeforeUploadTwoPeers(t *testing.T) {
	p := vpnPipe(t, vpnPeerA, vpnPeerB)
	p.Handle([]flow.Record{vpnRec(vpnPeerB, vpnServer, 50000, 443, vpnWG, vpnWAN, 700)}) // the wrong peer, same server
	p.Handle([]flow.Record{vpnRec(vpnServer, vpnNAT, 443, 60118, vpnWAN, vpnWG, 6000)})
	if got := vpnBooked(t, p); got[vpnPeerA][1] != 0 || got[vpnPeerB][1] != 0 || got[store.VPNUnassignedMAC][1] != 0 {
		t.Fatalf("booked while it should wait: %v", got)
	}
	if vpnCounter(p, "vpn_replies_held") != 1 {
		t.Errorf("counters: %v", p.Diagnostics())
	}
	p.Handle([]flow.Record{vpnRec(vpnPeerA, vpnServer, 60118, 443, vpnWG, vpnWAN, 200)})
	got := vpnBooked(t, p)
	if a := got[vpnPeerA]; a != [2]int64{200, 6000} {
		t.Errorf("peer A %v, all %v", a, got)
	}
	if a := got[vpnPeerB]; a != [2]int64{700, 0} {
		t.Errorf("peer B %v", a)
	}
	if _, ok := got[store.VPNUnassignedMAC]; ok {
		t.Errorf("booked as not attributable: %v", got)
	}
	if vpnCounter(p, "vpn_replies_late_matched") != 1 || vpnCounter(p, "vpn_replies_matched") != 1 || vpnCounter(p, "vpn_replies_held") != 0 {
		t.Errorf("counters: %v", p.Diagnostics())
	}
	vpnExpire(p)
	if got := vpnBooked(t, p); got[vpnPeerA] != [2]int64{200, 6000} {
		t.Errorf("booked twice: %v", got)
	}
}

// A reply waits as long as the exporter says, and not longer. It keeps its own timestamp.
func TestVPNReplyHoldTime(t *testing.T) {
	p := vpnPipe(t, vpnPeerA, vpnPeerB)
	ifc, _ := p.VPNIf("gw", vpnWG)
	ifc.HoldSec = 200
	p.VPNIf = func(dev string, idx uint32) (VPNIface, bool) { return ifc, dev == "gw" && idx == vpnWG }
	start := time.Now()
	rec := vpnRec(vpnServer, vpnNAT, 443, 60118, vpnWAN, vpnWG, 900)
	rec.End = start.Add(-5 * time.Minute)
	p.Handle([]flow.Record{rec})
	p.SweepVPN(start.Add(190 * time.Second))
	if vpnCounter(p, "vpn_replies_held") != 1 || vpnCounter(p, "vpn_replies_unassigned") != 0 {
		t.Fatalf("ended the wait too early: %v", p.Diagnostics())
	}
	p.SweepVPN(start.Add(202 * time.Second))
	if vpnCounter(p, "vpn_replies_held") != 0 || vpnCounter(p, "vpn_replies_unassigned") != 1 {
		t.Fatalf("still waiting: %v", p.Diagnostics())
	}
	p.St.Flush()
	var ts int64
	if err := p.St.DB.QueryRow(`SELECT ts FROM flows WHERE mac = ?`, store.VPNUnassignedMAC).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	if ts != rec.End.Unix() {
		t.Errorf("timestamp %d, want %d", ts, rec.End.Unix())
	}

	p = vpnPipe(t, vpnPeerA, vpnPeerB) // exporter's timeout not known
	start = time.Now()
	p.Handle([]flow.Record{vpnRec(vpnServer, vpnNAT, 443, 60118, vpnWAN, vpnWG, 900)})
	p.SweepVPN(start.Add((vpnHoldDefault - 10) * time.Second))
	if vpnCounter(p, "vpn_replies_held") != 1 {
		t.Errorf("default wait too short: %v", p.Diagnostics())
	}
	p.SweepVPN(start.Add((vpnHoldDefault + 2) * time.Second))
	if vpnCounter(p, "vpn_replies_held") != 0 {
		t.Errorf("default wait too long: %v", p.Diagnostics())
	}
}

// The connection table names the source while the reply waits. It is asked only for the connections that wait.
func TestVPNReplyConnectionTable(t *testing.T) {
	p := vpnPipe(t, vpnPeerA, vpnPeerB)
	var asked []VPNConnQuery
	p.VPNConn = func(dev string, q VPNConnQuery) (netip.Addr, bool) {
		asked = append(asked, q)
		if dev == "gw" && q.NATPort == 61000 {
			return netip.MustParseAddr(vpnPeerB), true
		}
		return netip.Addr{}, false
	}
	p.Handle([]flow.Record{
		vpnRec(vpnServer, vpnNAT, 443, 61000, vpnWAN, vpnWG, 900),
		vpnRec(vpnServer, vpnNAT, 443, 61001, vpnWAN, vpnWG, 50),
	})
	now := time.Now()
	p.SweepVPN(now)
	if a := vpnBooked(t, p)[vpnPeerB]; a != [2]int64{0, 900} {
		t.Errorf("peer B %v", a)
	}
	want := VPNConnQuery{NAT: netip.MustParseAddr(vpnNAT), Server: netip.MustParseAddr(vpnServer), NATPort: 61000, ServerPort: 443, Proto: 6}
	if len(asked) != 2 || (asked[0] != want && asked[1] != want) {
		t.Errorf("asked %+v", asked)
	}
	if vpnCounter(p, "vpn_replies_conntrack") != 1 || vpnCounter(p, "vpn_replies_held") != 1 {
		t.Errorf("counters: %v", p.Diagnostics())
	}
	p.SweepVPN(now.Add(10 * time.Second)) // the other connection is not asked again at once
	if len(asked) != 2 {
		t.Errorf("asked again: %+v", asked)
	}
	p.SweepVPN(now.Add(vpnConnEvery * time.Second))
	if len(asked) != 3 {
		t.Errorf("no second lookup: %+v", asked)
	}
	vpnExpire(p)
	if a := vpnBooked(t, p)[store.VPNUnassignedMAC]; a != [2]int64{0, 50} {
		t.Errorf("unassigned %v", a)
	}
}

// An answer that names the router itself, as a connection of its own would, is no peer.
func TestVPNReplyConnectionTableRouterOwned(t *testing.T) {
	p := vpnPipe(t, vpnPeerA, vpnPeerB)
	p.VPNConn = func(string, VPNConnQuery) (netip.Addr, bool) { return netip.MustParseAddr(vpnNAT), true }
	p.Handle([]flow.Record{vpnRec(vpnServer, vpnNAT, 443, 61000, vpnWAN, vpnWG, 900)})
	vpnExpire(p)
	if a := vpnBooked(t, p)[store.VPNUnassignedMAC]; a != [2]int64{0, 900} {
		t.Errorf("unassigned %v", a)
	}
}

// Stopping mtmon books what still waits, and a full buffer books at once: the bytes are never lost.
func TestVPNReplyNothingLost(t *testing.T) {
	p := vpnPipe(t, vpnPeerA, vpnPeerB)
	p.VPNConn = func(string, VPNConnQuery) (netip.Addr, bool) {
		t.Error("asked a router while stopping")
		return netip.Addr{}, false
	}
	p.Handle([]flow.Record{vpnRec(vpnServer, vpnNAT, 443, 61000, vpnWAN, vpnWG, 900)})
	p.FlushVPN()
	if a := vpnBooked(t, p)[store.VPNUnassignedMAC]; a != [2]int64{0, 900} {
		t.Errorf("after stop: %v", a)
	}

	p = vpnPipe(t, vpnPeerA, vpnPeerB)
	p.vpnHeldN = vpnHeldMax
	p.Handle([]flow.Record{vpnRec(vpnServer, vpnNAT, 443, 61000, vpnWAN, vpnWG, 300)})
	if a := vpnBooked(t, p)[store.VPNUnassignedMAC]; a != [2]int64{0, 300} {
		t.Errorf("buffer full: %v", a)
	}
}

// What stays open is one row in the top clients, so that the total stays right.
func TestVPNUnassignedRowInTopClients(t *testing.T) {
	p := vpnPipe(t, vpnPeerA, vpnPeerB)
	p.Handle([]flow.Record{
		vpnRec(vpnPeerA, vpnServer, 40000, 443, vpnWG, vpnWAN, 100),
		vpnRec(vpnServer, vpnNAT, 443, 40000, vpnWAN, vpnWG, 900),
		vpnRec("93.184.216.99", vpnNAT, 80, 61001, vpnWAN, vpnWG, 50),
	})
	vpnExpire(p)
	p.St.Flush()
	top, err := p.St.TopBy("clients", time.Now().Add(-time.Hour).Unix(), 10, "")
	if err != nil {
		t.Fatal(err)
	}
	var total, row int64
	label := ""
	for _, r := range top {
		total += r.Up + r.Down
		if r.Key == store.VPNUnassignedMAC {
			row, label = r.Up+r.Down, r.Label
		}
	}
	if row != 50 || label != store.VPNUnassignedName || total != 1050 {
		t.Errorf("row %d %q, total %d, top %+v", row, label, total, top)
	}
}

// The router changed the source port: server and server port still name the peer, as long as only one talks to it.
// With several peers active that is only known at the end of the wait, as another peer's own upload could still come.
func TestVPNReplyPortRewritten(t *testing.T) {
	p := vpnPipe(t, vpnPeerA, vpnPeerB)
	p.Handle([]flow.Record{
		vpnRec(vpnPeerA, vpnServer, 40000, 443, vpnWG, vpnWAN, 100),
		vpnRec(vpnServer, vpnNAT, 443, 61000, vpnWAN, vpnWG, 900),
	})
	vpnExpire(p)
	if a := vpnBooked(t, p)[vpnPeerA]; a != [2]int64{100, 900} {
		t.Errorf("peer A %v", a)
	}
}

// One peer active and no upload seen: that peer. The reply is not lost and not shown as unattributable.
func TestVPNReplySinglePeerFallback(t *testing.T) {
	p := vpnPipe(t, vpnPeerA)
	p.Handle([]flow.Record{vpnRec(vpnServer, vpnNAT, 80, 60118, vpnWAN, vpnWG, 5000)})
	if a := vpnBooked(t, p)[vpnPeerA]; a != [2]int64{0, 5000} {
		t.Errorf("peer A %v", a)
	}
	if vpnCounter(p, "vpn_replies_single_peer") != 1 {
		t.Errorf("counters: %v", p.Diagnostics())
	}
}

// Several peers, no upload, or two peers on one server with a rewritten port: the bytes stay visible on their own
// client and the diagnostics count them. Nothing is guessed and nothing dropped.
func TestVPNReplyUnassigned(t *testing.T) {
	p := vpnPipe(t, vpnPeerA, vpnPeerB)
	p.Handle([]flow.Record{
		vpnRec(vpnPeerA, vpnServer, 40000, 443, vpnWG, vpnWAN, 100),
		vpnRec(vpnPeerB, vpnServer, 40001, 443, vpnWG, vpnWAN, 100),
		vpnRec(vpnServer, vpnNAT, 443, 61000, vpnWAN, vpnWG, 900),
		vpnRec("93.184.216.99", vpnNAT, 80, 61001, vpnWAN, vpnWG, 50),
	})
	vpnExpire(p)
	got := vpnBooked(t, p)
	if a := got[store.VPNUnassignedMAC]; a != [2]int64{0, 950} {
		t.Errorf("unassigned %v, all %v", a, got)
	}
	if got[vpnPeerA][1] != 0 || got[vpnPeerB][1] != 0 {
		t.Errorf("guessed a peer: %v", got)
	}
	d := p.Diagnostics()
	if vpnCounter(p, "vpn_replies_unassigned") != 2 || d["vpn_replies_unassigned_bytes"].(uint64) != 950 {
		t.Errorf("counters: %v", d)
	}
	cs, err := p.St.Clients()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cs {
		found = found || c.MAC == store.VPNUnassignedMAC && c.Hostname == store.VPNUnassignedName && c.Vendor == store.VPNVendor
	}
	if !found {
		t.Errorf("client missing: %+v", cs)
	}
}

// What is no reply into a tunnel stays as it was: the post-NAT copy towards the server, a flow to the NAT address that
// leaves through another interface, a local client's download. A router that also exports the flow with the peer as
// destination is not counted twice.
func TestVPNReplyLeavesOtherFlowsAlone(t *testing.T) {
	p := vpnPipe(t, vpnPeerA)
	p.Handle([]flow.Record{
		vpnRec(vpnNAT, vpnServer, 60118, 80, vpnWG, vpnWAN, 11),     // the router's translated copy
		vpnRec(vpnServer, vpnNAT, 80, 60118, vpnWAN, 0, 22),         // terminates on the router
		vpnRec(vpnServer, vpnNAT, 80, 60119, vpnWAN, 7, 33),         // another interface
		vpnRec(vpnServer, "192.168.0.50", 80, 60120, vpnWAN, 7, 44), // a LAN client
	})
	got := vpnBooked(t, p)
	if len(got) != 1 || got["192.168.0.50"] != [2]int64{0, 44} {
		t.Errorf("booked %v", got)
	}

	p = vpnPipe(t, vpnPeerA, vpnPeerB)
	p.Handle([]flow.Record{
		vpnRec(vpnPeerA, vpnServer, 60118, 80, vpnWG, vpnWAN, 2000),
		vpnRec(vpnServer, vpnPeerA, 80, 60118, vpnWAN, vpnWG, 5000), // the peer as destination
	})
	p.Handle([]flow.Record{vpnRec(vpnServer, vpnNAT, 80, 60118, vpnWAN, vpnWG, 5000)})
	if a := vpnBooked(t, p)[vpnPeerA]; a != [2]int64{2000, 5000} {
		t.Errorf("peer A %v, want the download once", a)
	}
}
