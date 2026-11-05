// SPDX-License-Identifier: AGPL-3.0-only

package live

import (
	"context"
	"net/netip"
	"time"

	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// A VPN client behind a masquerading router (#287): the upload is exported with the peer as source, so it lands on the
// peer. The download is exported with the address the router translated to as destination, for example
// src=<server> dst=<router NAT address> out_if=<wg>: no client address anywhere, and the bytes would be dropped as
// transit. The egress interface says the bytes went into the tunnel, so they belong to a peer of that interface.
//
// Attribution, best evidence first:
//  1. an upload of a peer on that interface with the reversed tuple (server, server port, NAT port = peer port; the
//     router keeps the source port unless it collides)
//  2. with at most one peer active: the only peer that talked to this server and port recently (the router changed the
//     port), then the only peer with a recent handshake
//  3. otherwise the reply waits in a buffer, at most HoldSec, because each half of a flow has its own export timers and
//     the upload can come later. While it waits, 1 can still match and the router's connection table is asked which
//     source the NAT port belongs to. When the time is up: the only peer that talked to this server and port recently,
//     else the client "VPN (not attributable)".
//
// The bytes are never dropped, and a reply that waited keeps its own timestamp.

// VPNIface describes an exporter interface that is a WireGuard tunnel. Active lists, per peer with a recent handshake,
// its first host address of each address family. HoldSec is how long a reply waits for its upload, 0 = not known.
type VPNIface struct {
	Active  []netip.Addr
	HoldSec int64
}

// VPNConnQuery names a NAT connection as a reply sees it: the reply goes from Server:ServerPort to NAT:NATPort.
type VPNConnQuery struct {
	NAT, Server         netip.Addr
	NATPort, ServerPort uint16
	Proto               uint8
}

const (
	// vpnUploadWindow is how long, in seconds, an upload of a peer can explain a reply, at least. A longer hold time
	// (HoldSec) widens it: the two halves of a flow can then be further apart.
	vpnUploadWindow = 600
	vpnHoldDefault  = 60   // seconds a reply waits when the exporter's active-flow-timeout is not known
	vpnHoldMax      = 3600 // upper limit of the wait: twice the RouterOS default of 30 minutes
	vpnHeldMax      = 5000 // replies in the buffer; the next one is booked as not attributable at once
	vpnConnEvery    = 60   // seconds between two connection-table lookups of one NAT connection
	vpnConnPerSweep = 16   // connection-table lookups per sweep and device
	vpnSweepEvery   = 10 * time.Second
)

// vpnKey identifies a peer's connection as the reply sees it. nat 0 is the entry for "any port".
type vpnKey struct {
	dev        string
	ifx        uint32
	rip        netip.Addr
	rport, nat uint16
	proto      uint8
}

type vpnSeen struct {
	peer      netip.Addr
	ts        int64
	ambiguous bool // two peers talked to this server and port in the window
}

// vpnHeldRec is a reply waiting in the buffer; row holds everything except the attribution.
type vpnHeldRec struct {
	r        flow.Record
	src, dst netip.Addr
	row      store.FlowRow
	deadline int64
	window   int64 // how old an upload may be that explains this reply
}

// vpnHeldSet is all waiting replies of one NAT connection.
type vpnHeldSet struct {
	recs     []vpnHeldRec
	nextConn int64 // earliest next lookup in the connection table
}

// vpnLate is a waiting reply whose upload arrived.
type vpnLate struct {
	vpnHeldRec
	peer netip.Addr
}

type vpnVerdict int

const (
	vpnNone       vpnVerdict = iota // not a reply into a tunnel
	vpnMatched                      // on the peer that sent the upload
	vpnSinglePeer                   // on the only active peer
	vpnUnassigned                   // on the client "VPN (not attributable)"
	vpnDuplicate                    // the exporter also reported the flow with the peer as destination
	vpnHold                         // waits in the buffer: its upload may still arrive
	vpnConntrack                    // on the source the router's connection table names
)

// routerOwned reports whether a managed router has this address configured.
func (p *Pipeline) routerOwned(a netip.Addr) bool {
	ip := a.String()
	return p.SelfMAC != nil && p.SelfMAC(ip) != "" || p.Owns != nil && p.Owns(ip)
}

// noteVPNUploads remembers which peer talks to which server, before the batch is booked: an upload and its reply
// often arrive in one batch, in either order. It returns the waiting replies these uploads explain.
func (p *Pipeline) noteVPNUploads(recs []flow.Record, ts int64) []vpnLate {
	if p.VPNIf == nil {
		return nil
	}
	var late []vpnLate
	for _, r := range recs {
		if r.InIf == 0 || r.InIf == r.OutIf {
			continue
		}
		dev := p.Cfg.SourceDevice(r.Exporter)
		if dev == "" {
			continue
		}
		if _, ok := p.VPNIf(dev, r.InIf); !ok {
			continue
		}
		src, dst := r.Src.Unmap(), r.Dst.Unmap()
		if p.Cfg.IsLocal(dst) || p.routerOwned(src) {
			continue // no translation towards a local server; the router's own traffic has no peer
		}
		p.mu.Lock()
		if p.vpnUp == nil {
			p.vpnUp = map[vpnKey]vpnSeen{}
		}
		strict := vpnKey{dev, r.InIf, dst, r.DstPort, r.SrcPort, r.Proto}
		p.vpnUp[strict] = vpnSeen{peer: src, ts: ts}
		for _, h := range p.takeHeldLocked(strict) {
			late = append(late, vpnLate{h, src})
		}
		loose := vpnKey{dev, r.InIf, dst, r.DstPort, 0, r.Proto}
		v, ok := p.vpnUp[loose]
		live := ok && ts-v.ts <= vpnUploadWindow
		p.vpnUp[loose] = vpnSeen{peer: src, ts: ts, ambiguous: live && (v.ambiguous || v.peer != src)}
		if len(p.vpnUp) > 50000 {
			for k, v := range p.vpnUp {
				if ts-v.ts > vpnHoldMax {
					delete(p.vpnUp, k)
				}
			}
		}
		p.mu.Unlock()
	}
	return late
}

// vpnReply decides whether r is a reply into a tunnel, and which peer it belongs to (see the top of this file).
func (p *Pipeline) vpnReply(r flow.Record, src, dst netip.Addr, sl bool, ts int64) (netip.Addr, vpnVerdict) {
	if p.VPNIf == nil || r.OutIf == 0 || sl || !p.routerOwned(dst) || p.routerOwned(src) {
		return netip.Addr{}, vpnNone
	}
	dev := p.Cfg.SourceDevice(r.Exporter)
	if dev == "" {
		return netip.Addr{}, vpnNone
	}
	info, ok := p.VPNIf(dev, r.OutIf)
	if !ok {
		return netip.Addr{}, vpnNone
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	window := max(int64(vpnUploadWindow), info.HoldSec)
	if v, ok := p.vpnUp[vpnKey{dev, r.OutIf, src, r.SrcPort, r.DstPort, r.Proto}]; ok && ts-v.ts <= window {
		if s, ok := p.xseen[xkey{r.Src, v.peer, r.SrcPort, r.DstPort, r.Proto}]; ok && ts-s.ts <= xWindow {
			return v.peer, vpnDuplicate
		}
		return v.peer, vpnMatched
	}
	var only netip.Addr
	n := 0
	for _, a := range info.Active {
		if a.Is4() == dst.Is4() {
			only = a
			n++
		}
	}
	if n >= 2 { // another peer's upload to the same server must not win over the later upload of this reply
		return netip.Addr{}, vpnHold
	}
	if v, ok := p.vpnUp[vpnKey{dev, r.OutIf, src, r.SrcPort, 0, r.Proto}]; ok && !v.ambiguous && ts-v.ts <= window {
		return v.peer, vpnMatched
	}
	if n == 1 {
		return only, vpnSinglePeer
	}
	return netip.Addr{}, vpnHold
}

// holdVPN puts a reply into the buffer. false: the buffer is full.
func (p *Pipeline) holdVPN(r flow.Record, src, dst netip.Addr, row store.FlowRow, now int64) bool {
	dev := p.Cfg.SourceDevice(r.Exporter)
	info, _ := p.VPNIf(dev, r.OutIf)
	hold := info.HoldSec
	if hold <= 0 {
		hold = vpnHoldDefault
	}
	hold = min(hold, vpnHoldMax)
	k := vpnKey{dev, r.OutIf, src, r.SrcPort, r.DstPort, r.Proto}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.vpnHeldN >= vpnHeldMax {
		return false
	}
	if p.vpnHeld == nil {
		p.vpnHeld = map[vpnKey]*vpnHeldSet{}
	}
	set := p.vpnHeld[k]
	if set == nil {
		set = &vpnHeldSet{}
		p.vpnHeld[k] = set
	}
	set.recs = append(set.recs, vpnHeldRec{r: r, src: src, dst: dst, row: row, deadline: now + hold, window: max(int64(vpnUploadWindow), hold)})
	p.vpnHeldN++
	p.vpnHeldB += r.Bytes
	p.diag.vpnHeldState(p.vpnHeldN, p.vpnHeldB)
	return true
}

// takeHeldLocked removes all waiting replies of a connection from the buffer.
func (p *Pipeline) takeHeldLocked(k vpnKey) []vpnHeldRec {
	set := p.vpnHeld[k]
	if set == nil {
		return nil
	}
	delete(p.vpnHeld, k)
	p.forgetHeldLocked(set.recs)
	return set.recs
}

func (p *Pipeline) forgetHeldLocked(recs []vpnHeldRec) {
	for _, h := range recs {
		p.vpnHeldN--
		p.vpnHeldB -= h.r.Bytes
	}
	p.diag.vpnHeldState(p.vpnHeldN, p.vpnHeldB)
}

// RunVPNSweep ends the wait of buffered replies and asks the routers about the waiting NAT connections.
func (p *Pipeline) RunVPNSweep(ctx context.Context) {
	t := time.NewTicker(vpnSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			p.SweepVPN(now)
		}
	}
}

// SweepVPN attributes the replies the connection table can explain and ends the wait of those whose time is up.
func (p *Pipeline) SweepVPN(now time.Time) { p.sweepVPN(now, false) }

// FlushVPN books everything that still waits, without asking a router: mtmon is stopping and nothing may be lost.
func (p *Pipeline) FlushVPN() { p.sweepVPN(time.Now(), true) }

func (p *Pipeline) sweepVPN(now time.Time, flush bool) {
	t := now.Unix()
	found := p.askVPNConn(t, flush)
	var book []vpnBooking
	p.mu.Lock()
	for k, set := range p.vpnHeld {
		if peer, ok := found[k]; ok {
			for _, h := range p.takeHeldLocked(k) {
				book = append(book, vpnBooking{h, peer, vpnConntrack})
			}
			continue
		}
		book = append(book, p.takeDueLocked(k, set, t, flush)...)
	}
	p.mu.Unlock()

	rows := make([]store.FlowRow, 0, len(book))
	for _, b := range book {
		if row, ok := p.bookVPN(b.h, b.peer, b.v); ok {
			rows = append(rows, row)
		}
	}
	if len(rows) > 0 {
		p.St.Enqueue(rows...)
	}
}

// vpnBooking is the verdict on a reply leaving the buffer.
type vpnBooking struct {
	h    vpnHeldRec
	peer netip.Addr
	v    vpnVerdict
}

// askVPNConn asks the routers about waiting NAT connections that are due for a lookup. The calls run outside the lock,
// the flow path needs it.
func (p *Pipeline) askVPNConn(t int64, flush bool) map[vpnKey]netip.Addr {
	if flush || p.VPNConn == nil {
		return nil
	}
	type question struct {
		k vpnKey
		q VPNConnQuery
	}
	var ask []question
	budget := map[string]int{}
	p.mu.Lock()
	for k, set := range p.vpnHeld {
		// IPv4 only: IPv6 connections are in another menu, and without NAT their replies name the peer themselves
		if k.rip.Is4() && k.nat != 0 && set.nextConn <= t && budget[k.dev] < vpnConnPerSweep {
			budget[k.dev]++
			set.nextConn = t + vpnConnEvery
			ask = append(ask, question{k, VPNConnQuery{NAT: set.recs[0].dst, Server: k.rip, NATPort: k.nat, ServerPort: k.rport, Proto: k.proto}})
		}
	}
	p.mu.Unlock()
	found := map[vpnKey]netip.Addr{}
	for _, a := range ask {
		if peer, ok := p.VPNConn(a.k.dev, a.q); ok && peer.IsValid() && !p.routerOwned(peer) {
			found[a.k] = peer
		}
	}
	return found
}

// takeDueLocked removes the replies of a connection whose wait is over and decides on them: the only peer that talked
// to this server and port recently, else not attributable.
func (p *Pipeline) takeDueLocked(k vpnKey, set *vpnHeldSet, t int64, flush bool) []vpnBooking {
	var due, keep []vpnHeldRec
	for _, h := range set.recs {
		if flush || h.deadline <= t {
			due = append(due, h)
		} else {
			keep = append(keep, h)
		}
	}
	if len(due) == 0 {
		return nil
	}
	p.forgetHeldLocked(due)
	if set.recs = keep; len(keep) == 0 {
		delete(p.vpnHeld, k)
	}
	loose, ok := p.vpnUp[vpnKey{k.dev, k.ifx, k.rip, k.rport, 0, k.proto}]
	book := make([]vpnBooking, 0, len(due))
	for _, h := range due {
		if ok && !loose.ambiguous && t-loose.ts <= h.window {
			book = append(book, vpnBooking{h, loose.peer, vpnMatched})
		} else {
			book = append(book, vpnBooking{h, netip.Addr{}, vpnUnassigned})
		}
	}
	return book
}

// bookVPN attributes a reply that waited in the buffer. false: the exporter also reported it with the peer as destination.
func (p *Pipeline) bookVPN(h vpnHeldRec, peer netip.Addr, v vpnVerdict) (store.FlowRow, bool) {
	if v != vpnUnassigned {
		p.mu.Lock()
		s, ok := p.xseen[xkey{h.r.Src, peer, h.r.SrcPort, h.r.DstPort, h.r.Proto}]
		if ok && h.row.TS-s.ts <= xWindow && s.ts-h.row.TS <= xWindow {
			p.Ignored++
			p.mu.Unlock()
			p.diag.note(h.r, "x", "vpn reply, also exported with the peer as destination")
			return store.FlowRow{}, false
		}
		p.mu.Unlock()
	}
	row := h.row
	p.fillVPNRow(&row, h.r, h.src, h.dst, peer, v)
	p.finishRow(&row, h.r, h.src, h.dst)
	return row, true
}

// fillVPNRow sets the client of a reply into a tunnel and counts it.
func (p *Pipeline) fillVPNRow(row *store.FlowRow, r flow.Record, src, dst, peer netip.Addr, v vpnVerdict) {
	p.diag.vpnReply(v, r.Bytes)
	p.diag.note(r, "d")
	client := peer
	if v == vpnUnassigned {
		client = dst
		p.ensureVPNUnassigned(p.Cfg.SourceDevice(r.Exporter))
		row.MAC = store.VPNUnassignedMAC
	}
	row.Dir, row.CIP, row.RIP, row.CPort, row.RPort = "d", client.String(), src.String(), r.DstPort, r.SrcPort
}

// ensureVPNUnassigned creates the client the unattributable bytes are booked on, the first time it is needed.
func (p *Pipeline) ensureVPNUnassigned(dev string) {
	p.mu.Lock()
	done := p.vpnClient
	p.vpnClient = true
	p.mu.Unlock()
	if done {
		return
	}
	p.St.UpsertClients([]store.Observation{{MAC: store.VPNUnassignedMAC, Hostname: store.VPNUnassignedName, Vendor: store.VPNVendor, Device: dev}})
}
